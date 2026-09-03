package main

import (
	"context"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"quota-viewer/internal/config"
	"quota-viewer/internal/fetcher"
	"quota-viewer/internal/tray"
	"quota-viewer/internal/updater"
	"sync/atomic"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx     context.Context
	cfg     *config.Config
	mu      sync.Mutex
	cache   []fetcher.QuotaResult
	tray    *tray.TrayHandler
	visible atomic.Bool
	stopAuto chan struct{} // 当前自动刷新 goroutine 的停止信号(startAutoRefresh 重启时关闭旧的)

	// 展开/收起窗口状态:savedX/savedY 记录展开前悬浮球位置
	expanded bool
	savedX   int
	savedY   int
}

func NewApp() *App {
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		cfg = config.Default()
	}
	return &App{
		cfg: cfg,
	}
}

func (a *App) OnStartup(ctx context.Context) {
	a.ctx = ctx
	a.visible.Store(true)

	// Windows 对 overlapped 窗口强制默认最小宽度(高 DPI 下实测约 262px 物理),
	// 导致 60px 球窗被撑宽、球体偏左。安装子类覆盖 WM_GETMINMAXINFO,
	// 并设为工具窗口(去任务栏按钮/缩略图),再把球窗规整到精确的 60x60。
	setupWindowStyles("Quota Viewer")
	wailsruntime.WindowSetMinSize(ctx, ballSize, ballSize)
	wailsruntime.WindowSetSize(ctx, ballSize, ballSize)
	// 样式切换后重申置顶,防止球窗被其它窗口盖住
	wailsruntime.WindowSetAlwaysOnTop(ctx, true)

	// 应用已保存的界面透明度(窗口级 alpha,见 setWindowOpacity)
	setWindowOpacity(a.cfg.Opacity)

	// 恢复悬浮球位置(配置中 BallX/BallY >= 0 时生效)
	// BallX/BallY 来自 WindowGetPosition 返回的虚拟桌面绝对坐标,
	// 需要 fitToScreen 转为显示器相对坐标后再传给 WindowSetPosition
	if a.cfg.BallX >= 0 && a.cfg.BallY >= 0 {
		if nx, ny, ok := fitToScreen(ctx, a.cfg.BallX, a.cfg.BallY, ballSize, ballSize); ok {
			wailsruntime.WindowSetPosition(ctx, nx, ny)
		}
	}

	// 设置系统托盘菜单(刷新/显示隐藏/打开配置/退出)
	a.tray = tray.New(ctx)
	a.tray.Start()

	// 监听托盘事件并转发到对应行为
	wailsruntime.EventsOn(ctx, "tray:refresh", func(...interface{}) {
		a.Refresh()
	})
	wailsruntime.EventsOn(ctx, "tray:toggle", func(...interface{}) {
		// Wails v2.12.0 无 WindowIsVisible,用本地可见性状态切换
		if a.visible.Load() {
			a.visible.Store(false)
			wailsruntime.WindowHide(ctx)
		} else {
			a.visible.Store(true)
			wailsruntime.WindowShow(ctx)
		}
	})
	wailsruntime.EventsOn(ctx, "tray:settings", func(...interface{}) {
		// 窗口被隐藏时先从托盘唤出,否则配置面板不可见
		a.visible.Store(true)
		wailsruntime.WindowShow(ctx)
		wailsruntime.EventsEmit(ctx, "ui:show-settings")
	})

	// 启动后台自动刷新(startAutoRefresh 内部自起 goroutine)
	a.startAutoRefresh()

	// 启动自动升级检查(dev 构建或未配置清单时内部直接跳过)
	go updater.StartAuto(ctx, Version, func(version string) {
		wailsruntime.EventsEmit(ctx, "update:applying", version)
	})
}

// GetVersion 返回应用版本号(发布构建经 ldflags 注入,dev 构建为 "dev")。
func (a *App) GetVersion() string {
	return Version
}

