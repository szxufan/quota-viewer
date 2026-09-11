package fetcher

// FieldOption 是 select 型字段的一个可选项(Value 存入配置,Label 展示给用户)。
type FieldOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// CredentialField 描述一个 Provider 的凭证字段(驱动前端动态渲染输入框)。
// Type="select" 时前端按 Options 渲染复选框组(Multiple 固定为多选语义),
// 选中值以逗号拼接存入凭证(如 "ots,flowbag")。
type CredentialField struct {
	Key      string        `json:"key"`
	Label    string        `json:"label"`
	Type     string        `json:"type"`               // "password" | "text" | "textarea" | "select"
	Options  []FieldOption `json:"options,omitempty"`  // Type="select" 时的可选项
	Multiple bool          `json:"multiple,omitempty"` // Type="select" 时是否允许多选
	Plain    bool          `json:"plain,omitempty"`    // 非敏感字段:不掩码,设置界面原样回显
}

// ProviderDef 描述一个可配置的 Provider(注册表条目)。
type ProviderDef struct {
	ID          string
	DisplayName string
	Abbr        string // 球格缩写
	Kind        string // KindUsage | KindBalance
	LoginURL    string // 打开登录页按钮 URL(空 = 不显示按钮)
	// Credentialless 免凭证 Provider(凭证存于外部工具,如百炼 CLI 全局登录态):
	// 启用即可用,前端徽标不按"凭证组非空"判定,fetchAll 为其创建空凭证组任务。
	Credentialless bool
	Fields         []CredentialField
	Build          func(creds map[string]string) Fetcher
}

