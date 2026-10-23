// Package probe 提供无界面探针的 JSON 契约与入口逻辑:
// 抓一轮额度 → 归一 → 一行合法 JSON(供 cmd/quotaviewer --json 输出)。
// 抓取编排与 app.go fetchAll 同口径(含 SettleResult 身份归一与逐凭证组预算),
// 但不写回凭证、不更新余额基线(纯只读探测)。
package probe

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"quota-viewer/internal/config"
	"quota-viewer/internal/fetcher"
)

// schemaVersion 是顶层 schema 的固定值。
const schemaVersion = 1

// reportTimeFormat 是 generated_at 的格式(RFC3339 带本地偏移)。
const reportTimeFormat = time.RFC3339

// cstZone 是解释无时区串用的固定 +08:00 时区。
// 中国无夏令时,与 LoadLocation("Asia/Shanghai") 等价,且不依赖 tzdata 文件。
var cstZone = time.FixedZone("CST", 8*3600)

// reportTimeLayout 是无时区上游串的格式(xfyun 的写入形态)。
const reportTimeLayout = "2006-01-02 15:04:05"

// Report 是探针输出的一行 JSON 的顶层结构(成功与失败同形状)。
// Objects 顺序 = Provider 注册表顺序 → 凭证组顺序 → 多结果顺序,与球面一致。
type Report struct {
	Schema       int          `json:"schema"`
	GeneratedAt  string       `json:"generated_at"`
	EnabledCount int          `json:"enabled_count"`
	Objects      []ObjectLine `json:"objects"`
	Error        string       `json:"error"`
}

// ObjectLine 是每个监控对象的记录(一条 = 球面一格 = 一组凭证 × 一张套餐卡/资源包)。
// 数值/字符串字段用指针,否则失败记录只能是 0 / "" 而不是 null。
type ObjectLine struct {
	ID               string   `json:"id"`
	KeyIndex         int      `json:"key_index"`
	KeyName          string   `json:"key_name"`
	Platform         string   `json:"platform"`
	Kind             string   `json:"kind"`
	Percent          *float64 `json:"percent"`
	RemainingPercent *float64 `json:"remaining_percent"`
	Remaining        *string  `json:"remaining"`
	ResetAt          *string  `json:"reset_at"`
	Balance          *float64 `json:"balance"`
	Currency         *string  `json:"currency"`
	Error            string   `json:"error"`
}

// Lookup 按 id 取注册表条目(生产用 fetcher.Get;测试注入夹具)。
type Lookup func(id string) (fetcher.ProviderDef, bool)

// BuildReport 按配置抓一轮额度并构造契约对象。
// get 为 nil 时用 fetcher.Get。
func BuildReport(now time.Time, cfg *config.Config, get Lookup) Report {
	if get == nil {
		get = fetcher.Get
	}
	rep := Report{
		Schema:      schemaVersion,
		GeneratedAt: now.Format(reportTimeFormat),
		Objects:     make([]ObjectLine, 0),
	}

	// 任务展开与 app.go fetchAll 同构:已启用渠道 × 凭证组;
	// 免凭证 Provider(如百炼 CLI)无凭证组时创建空组任务。
	type job struct {
		p      config.ProviderConfig
		creds  map[string]string
		keyIdx int
	}
	var jobs []job
	for _, p := range cfg.Providers {
		if !p.Enabled {
			continue
		}
		credsList := p.CredKeys()
		if len(credsList) == 0 {
			if def, ok := get(p.ID); ok && def.Credentialless {
				credsList = []map[string]string{{}}
			}
		}
		for i, creds := range credsList {
			jobs = append(jobs, job{p: p, creds: creds, keyIdx: i})
		}
	}

	perJob := make([][]ObjectLine, len(jobs))
	var wg sync.WaitGroup
	for i := range jobs {
		wg.Add(1)
		go func(i int, j job) {
			defer wg.Done()
			def, ok := get(j.p.ID)
			if !ok {
				// app.go fetchAll 同口径的未知平台记录。
				perJob[i] = []ObjectLine{{ID: j.p.ID, KeyIndex: j.keyIdx, Kind: fetcher.KindUsage, Error: "未知平台"}}
				return
			}
			perJob[i] = buildLines(j.p, def, j.creds, j.keyIdx)
		}(i, jobs[i])
	}
	wg.Wait()

	for _, ls := range perJob {
		rep.Objects = append(rep.Objects, ls...)
	}
	rep.EnabledCount = len(rep.Objects)
	return rep
}

// buildLines 抓一组凭证,按返回的每条结果(单结果抓取器只有一条)产出一条记录;
// 身份归一共用 fetcher.SettleResult(与 app.go 同口径),再套逐凭证组预算。
func buildLines(p config.ProviderConfig, def fetcher.ProviderDef, creds map[string]string, keyIdx int) []ObjectLine {
	results := fetcher.BuildAndFetch(def, creds)
	lines := make([]ObjectLine, 0, len(results))
	for _, r := range results {
		fetcher.SettleResult(&r, def, keyIdx, keyNameOf(p, keyIdx), keyAbbrOf(p, keyIdx))
		budget := 0.0
		if keyIdx >= 0 && keyIdx < len(p.Budgets) {
			budget = p.Budgets[keyIdx]
		}
		fetcher.ApplyBudget(&r, budget)
		lines = append(lines, lineOf(r))
	}
	return lines
}

func keyNameOf(p config.ProviderConfig, i int) string {
	if i < len(p.KeyNames) {
		return p.KeyNames[i]
	}
	return ""
}

func keyAbbrOf(p config.ProviderConfig, i int) string {
	if i < len(p.KeyAbbrs) {
		return p.KeyAbbrs[i]
	}
	return ""
}