// OnShutdown 在应用退出时清理托盘图标。
func (a *App) OnShutdown(ctx context.Context) {
	if a.tray != nil {
		a.tray.Quit()
	}
}

// Refresh 抓取并推送事件到前端。
// 订阅模式:不抓取本地,下载远端加密状态直接展示;
// 发布模式:本地抓取后异步加密上传 OSS(不阻塞返回)。
func (a *App) Refresh() []fetcher.QuotaResult {
	a.mu.Lock()
	mode := a.cfg.Sync.Mode
	a.mu.Unlock()

	if mode == config.SyncModeSubscribe {
		a.fetchRemoteState()
		a.mu.Lock()
		results := a.cache
		a.mu.Unlock()
		return results
	}

	results := a.fetchAll()
	a.mu.Lock()
	a.cache = results
	a.mu.Unlock()

	// 推送事件到前端
	wailsruntime.EventsEmit(a.ctx, "quota:update", results)

	if mode == config.SyncModePublish {
		go a.publishState(results)
	}
	return results
}

// ProviderInput 是前端提交的 Provider 状态。
// 凭证字段空字符串 = 不修改(避免掩码回写覆盖真实值)。
type ProviderInput struct {
	ID           string              `json:"id"`
	Enabled      bool                `json:"enabled"`
	Creds        map[string]string   `json:"creds"`         // 旧前端单凭证(兼容;优先使用 Keys)
	Keys         []map[string]string `json:"keys"`          // 多组凭证(每组一套字段值)
	KeyNames     []string            `json:"keyNames"`      // 各凭证组的显示名(与 Keys 对齐)
	Budgets      []float64           `json:"budgets"`       // 各凭证组的预算(与 Keys 对齐)
	Budget       float64             `json:"budget"`        // 旧前端渠道级预算(兼容;忽略,由 Budgets 取代)
	SyncExcludes []bool              `json:"sync_excludes"` // 各凭证组是否排除同步到 OSS(与 Keys 对齐)
}

// GetConfig 返回当前配置(凭证做掩码)与全部 Provider 元数据。
func (a *App) GetConfig() map[string]interface{} {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 当前配置索引
	cur := map[string]config.ProviderConfig{}
	for _, p := range a.cfg.Providers {
		cur[p.ID] = p
	}

	providers := make([]map[string]interface{}, 0, len(fetcher.GetAll()))
	for _, def := range fetcher.GetAll() {
		pc, ok := cur[def.ID]
		// plain 字段(如资源包类型选择)非敏感,回显原值不掩码
		plainKeys := make(map[string]bool, len(def.Fields))
		for _, f := range def.Fields {
			if f.Plain {
				plainKeys[f.Key] = true
			}
		}
		keys := make([]map[string]string, 0)
		if ok {
			for _, k := range pc.CredKeys() {
				masked := make(map[string]string, len(k))
				for fk, v := range k {
					if plainKeys[fk] {
						masked[fk] = v
					} else {
						masked[fk] = maskSecret(v)
					}
				}
				keys = append(keys, masked)
			}
		}
		fields := make([]map[string]interface{}, 0, len(def.Fields))
		for _, f := range def.Fields {
			fm := map[string]interface{}{
				"key": f.Key, "label": f.Label, "type": f.Type,
			}
			if len(f.Options) > 0 {
				opts := make([]map[string]string, 0, len(f.Options))
				for _, o := range f.Options {
					opts = append(opts, map[string]string{"value": o.Value, "label": o.Label})
				}
				fm["options"] = opts
			}
			if f.Multiple {
				fm["multiple"] = true
			}
			if f.Plain {
				fm["plain"] = true
			}
			fields = append(fields, fm)
		}
		providers = append(providers, map[string]interface{}{
			"id":            def.ID,
			"name":          def.DisplayName,
			"abbr":          def.Abbr,
			"kind":          def.Kind,
			"enabled":       ok && pc.Enabled,
			"login_url":     def.LoginURL,
			"fields":        fields,
			"creds":         keys,
			"keys":          keys,
			"key_names":     pc.KeyNames,
			"budgets":       pc.Budgets,
			"sync_excludes": pc.SyncExcludes,
		})
	}

	// 同步配置下发(密码与 AccessKey Secret 掩码)
	syncMasked := map[string]interface{}{
		"mode":              a.cfg.Sync.Mode,
		"password":          maskSecret(a.cfg.Sync.Password),
		"oss_endpoint":      a.cfg.Sync.OSSEndpoint,
		"oss_bucket":        a.cfg.Sync.OSSBucket,
		"oss_key":           a.cfg.Sync.OSSKey,
		"oss_access_id":     a.cfg.Sync.OSSAccessID,
		"oss_access_secret": maskSecret(a.cfg.Sync.OSSAccessSecret),
		"url":               a.cfg.Sync.URL,
	}

	return map[string]interface{}{
		"providers":            providers,
		"refresh_interval_min": a.cfg.RefreshIntervalMin,
		"ball_x":               a.cfg.BallX,
		"ball_y":               a.cfg.BallY,
		"opacity":              a.cfg.Opacity,
		"sync":                 syncMasked,
	}
}