// registry 是全部已知 Provider 的注册表,顺序固定 = 推荐展示顺序。
var registry = []ProviderDef{
	{
		ID:          "kimi",
		DisplayName: "Kimi",
		Abbr:        "K",
		Kind:        KindUsage,
		Fields: []CredentialField{
			{Key: "api_key", Label: "API Key", Type: "password"},
		},
		Build: func(creds map[string]string) Fetcher {
			return NewKimiFetcher(creds["api_key"])
		},
	},
	{
		ID:          "xfyun",
		DisplayName: "讯飞星辰",
		Abbr:        "讯",
		Kind:        KindUsage,
		LoginURL:    "https://maas.xfyun.cn/packageSubscription",
		Fields: []CredentialField{
			{Key: "cookie", Label: "Cookie(浏览器 F12 复制)", Type: "textarea"},
		},
		Build: func(creds map[string]string) Fetcher {
			return NewXfyunFetcher(creds["cookie"], "")
		},
	},
	{
		ID:          "opencode-go",
		DisplayName: "OpenCode Go",
		Abbr:        "Go",
		Kind:        KindUsage,
		LoginURL:    "https://opencode.ai",
		Fields: []CredentialField{
			{Key: "workspace_id", Label: "Workspace ID", Type: "text"},
			{Key: "session_token", Label: "Session Token", Type: "password"},
		},
		Build: func(creds map[string]string) Fetcher {
			return NewOpenCodeGoFetcher(creds["workspace_id"], creds["session_token"])
		},
	},
	{
		ID:          "mimo",
		DisplayName: "小米 MiMo",
		Abbr:        "M",
		// 支持按量余额(usage 无套餐数据时回退),因此按余额型展示并允许设置预算
		Kind:     KindBalance,
		LoginURL: "https://platform.xiaomimimo.com/console/plan-manage",
		Fields: []CredentialField{
			{Key: "cookie", Label: "Cookie(浏览器 F12 复制)", Type: "textarea"},
			{Key: "xiaomi_cookie", Label: "小米账号 Cookie(可选:失效时自动换取)", Type: "textarea"},
		},
		Build: func(creds map[string]string) Fetcher {
			return NewMiMoFetcher(creds["cookie"], creds["xiaomi_cookie"], "")
		},
	},
	{
		ID:          "deepseek",
		DisplayName: "DeepSeek",
		Abbr:        "D",
		Kind:        KindBalance,
		LoginURL:    "https://platform.deepseek.com",
		Fields: []CredentialField{
			{Key: "api_key", Label: "API Key", Type: "password"},
		},
		Build: func(creds map[string]string) Fetcher {
			return NewDeepSeekFetcher(creds["api_key"])
		},
	},
	{
		ID:          "glm",
		DisplayName: "GLM",
		Abbr:        "GL",
		Kind:        KindUsage,
		LoginURL:    "https://bigmodel.cn/coding-plan/personal/overview",
		Fields: []CredentialField{
			{Key: "token", Label: "Token(authorization 头,F12 复制)", Type: "password"},
			{Key: "organization", Label: "Organization ID(可选)", Type: "text"},
			{Key: "project", Label: "Project ID(可选)", Type: "text"},
		},
		Build: func(creds map[string]string) Fetcher {
			return NewGLMFetcher(creds["token"], creds["organization"], creds["project"])
		},
	},
	{
		ID:          "openrouter",
		DisplayName: "OpenRouter",
		Abbr:        "OR",
		Kind:        KindUsage,
		LoginURL:    "https://openrouter.ai/settings/credits",
		Fields: []CredentialField{
			{Key: "api_key", Label: "API Key", Type: "password"},
		},
		Build: func(creds map[string]string) Fetcher {
			return NewOpenRouterFetcher(creds["api_key"])
		},
	},
	{
		ID:          "aliyun",
		DisplayName: "阿里云",
		Abbr:        "AL",
		Kind:        KindBalance,
		LoginURL:    "https://usercenter2.aliyun.com/home",
		Fields: []CredentialField{
			{Key: "access_key_id", Label: "AccessKey ID", Type: "text"},
			{Key: "access_key_secret", Label: "AccessKey Secret", Type: "password"},
			{
				Key: "package_types", Label: "云资源包用量(可选,多选)", Type: "select",
				Multiple: true, Plain: true,
				Options: []FieldOption{
					{Value: "ots", Label: "OTS 资源包(TableStore)"},
					{Value: "flowbag", Label: "VPC 共享流量包"},
					{Value: "cdt", Label: "CDT 流量包"},
				},
			},
		},
		Build: func(creds map[string]string) Fetcher {
			f := NewAliyunFetcher(creds["access_key_id"], creds["access_key_secret"])
			f.packageTypes = ParseAliyunPackageTypes(creds["package_types"])
			return f
		},
	},
	{
		ID:          "bailian",
		DisplayName: "百炼",
		Abbr:        "BL",
		Kind:        KindUsage,
		LoginURL:    "https://bailian.console.aliyun.com/cn-beijing?tab=plan#/efm/subscription/overview",
		// 凭证由 bl CLI 全局登录管理,启用即可用,无需在应用内配置
		Credentialless: true,
		Fields: []CredentialField{
			{Key: "cli_path", Label: "bl 命令路径(可选,默认 PATH 查找;凭证由 bl CLI 全局登录管理,请勿配置多组)", Type: "text", Plain: true},
		},
		Build: func(creds map[string]string) Fetcher {
			return NewBailianFetcher(creds["cli_path"])
		},
	},
	{
		ID:          "new-api",
		DisplayName: "New API",
		Abbr:        "NA",
		Kind:        KindUsage,
		Fields: []CredentialField{
			{Key: "base_url", Label: "BaseUrl", Type: "text", Plain: true},
			{Key: "channel_id", Label: "channel_id", Type: "text", Plain: true},
			{Key: "user", Label: "User(请求头 New-Api-User)", Type: "text", Plain: true},
			{Key: "authorization", Label: "Authorization(请求头原值)", Type: "password"},
		},
		Build: func(creds map[string]string) Fetcher {
			return NewNewAPIFetcher(creds["base_url"], creds["channel_id"], creds["user"], creds["authorization"])
		},
	},
}

// GetAll 返回全部注册 Provider 的副本(固定顺序)。
func GetAll() []ProviderDef {
	out := make([]ProviderDef, len(registry))
	copy(out, registry)
	return out
}

// Get 按 id 查找 Provider;不存在返回 false。
func Get(id string) (ProviderDef, bool) {
	for _, d := range registry {
		if d.ID == id {
			return d, true
		}
	}
	return ProviderDef{}, false
}
