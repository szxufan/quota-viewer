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

	// 启动后台定时刷新
	go a.startAutoRefresh()
}

// OnShutdown 在应用退出时清理托盘图标。
func (a *App) OnShutdown(ctx context.Context) {
	if a.tray != nil {
		a.tray.Quit()
	}
}

// Refresh 并发调用三个 fetcher,返回结果并推送事件到前端。
func (a *App) Refresh() []fetcher.QuotaResult {
	results := a.fetchAll()
	a.mu.Lock()
	a.cache = results
	a.mu.Unlock()

	// 推送事件到前端
	wailsruntime.EventsEmit(a.ctx, "quota:update", results)

	return results
}

// ProviderInput 是前端提交的 Provider 状态。
// 凭证字段空字符串 = 不修改(避免掩码回写覆盖真实值)。
type ProviderInput struct {
	ID      string              `json:"id"`
	Enabled bool                `json:"enabled"`
	Creds   map[string]string   `json:"creds"` // 旧前端单凭证(兼容;优先使用 Keys)
	Keys    []map[string]string `json:"keys"`  // 多组凭证(每组一套字段值)
	Budget  float64             `json:"budget"`
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
		keys := make([]map[string]string, 0)
		if ok {
			for _, k := range pc.CredKeys() {
				masked := make(map[string]string, len(k))
				for fk, v := range k {
					masked[fk] = maskSecret(v)
				}
				keys = append(keys, masked)
			}
		}
		fields := make([]map[string]string, 0, len(def.Fields))
		for _, f := range def.Fields {
			fields = append(fields, map[string]string{
				"key": f.Key, "label": f.Label, "type": f.Type,
			})
		}
		providers = append(providers, map[string]interface{}{
			"id":        def.ID,
			"name":      def.DisplayName,
			"abbr":      def.Abbr,
			"kind":      def.Kind,
			"enabled":   ok && pc.Enabled,
			"login_url": def.LoginURL,
			"fields":    fields,
			"creds":     keys,
			"keys":      keys,
			"budget":    pc.Budget,
		})
	}

	return map[string]interface{}{
		"providers":            providers,
		"refresh_interval_min": a.cfg.RefreshIntervalMin,
		"ball_x":               a.cfg.BallX,
		"ball_y":               a.cfg.BallY,
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
		pc.Budget = in.Budget
		// 掩码还原基准必须包含 Creds(旧版单凭证),否则旧数据会被掩码字面量覆盖
		pc.Keys = mergeKeys(pc.CredKeys(), in.Keys, in.Creds, def.Fields)
		pc.Creds = nil // 统一以 Keys 为单一数据源
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
// 默认从球的位置向右下展开,放不下则翻转到左上,高度钳制在工作区内(防止超屏截断),
// 最后整体钳制在屏幕内。首次展开时记录悬浮球位置,供 CollapseWindow 精确恢复。
func (a *App) ExpandWindow(w, h int) {
	a.mu.Lock()
	if !a.expanded {
		a.savedX, a.savedY = wailsruntime.WindowGetPosition(a.ctx)
		a.expanded = true
	}
	x, y := a.savedX, a.savedY
	a.mu.Unlock()

	// 高度钳制到球所在屏幕工作区内(含边距),超出部分由前端内容区滚动
	if _, _, _, wh, dpi, ok := workAreaForPoint(x, y); ok && dpi > 0 {
		maxH := (wh - 2*screenMargin*dpi/96) * 96 / dpi
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
func (a *App) CollapseWindow() {
	a.mu.Lock()
	a.expanded = false
	x, y := a.savedX, a.savedY
	a.mu.Unlock()

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

// fetchAll 并发抓取所有已启用的 Provider 的所有凭证组,
// 结果顺序 = 注册表顺序 × 凭证组顺序。
func (a *App) fetchAll() []fetcher.QuotaResult {
	a.mu.Lock()
	cfg := *a.cfg
	a.mu.Unlock()

	type job struct {
		providerID string
		budget     float64
		keyIdx     int
		creds      map[string]string
	}
	jobs := make([]job, 0)
	for _, p := range cfg.Providers {
		if !p.Enabled {
			continue
		}
		for i, creds := range p.CredKeys() {
			jobs = append(jobs, job{providerID: p.ID, budget: p.Budget, keyIdx: i, creds: creds})
		}
	}

	results := make([]fetcher.QuotaResult, len(jobs))
	var wg sync.WaitGroup
	for i, j := range jobs {
		def, ok := fetcher.Get(j.providerID)
		if !ok {
			results[i] = fetcher.QuotaResult{Platform: j.providerID, Error: "未知平台"}
			continue
		}
		wg.Add(1)
		go func(i int, j job, def fetcher.ProviderDef) {
			defer wg.Done()
			r := def.Build(j.creds).Fetch()
			r.ID = def.ID
			r.Abbr = def.Abbr
			r.KeyIndex = j.keyIdx
			if r.Kind == "" {
				r.Kind = fetcher.KindUsage
			}
			fetcher.ApplyBudget(&r, j.budget)
			results[i] = r
		}(i, j, def)
	}
	wg.Wait()

	return results
}

// startAutoRefresh 定时后台刷新。
func (a *App) startAutoRefresh() {
	for {
		interval := 15
		a.mu.Lock()
		if a.cfg.RefreshIntervalMin > 0 {
			interval = a.cfg.RefreshIntervalMin
		}
		a.mu.Unlock()

		time.Sleep(time.Duration(interval) * time.Minute)
		a.Refresh()
	}
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