// SetOpacity 保存界面透明度(0.2-1.0),越界钳制,并立即应用到窗口。
func (a *App) SetOpacity(opacity float64) error {
	a.mu.Lock()
	if opacity < 0.2 {
		opacity = 0.2
	}
	if opacity > 1 {
		opacity = 1
	}
	a.cfg.Opacity = opacity
	err := config.Save(a.cfg)
	a.mu.Unlock()
	setWindowOpacity(opacity)
	return err
}

// SetOpacityPreview 实时预览透明度(仅应用到窗口,不写入配置)。
// 滑块拖动过程中调用,避免频繁写盘;松开滑块由 SetOpacity 持久化。
func (a *App) SetOpacityPreview(opacity float64) {
	setWindowOpacity(opacity)
}

// settingsMode 记录当前是否处于设置界面模式(不透明)
var settingsMode bool

// SetSettingsMode 切换设置界面模式:进入设置界面时临时设置不透明,
// 离开时恢复配置中的透明度。
func (a *App) SetSettingsMode(enabled bool) {
	if enabled {
		// 进入设置界面:临时设置不透明
		settingsMode = true
		setWindowOpacity(1.0)
	} else {
		// 离开设置界面:恢复配置中的透明度
		settingsMode = false
		a.mu.Lock()
		opacity := a.cfg.Opacity
		a.mu.Unlock()
		setWindowOpacity(opacity)
	}
}

