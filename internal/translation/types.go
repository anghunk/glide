// Package translation 统一管理翻译渠道、认证配置与请求结果。
package translation

// Config 描述一个可独立启用的翻译渠道；密钥随应用配置保存在本机。
type Config struct {
	Format    string `json:"format,omitempty"`
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Endpoint  string `json:"endpoint"`
	APIKey    string `json:"apiKey"`
	AppID     string `json:"appId"`
	SecretID  string `json:"secretId"`
	SecretKey string `json:"secretKey"`
	Region    string `json:"region"`
	Model     string `json:"model"`
	Enabled   bool   `json:"enabled"`
}

// Request 使用统一语言代码；源语言 auto 表示自动检测，目标语言不能为 auto。
type Request struct {
	Text       string `json:"text"`
	SourceLang string `json:"sourceLang"`
	TargetLang string `json:"targetLang"`
}

// Result 包含译文、渠道标识、识别语言与本次请求耗时。
type Result struct {
	ProviderID   string `json:"providerId"`
	Text         string `json:"text"`
	DetectedLang string `json:"detectedLang"`
	DurationMS   int64  `json:"durationMs"`
}

// Field 描述渠道设置中的一个输入项，Secret 字段应隐藏显示。
type Field struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder"`
	Secret      bool   `json:"secret"`
	Required    bool   `json:"required"`
}

// Definition 描述一个受支持渠道及其设置表单。
type Definition struct {
	Kind        string  `json:"kind"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Fields      []Field `json:"fields"`
	RequiresKey bool    `json:"requiresKey"`
}