// lineOf 把一条归一后的结果转成契约记录。
func lineOf(r fetcher.QuotaResult) ObjectLine {
	line := ObjectLine{
		ID: r.ID, KeyIndex: r.KeyIndex, KeyName: r.KeyName, Platform: r.Platform,
		Kind: r.Kind, Error: r.Error,
	}
	if r.Error != "" {
		// 失败记录:数值/可空字段全 null(不是 0 / "")。
		return line
	}
	percent := r.Percent
	remaining := 100 - r.Percent
	if remaining < 0 {
		remaining = 0
	}
	line.Percent = &percent
	line.RemainingPercent = &remaining
	if r.Remaining != "" {
		remainingText := r.Remaining
		line.Remaining = &remainingText
	}
	line.ResetAt = normalizeResetAt(r.ResetAt)
	if r.Kind == fetcher.KindBalance {
		balance, currency := r.Balance, r.Currency
		line.Balance = &balance
		line.Currency = &currency
	}
	return line
}

// normalizeResetAt 把上游 ResetAt 归一为 UTC 秒精度 RFC3339;
// 缺失 / 空串 / 无法解析 / 解析出的时刻不在未来(!After(now))一律 nil。
// 无时区串按 +08:00 解释(今天只有 xfyun)。
func normalizeResetAt(raw string) *string {
	if raw == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		parsed, err = time.ParseInLocation(reportTimeLayout, raw, cstZone)
		if err != nil {
			return nil
		}
	}
	if !parsed.After(time.Now()) {
		return nil
	}
	out := parsed.UTC().Format(time.RFC3339)
	return &out
}

// Run 是 CLI 的入口逻辑(可测):
//   - 带 --json 时:一行 JSON 打到 wout 并返回 0——参数失败与配置读取失败都出
//     失败信封(仍输出一行同形状的合法 JSON 并退出 0),失败同时写 error 字段与
//     stderr(诊断旁路);
//   - 不带 --json(无参 / 未知参数):stderr 打 usage、stdout 一个字节都不写,返回 2;
//   - 写 stdout 失败(成功路径与失败信封路径一视同仁)返回 1——空 stdout + exit 0
//     是明令禁止的形状,那是唯一的非 0 例外。
func Run(args []string, wout, werr io.Writer, lookup Lookup) int {
	wantJSON := false
	unknown := ""
	for _, a := range args {
		if a == "--json" || a == "-json" {
			wantJSON = true
			continue
		}
		if unknown == "" {
			unknown = "未知参数: " + a
		}
		// `--json=1` 这类「取值形」本身是参数失败(本开关是裸开关),但它同时声明了
		// 「这次是按 JSON 模式调用」⇒ 下面照旧走失败信封,而不是 usage + 2。
		if strings.HasPrefix(a, "--json=") || strings.HasPrefix(a, "-json=") {
			wantJSON = true
		}
	}
	if !wantJSON {
		msg := "缺少 --json 参数"
		if unknown != "" {
			msg = unknown
		}
		writeUsage(werr, msg)
		return 2
	}

	now := NowLocal()
	cfg, lookupFn, err := loadConfig(lookup)
	if err != nil || unknown != "" {
		msg := unknown
		if err != nil {
			msg = err.Error()
		}
		// 顶层失败的诊断旁路:信封之外再往 stderr 写一行。
		io.WriteString(werr, "quota-viewer-cli: "+msg+"\n")
		if !WriteReport(wout, FailReport(now, msg)) {
			return 1
		}
		return 0
	}

	rep := BuildReport(now, cfg, lookupFn)
	if !WriteReport(wout, rep) {
		return 1
	}
	return 0
}

// loadConfig 读取配置;lookup 非 nil 时跳过配置读取(测试用,避免碰真实配置)。
// 自己先查一次 APPDATA:config.Load() 在 APPDATA 缺失时会吞掉错误返回默认配置,
// 不预检就会输出「成功形状」的空结果。
func loadConfig(lookup Lookup) (*config.Config, Lookup, error) {
	if lookup != nil {
		return config.Default(), lookup, nil
	}
	if os.Getenv("APPDATA") == "" {
		return nil, nil, errors.New("APPDATA 环境变量缺失,无法定位配置目录")
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	return cfg, DefaultLookup, nil
}

// DefaultLookup 是生产用的查表函数。
func DefaultLookup(id string) (fetcher.ProviderDef, bool) {
	return fetcher.Get(id)
}

// EncodeLine 把契约对象序列化成一行 JSON(内部无换行)。
func EncodeLine(rep Report) ([]byte, error) {
	return json.Marshal(rep)
}

// WriteReport 序列化并整块写入 w;写失败返回 false(调用方可据此置退出码)。
func WriteReport(w io.Writer, rep Report) bool {
	data, err := EncodeLine(rep)
	if err != nil {
		return false
	}
	if _, err := w.Write(data); err != nil {
		return false
	}
	return true
}

// FailReport 构造失败信封:objects 是初始化的空切片(序列化成 [] 而非 null),
// 顶层字段齐全。
func FailReport(now time.Time, msg string) Report {
	return Report{
		Schema:      schemaVersion,
		GeneratedAt: now.Format(reportTimeFormat),
		Objects:     make([]ObjectLine, 0),
		Error:       msg,
	}
}

// NowLocal 返回本次抓取时刻(generated_at 用,带本地偏移)。
func NowLocal() time.Time {
	return time.Now()
}

func writeUsage(w io.Writer, msg string) {
	fmt.Fprintln(w, "usage: quota-viewer-cli.exe --json")
	fmt.Fprintln(w, msg)
}