// SaveConfig 保存 Provider 配置。无上限(>=1 个启用),凭证支持多组(keys)。
func (a *App) SaveConfig(providers []ProviderInput, refreshMin int) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 1) 钳制启用数量下限:0 个 → 全部启用
	enabledCount := 0
	for _, p := range providers {
		if p.Enabled {
			enabledCount++
		}
	}
	if enabledCount == 0 {
		for i := range providers {
			providers[i].Enabled = true
		}
	}

	// 2) 合并到配置(空凭证字段 = 不修改)
	for _, in := range providers {
		def, ok := fetcher.Get(in.ID)
		if !ok {
			continue // 未知 provider 忽略
		}
		idx := -1
		for j := range a.cfg.Providers {
			if a.cfg.Providers[j].ID == in.ID {
				idx = j
				break
			}
		}
		if idx == -1 {
			a.cfg.Providers = append(a.cfg.Providers, config.ProviderConfig{ID: in.ID})
			idx = len(a.cfg.Providers) - 1
		}
		pc := &a.cfg.Providers[idx]
		pc.Enabled = in.Enabled
		// 掩码还原基准必须包含 Creds(旧版单凭证),否则旧数据会被掩码字面量覆盖
		pc.Keys = mergeKeys(pc.CredKeys(), in.Keys, in.Creds, def.Fields)
		pc.Creds = nil // 统一以 Keys 为单一数据源
		// 凭证组显示名与 Keys 对齐;组数增加/删除时按顺序截断/补齐(空名 = 回退 "Key N")
		pc.KeyNames = alignKeyNames(in.KeyNames, len(pc.Keys))
		// 各凭证组的预算与 Keys 对齐(0 = 该组未设);旧渠道级预算字段废弃
		pc.Budgets = config.AlignBudgets(in.Budgets, len(pc.Keys))
		pc.Budget = 0
		// 各凭证组的同步排除开关与 Keys 对齐(false = 同步);
		// 前端仅在发布模式渲染该开关,未提交(nil)时保留已存值
		if in.SyncExcludes != nil {
			pc.SyncExcludes = config.AlignSyncExcludes(in.SyncExcludes, len(pc.Keys))
		}
		// 凭证组数变化后旧余额基线不再对齐,重置待下次抓取重建
		if len(pc.Keys) != len(pc.LastBalances) {
			pc.LastBalances = nil
		}
	}

	// 3) 按注册表顺序重排(展示顺序固定)
	ordered := make([]config.ProviderConfig, 0, len(fetcher.GetAll()))
	seen := map[string]bool{}
	for _, def := range fetcher.GetAll() {
		for _, p := range a.cfg.Providers {
			if p.ID == def.ID {
				ordered = append(ordered, p)
				seen[p.ID] = true
				break
			}
		}
	}
	for _, p := range a.cfg.Providers {
		if !seen[p.ID] {
			ordered = append(ordered, p)
		}
	}
	a.cfg.Providers = ordered

	if refreshMin > 0 {
		a.cfg.RefreshIntervalMin = refreshMin
	}

	return config.Save(a.cfg)
}

// mergeKeys 合并前端提交的多组凭证到旧值(全量替换 + 掩码还原)。
// 前端对未修改的输入框提交 placeholder 掩码值:提交值若与任一旧组对应字段的掩码
// 一致 → 还原旧值(支持删除组后索引错位);空值 = 清空该字段;全空组跳过。
// in.Keys 为空(旧前端单凭证)时退化为 Creds 合并,结果作为单组。
func mergeKeys(old []map[string]string, keys []map[string]string, creds map[string]string, fields []fetcher.CredentialField) []map[string]string {
	// 旧前端退化路径:无 keys 时用 creds 合并
	if len(keys) == 0 {
		if len(creds) == 0 {
			return old
		}
		merged := map[string]string{}
		if len(old) > 0 {
			for _, f := range fields {
				merged[f.Key] = old[0][f.Key]
			}
		}
		for _, f := range fields {
			if v, ok := creds[f.Key]; ok && v != "" {
				merged[f.Key] = config.NormalizeCookieInput(v)
			}
		}
		if len(merged) == 0 {
			return nil
		}
		return []map[string]string{merged}
	}

	// 提交值是否匹配某旧组对应字段的掩码(掩码占位符 → 还原旧值)
	restore := func(f fetcher.CredentialField, v string) (string, bool) {
		if v == "" {
			return "", false
		}
		for _, og := range old {
			if og[f.Key] != "" && maskSecret(og[f.Key]) == v {
				return og[f.Key], true
			}
		}
		return config.NormalizeCookieInput(v), false
	}

	out := make([]map[string]string, 0, len(keys))
	for _, k := range keys {
		group := map[string]string{}
		for _, f := range fields {
			v, _ := restore(f, k[f.Key])
			if v == "" {
				continue // 空 = 清空该字段
			}
			group[f.Key] = v
		}
		if len(group) == 0 {
			continue // 全空组跳过
		}
		out = append(out, group)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// alignKeyNames 把前端提交的凭证组显示名与 Keys 对齐:
// 保证返回 slice 与 keys 等长,不足补空(空名 = 详情页回退 "Key N"),超出截断。
func alignKeyNames(names []string, n int) []string {
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		var name string
		if i < len(names) {
			name = names[i]
		}
		out = append(out, name)
	}
	return out
}

// TestConnection 测试单个平台连接是否可用。
func (a *App) TestConnection(platform string) string {
	def, ok := fetcher.Get(platform)
	if !ok {
		return "未知平台"
	}

	a.mu.Lock()
	var creds map[string]string
	for _, p := range a.cfg.Providers {
		if p.ID == platform {
			if keys := p.CredKeys(); len(keys) > 0 {
				creds = keys[0] // 测试用第一组凭证
			}
			break
		}
	}
	a.mu.Unlock()

	result := def.Build(creds).Fetch()
	if result.Error != "" {
		return "失败: " + result.Error
	}
	return "成功: " + result.Remaining
}

// OpenLoginPage 用默认浏览器打开 URL。
func (a *App) OpenLoginPage(url string) {
	openBrowser(url)
}

// SaveBallPosition 保存悬浮球位置。
func (a *App) SaveBallPosition(x, y int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.BallX = x
	a.cfg.BallY = y
	return config.Save(a.cfg)
}

// ballSize 是悬浮球(收起态)窗口边长,与前端 SIZES.ball 保持一致。
// 运行时随渠道数动态变化(见 SetBallSize),workarea_windows.go 的子类回调也读取它。
var ballSize = 60

// screenMargin 是展开后面板与屏幕边缘保留的间距。
const screenMargin = 8

// SetBallSize 前端渲染完 ball 网格后调用:更新最小/当前窗口尺寸并校正位置。
func (a *App) SetBallSize(size int) {
	if size < ballSizeMin {
		size = ballSizeMin
	}
	if size == ballSize {
		return
	}
	ballSize = size
	wailsruntime.WindowSetMinSize(a.ctx, ballSize, ballSize)
	wailsruntime.WindowSetSize(a.ctx, ballSize, ballSize)
	// 尺寸变化后重新钳制位置,保证球完整落在屏幕内
	x, y := wailsruntime.WindowGetPosition(a.ctx)
	if nx, ny, ok := fitToScreen(a.ctx, x, y, ballSize, ballSize); ok {
		wailsruntime.WindowSetPosition(a.ctx, nx, ny)
	}
}

// ballSizeMin 是悬浮球最小边长(逻辑像素)。
const ballSizeMin = 60

// ExpandWindow 调整窗口尺寸并重新定位,保证面板完整落在当前屏幕内:
// 默认从球的位置向右下展开,放不下则翻转到左上;宽/高均钳制在工作区内
// (高度超出由前端内容区滚动,宽度超出防止窗口远超屏宽)。
// 首次展开时记录悬浮球位置,供 CollapseWindow 精确恢复。
func (a *App) ExpandWindow(w, h int) {
	a.mu.Lock()
	if !a.expanded {
		a.savedX, a.savedY = wailsruntime.WindowGetPosition(a.ctx)
		a.expanded = true
	}
	x, y := a.savedX, a.savedY
	a.mu.Unlock()

	// 尺寸钳制到球所在屏幕工作区内(含边距):高度超出由前端内容区滚动;
	// 宽度超出(单渠道凭证很多时前端会请求超宽面板)则收缩,避免窗口远超屏宽
	if _, _, ww, wh, dpi, ok := workAreaForPoint(x, y); ok && dpi > 0 {
		maxW := (ww - 2*screenMargin*dpi/96) * 96 / dpi
		maxH := (wh - 2*screenMargin*dpi/96) * 96 / dpi
		if w > maxW {
			w = maxW
		}
		if h > maxH {
			h = maxH
		}
	}

	if nx, ny, ok := fitToScreen(a.ctx, x, y, w, h); ok {
		x, y = nx, ny
	}
	wailsruntime.WindowSetSize(a.ctx, w, h)
	wailsruntime.WindowSetPosition(a.ctx, x, y)
}

// CollapseWindow 收起为悬浮球,并恢复到展开前的位置。
// size 为前端按数据量算出的球窗口边长:展开期间数据可能变化,收起时一次调用
// 内同步更新 ballSize 与窗口尺寸,避免前端两次 IPC(SetBallSize + CollapseWindow)
// 在 Go 侧并发执行导致尺寸/位置互相覆盖。
func (a *App) CollapseWindow(size int) {
	if size < ballSizeMin {
		size = ballSizeMin
	}
	a.mu.Lock()
	a.expanded = false
	x, y := a.savedX, a.savedY
	a.mu.Unlock()

	if size != ballSize {
		ballSize = size
		wailsruntime.WindowSetMinSize(a.ctx, ballSize, ballSize)
	}
	// 将绝对坐标转为显示器相对坐标,避免 WindowSetPosition 叠加工作区原点
	if nx, ny, ok := fitToScreen(a.ctx, x, y, ballSize, ballSize); ok {
		x, y = nx, ny
	}
	wailsruntime.WindowSetSize(a.ctx, ballSize, ballSize)
	wailsruntime.WindowSetPosition(a.ctx, x, y)
}

// fitToScreen 计算让 w×h(逻辑像素)窗口完整落在球所在屏幕内的位置。
// Windows 下 WindowGetPosition 返回物理像素而 Screen.Size 是逻辑像素,
// 直接用 Screen.Size 钳制会在 DPI 缩放 >100% 时失效,因此优先走
// workAreaForPoint 的物理像素精确路径(含任务栏工作区)。
// 返回的坐标为 WindowSetPosition 所需的工作区相对坐标。
func fitToScreen(ctx context.Context, ballX, ballY, w, h int) (int, int, bool) {
	// Windows 精确路径:物理像素,含 DPI 与任务栏
	if wx, wy, ww, wh, dpi, ok := workAreaForPoint(ballX, ballY); ok && dpi > 0 {
		pw := w * dpi / 96
		ph := h * dpi / 96
		ballPhys := ballSize * dpi / 96
		margin := screenMargin * dpi / 96
		ax, ay := anchoredPos(ballX, ballY, pw, ph, ballPhys, wx, wy, ww, wh, margin)
		// WindowSetPosition(SetPos)期望相对工作区原点的坐标
		return ax - wx, ay - wy, true
	}

	// 回退路径(非 Windows 或查询失败):Wails Screen 近似,逻辑坐标
	screens, err := wailsruntime.ScreenGetAll(ctx)
	if err != nil || len(screens) == 0 {
		return 0, 0, false
	}

	// 优先取窗口当前所在屏,回退主屏,再回退第一块屏
	cur := screens[0]
	for _, s := range screens {
		if s.IsCurrent {
			cur = s
			break
		}
		if s.IsPrimary {
			cur = s
		}
	}
	sw, sh := cur.Size.Width, cur.Size.Height
	if sw <= 0 || sh <= 0 {
		return 0, 0, false
	}

	// 球心必须在 (0,0,sw,sh) 坐标系内,否则说明多屏偏移,放弃钳制
	cx, cy := ballX+ballSize/2, ballY+ballSize/2
	if cx < 0 || cx > sw || cy < 0 || cy > sh {
		return 0, 0, false
	}

	x, y := anchoredPos(ballX, ballY, w, h, ballSize, 0, 0, sw, sh, screenMargin)
	return x, y, true
}

// anchoredPos 计算展开位置:默认从球的位置向右下展开,放不下则翻转到左上
// (翻转时球边缘对齐面板边缘),最后整体钳制在矩形 (rx,ry,rw,rh) 内并保留边距。
// 所有参数同一坐标系(调用方保证)。
func anchoredPos(ballX, ballY, w, h, ballPhys, rx, ry, rw, rh, margin int) (int, int) {
	x, y := ballX, ballY
	if x+w > rx+rw-margin {
		x = ballX + ballPhys - w // 向右放不下则向左展开,球右缘对齐面板右缘
	}
	if y+h > ry+rh-margin {
		y = ballY + ballPhys - h // 向下放不下则向上展开
	}
	x = max(rx+margin, min(x, rx+rw-w-margin))
	y = max(ry+margin, min(y, ry+rh-h-margin))
	return x, y
}

// credUpdate 是一次抓取产生的新凭证(如 MiMo Cookie 自动换取),抓取完成后写回配置。
type credUpdate struct {
	providerID string
	keyIdx     int
	creds      map[string]string
}

// fetchAll 并发抓取所有已启用的 Provider 的所有凭证组,
// 结果顺序 = 注册表顺序 × 凭证组顺序。
// 抓取中产生的新凭证(如 MiMo Cookie 自动换取)会写回配置并保存。
func (a *App) fetchAll() []fetcher.QuotaResult {
	a.mu.Lock()
	cfg := *a.cfg
	a.mu.Unlock()

	type job struct {
		providerID string
		keyIdx     int
		keyName    string
		creds      map[string]string
	}
	jobs := make([]job, 0)
	for _, p := range cfg.Providers {
		if !p.Enabled {
			continue
		}
		for i, creds := range p.CredKeys() {
			name := ""
			if i < len(p.KeyNames) {
				name = p.KeyNames[i]
			}
			jobs = append(jobs, job{providerID: p.ID, keyIdx: i, keyName: name, creds: creds})
		}
	}

	var (
		updMu   sync.Mutex
		updates []credUpdate
	)

	jobResults := make([][]fetcher.QuotaResult, len(jobs))
	var wg sync.WaitGroup
	for i, j := range jobs {
		def, ok := fetcher.Get(j.providerID)
		if !ok {
			jobResults[i] = []fetcher.QuotaResult{{Platform: j.providerID, Error: "未知平台"}}
			continue
		}
		wg.Add(1)
		go func(i int, j job, def fetcher.ProviderDef) {
			defer wg.Done()
			// 一次抓取可能返回多条结果(如阿里云:余额 + 各资源包类型用量)
			rs := fetcher.BuildAndFetch(def, j.creds)
			for k := range rs {
				r := &rs[k]
				if len(r.UpdatedCreds) > 0 {
					updMu.Lock()
					updates = append(updates, credUpdate{providerID: j.providerID, keyIdx: j.keyIdx, creds: r.UpdatedCreds})
					updMu.Unlock()
				}
				// ID/Abbr/KeyName 仅在抓取器未自行设置时回填
				// (多结果抓取器可为每条结果指定独立分组与缩写,如资源包按类型成组)
				if r.ID == "" {
					r.ID = def.ID
				}
				if r.Abbr == "" {
					r.Abbr = def.Abbr
				}
				r.KeyIndex = j.keyIdx
				if r.KeyName == "" {
					r.KeyName = j.keyName
				} else if j.keyName != "" {
					// 凭证组名 + 子结果名(多账号场景区分账号)
					r.KeyName = j.keyName + " · " + r.KeyName
				}
				if r.Kind == "" {
					r.Kind = fetcher.KindUsage
				}
			}
			jobResults[i] = rs
		}(i, j, def)
	}
	wg.Wait()

	// 按 job 顺序(注册表顺序 × 凭证组顺序)展平
	results := make([]fetcher.QuotaResult, 0, len(jobs))
	for _, rs := range jobResults {
		results = append(results, rs...)
	}

	a.persistCredUpdates(updates)
	a.applyAutoBudget(results)
	return results
}

// applyAutoBudget 余额型凭证组自动更新预算:
// 成功的余额型结果与本凭证组上次余额比较,余额增加视为充值,将该凭证组的预算更新为新余额;
// 同时刷新各组余额基线并落盘;最后用各凭证组的预算重算消耗百分比。
func (a *App) applyAutoBudget(results []fetcher.QuotaResult) {
	a.mu.Lock()
	defer a.mu.Unlock()

	idx := map[string]int{}
	for i := range a.cfg.Providers {
		idx[a.cfg.Providers[i].ID] = i
	}

	changed := false
	for i := range results {
		r := &results[i]
		if r.Kind != fetcher.KindBalance || r.Error != "" {
			continue
		}
		pi, ok := idx[r.ID]
		if !ok {
			continue
		}
		pc := &a.cfg.Providers[pi]
		hasLast := r.KeyIndex < len(pc.LastBalances)
		var last float64
		if hasLast {
			last = pc.LastBalances[r.KeyIndex]
		}
		if fetcher.DetectRecharge(*r, last, hasLast) {
			// 充值 → 该凭证组的预算 = 新余额(仅此组,不影响同渠道其它凭证)
			for len(pc.Budgets) <= r.KeyIndex {
				pc.Budgets = append(pc.Budgets, 0)
			}
			if pc.Budgets[r.KeyIndex] != r.Balance {
				pc.Budgets[r.KeyIndex] = r.Balance
				changed = true
			}
		}
		for len(pc.LastBalances) <= r.KeyIndex {
			pc.LastBalances = append(pc.LastBalances, 0)
		}
		if pc.LastBalances[r.KeyIndex] != r.Balance {
			pc.LastBalances[r.KeyIndex] = r.Balance
			changed = true
		}
	}
	if changed {
		_ = config.Save(a.cfg)
	}

	// 用各凭证组的预算重算展示值(含未发生变化的组,统一走原逻辑)
	for i := range results {
		b := 0.0
		if pi, ok := idx[results[i].ID]; ok {
			if blds := a.cfg.Providers[pi].Budgets; results[i].KeyIndex >= 0 && results[i].KeyIndex < len(blds) {
				b = blds[results[i].KeyIndex]
			}
		}
		fetcher.ApplyBudget(&results[i], b)
	}
}

// persistCredUpdates 把抓取产生的新凭证(如 MiMo Cookie 自动换取结果)写回配置并保存。
func (a *App) persistCredUpdates(updates []credUpdate) {
	if len(updates) == 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	changed := false
	for _, u := range updates {
		for i := range a.cfg.Providers {
			if a.cfg.Providers[i].ID != u.providerID {
				continue
			}
			keys := a.cfg.Providers[i].CredKeys()
			if u.keyIdx < 0 || u.keyIdx >= len(keys) {
				break
			}
			for k, v := range u.creds {
				if keys[u.keyIdx][k] == v {
					continue
				}
				keys[u.keyIdx][k] = v
				changed = true
			}
			break
		}
	}
	if changed {
		_ = config.Save(a.cfg)
	}
}

// startAutoRefresh 后台自动刷新(可通过再次调用重启,保存配置后模式/间隔立即生效)。
// 订阅模式:立即拉取远端状态,之后按载荷预计下次刷新时间 +60 秒动态休眠;
// 其它模式:按刷新间隔周期抓取,间隔每轮重读配置。
func (a *App) startAutoRefresh() {
	a.mu.Lock()
	if a.stopAuto != nil {
		close(a.stopAuto) // 停掉旧 goroutine
	}
	stop := make(chan struct{})
	a.stopAuto = stop
	a.mu.Unlock()

	go func() {
		for {
			a.mu.Lock()
			mode := a.cfg.Sync.Mode
			interval := a.cfg.RefreshIntervalMin
			a.mu.Unlock()
			if interval <= 0 {
				interval = 15
			}

			wait := time.Duration(interval) * time.Minute
			if mode == config.SyncModeSubscribe {
				wait = a.fetchRemoteState() // 返回值即下次等待时长
			}
			select {
			case <-stop:
				return
			case <-time.After(wait):
			}
			if mode != config.SyncModeSubscribe {
				a.Refresh()
			}
		}
	}()
}

func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 4 {
		return "****"
	}
	// 5-8 字符保留前后 2 位,避免多组凭证掩码全为 "****" 导致还原错位
	if len(s) <= 8 {
		return s[:2] + "..." + s[len(s)-2:]
	}
	return s[:4] + "..." + s[len(s)-4:]
}

func openBrowser(url string) {
	switch runtime.GOOS {
	case "windows":
		exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		exec.Command("open", url).Start()
	default:
		exec.Command("xdg-open", url).Start()
	}
}
