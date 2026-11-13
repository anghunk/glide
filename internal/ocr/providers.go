package ocr

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"glide/internal/translation"
)

// Config 保存独立的 OCR 服务及凭据；同一时间最多启用一个服务。
type Config struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Icon      string `json:"icon,omitempty"`
	Enabled   bool   `json:"enabled"`
	Endpoint  string `json:"endpoint,omitempty"`
	APIKey    string `json:"apiKey,omitempty"`
	SecretID  string `json:"secretId,omitempty"`
	SecretKey string `json:"secretKey,omitempty"`
	AppID     string `json:"appId,omitempty"`
	ProjectID string `json:"projectId,omitempty"`
	Region    string `json:"region,omitempty"`
	Language  string `json:"language,omitempty"`
	Method    string `json:"method,omitempty"`
	Format    string `json:"format,omitempty"`
	Model     string `json:"model,omitempty"`
}

// Option 将厂商协议参数与界面显示名称分离。
type Option struct{ Value, Label string }

// Field 描述服务表单；密钥默认隐藏，选项字段仅接受已声明的值。
type Field struct {
	Key, Label, Placeholder string
	Secret, Required        bool
	Options                 []Option
}

// Definition 描述已实现的 OCR 类型、配置表单及官方文档。
type Definition struct {
	Kind, Name, Description, DocsURL string
	Fields                           []Field
}

type provider struct {
	definition Definition
	recognize  func(context.Context, Config, string) (string, error)
}

var providers = []provider{
	{definition: Definition{Kind: "system", Name: "系统 OCR"}, recognize: func(ctx context.Context, _ Config, path string) (string, error) { return recognizeNative(ctx, path) }},
	{definition: Definition{Kind: "openai", Name: "AI 识别", Description: "使用支持图片输入的视觉模型提取原文，支持 OpenAI、Anthropic 与 Gemini 协议；模型列表也可能包含不支持图片的模型。", DocsURL: "https://developers.openai.com/api/docs/guides/images-vision", Fields: []Field{
		{Key: "format", Label: "上游格式", Options: []Option{{"openai", "OpenAI Chat Completions"}, {"responses", "OpenAI Responses"}, {"anthropic", "Anthropic Messages"}, {"gemini", "Gemini"}}},
		{Key: "endpoint", Label: "API 地址", Placeholder: "留空使用官方地址，支持自定义 API 地址"},
		{Key: "apiKey", Label: "API Key", Placeholder: "无认证的本地服务可留空", Secret: true},
		{Key: "model", Label: "模型名称", Placeholder: "填写支持图片输入的完整模型名称", Required: true},
	}}, recognize: cloudRecognizer(func(ctx context.Context, config Config, image []byte) (string, error) {
		return translation.RecognizeImage(ctx, config.AIConfig(), image)
	})},
	{definition: Definition{Kind: "tencent", Name: "腾讯云 OCR", Description: "开通腾讯云通用文字识别，使用 SecretId 和 SecretKey；支持标准版与高精度版。", DocsURL: "https://cloud.tencent.com/document/product/866/33526", Fields: []Field{
		{Key: "secretId", Label: "SecretId", Required: true}, {Key: "secretKey", Label: "SecretKey", Secret: true, Required: true},
		{Key: "region", Label: "区域", Placeholder: "ap-guangzhou"}, {Key: "language", Label: "识别语言（标准版）", Placeholder: "auto / zh / jap / kor"},
		{Key: "method", Label: "识别版本", Options: []Option{{"GeneralBasicOCR", "标准版"}, {"GeneralAccurateOCR", "高精度版"}}},
	}}, recognize: cloudRecognizer(tencent)},
	{definition: Definition{Kind: "aliyun", Name: "阿里云 OCR", Description: "开通阿里云读光通用文字识别，使用具有 OCR 权限的 AccessKey。", DocsURL: "https://help.aliyun.com/zh/ocr/developer-reference/api-ocr-api-2021-07-07-recognizegeneral", Fields: []Field{
		{Key: "secretId", Label: "AccessKey ID", Required: true}, {Key: "secretKey", Label: "AccessKey Secret", Secret: true, Required: true},
	}}, recognize: cloudRecognizer(aliyun)},
	{definition: Definition{Kind: "volcengine", Name: "火山引擎 OCR", Description: "开通火山引擎通用文字识别 OCRNormal，使用 Access Key 和 Secret Key。", DocsURL: "https://docs.volcengine.com/docs/large-model-of-image-generation/calling-method?lang=zh", Fields: []Field{
		{Key: "secretId", Label: "Access Key", Required: true}, {Key: "secretKey", Label: "Secret Key", Secret: true, Required: true}, {Key: "region", Label: "区域", Placeholder: "cn-north-1"},
	}}, recognize: cloudRecognizer(volcengine)},
	{definition: Definition{Kind: "baidu", Name: "百度智能云 OCR", Description: "开通百度智能云文字识别应用，填写 OCR 应用的 API Key 和 Secret Key。", DocsURL: "https://ai.baidu.com/ai-doc/OCR/", Fields: []Field{
		{Key: "apiKey", Label: "API Key", Secret: true, Required: true}, {Key: "secretKey", Label: "Secret Key", Secret: true, Required: true},
		{Key: "language", Label: "识别语言", Placeholder: "CHN_ENG / ENG / JAP / KOR"},
		{Key: "method", Label: "识别版本", Options: []Option{{"general_basic", "标准版"}, {"accurate_basic", "高精度版"}}},
	}}, recognize: cloudRecognizer(baidu)},
	{definition: Definition{Kind: "huawei", Name: "华为云 OCR", Description: "开通对应区域的通用文字识别，填写 AK、SK 和该区域的项目 ID。", DocsURL: "https://support.huaweicloud.com/api-ocr/ocr_03_0042.html", Fields: []Field{
		{Key: "secretId", Label: "Access Key", Required: true}, {Key: "secretKey", Label: "Secret Key", Secret: true, Required: true},
		{Key: "projectId", Label: "项目 ID", Required: true}, {Key: "region", Label: "区域", Placeholder: "cn-north-4"},
	}}, recognize: cloudRecognizer(huawei)},
	{definition: Definition{Kind: "xfyun", Name: "科大讯飞 OCR", Description: "开通讯飞通用文字识别，填写应用 APP ID、API Key 和 API Secret，支持中英文。", DocsURL: "https://static.xfyun.cn/doc/words/universal_character_recognition/API.html", Fields: []Field{
		{Key: "appId", Label: "APP ID", Required: true}, {Key: "apiKey", Label: "API Key", Secret: true, Required: true}, {Key: "secretKey", Label: "API Secret", Secret: true, Required: true},
	}}, recognize: cloudRecognizer(xfyun)},
	{definition: Definition{Kind: "youdao", Name: "网易有道 OCR", Description: "开通有道智云通用 OCR，填写应用 ID 和应用密钥；识别语言使用有道语言代码。", DocsURL: "https://ai.youdao.com/DOCSIRMA/html/ocr/api/tyocr/index.html", Fields: []Field{
		{Key: "appId", Label: "应用 ID", Required: true}, {Key: "secretKey", Label: "应用密钥", Secret: true, Required: true}, {Key: "language", Label: "识别语言", Placeholder: "zh-CHS / zh-CHT / en / ja / ko"},
	}}, recognize: cloudRecognizer(youdao)},
}

// Definitions 返回独立的目录快照，不将可变表单切片暴露给调用方。
func Definitions() []Definition {
	definitions := make([]Definition, 0, len(providers))
	for _, p := range providers {
		d := p.definition
		d.Fields = slices.Clone(d.Fields)
		for i := range d.Fields {
			d.Fields[i].Options = slices.Clone(d.Fields[i].Options)
		}
		if d.Kind != "system" && d.Kind != "openai" {
			d.Fields = append(d.Fields, Field{Key: "endpoint", Label: "服务地址", Placeholder: "留空使用官方地址（HTTPS 域名）"})
		}
		definitions = append(definitions, d)
	}
	return definitions
}

// FieldValue 返回表单对应的配置字段；未知字段不会被写入配置。
func (c *Config) FieldValue(key string) *string {
	switch key {
	case "endpoint":
		return &c.Endpoint
	case "apiKey":
		return &c.APIKey
	case "secretId":
		return &c.SecretID
	case "secretKey":
		return &c.SecretKey
	case "appId":
		return &c.AppID
	case "projectId":
		return &c.ProjectID
	case "region":
		return &c.Region
	case "language":
		return &c.Language
	case "method":
		return &c.Method
	case "format":
		return &c.Format
	case "model":
		return &c.Model
	}
	return nil
}

// AIConfig 映射到共用 AI 客户端配置，调用时不复用翻译服务的凭据或模型。
func (c Config) AIConfig() translation.Config {
	return translation.Config{ID: c.ID, Kind: "openai", Name: c.Name, Format: c.Format,
		Endpoint: strings.TrimSpace(c.Endpoint), APIKey: strings.TrimSpace(c.APIKey), Model: strings.TrimSpace(c.Model)}
}

// DefaultConfigs 为首次启动及旧配置保留系统识别；云端服务由用户主动添加。
func DefaultConfigs() []Config {
	return []Config{{ID: "system", Kind: "system", Name: "系统 OCR", Enabled: true}}
}

var resourceID = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// ValidateConfigs 允许暂存未填完的凭据，拒绝无效类型、地址和多个启用项。
func ValidateConfigs(configs []Config) error {
	if len(configs) > 24 {
		return fmt.Errorf("最多配置 24 个 OCR 服务")
	}
	definitions := Definitions()
	ids, enabled := map[string]bool{}, 0
	for _, config := range configs {
		index := slices.IndexFunc(definitions, func(d Definition) bool { return d.Kind == config.Kind })
		if strings.TrimSpace(config.ID) == "" || len(config.ID) > 80 || ids[config.ID] || index < 0 {
			return fmt.Errorf("OCR 服务标识重复或类型无效")
		}
		ids[config.ID] = true
		if config.Enabled {
			enabled++
		}
		if config.Endpoint != "" {
			if config.Kind == "openai" {
				u, err := url.Parse(strings.TrimSpace(config.Endpoint))
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
					return fmt.Errorf("AI 识别地址必须是有效的 HTTP(S) 地址，且不能包含用户名或片段")
				}
			} else if _, err := cloudEndpoint(config.Endpoint, "https://example.com"); err != nil {
				return err
			}
		}
		for _, value := range []string{config.Region, config.ProjectID} {
			if strings.TrimSpace(value) != "" && !resourceID.MatchString(strings.TrimSpace(value)) {
				return fmt.Errorf("OCR 区域或项目 ID 格式无效")
			}
		}
		for _, field := range definitions[index].Fields {
			value := config.FieldValue(field.Key)
			if value != nil && *value != "" && len(field.Options) > 0 && !slices.ContainsFunc(field.Options, func(o Option) bool { return o.Value == *value }) {
				if field.Key == "format" {
					return fmt.Errorf("不支持的 AI 上游格式")
				}
				return fmt.Errorf("OCR 识别版本无效")
			}
		}
	}
	if enabled > 1 {
		return fmt.Errorf("每次只能启用一个 OCR 服务")
	}
	return nil
}

// ActiveConfig 返回当前启用的服务，没有启用项时不隐式回退。
func ActiveConfig(configs []Config) (Config, error) {
	for _, config := range configs {
		if config.Enabled {
			return config, nil
		}
	}
	return Config{}, fmt.Errorf("请在“OCR 服务”中添加并启用服务")
}

// ValidateProvider 在打开文件或截图之前检查凭据，自动保存期间允许配置尚未填完。
func ValidateProvider(config Config) error {
	if err := ValidateConfigs([]Config{config}); err != nil {
		return err
	}
	for _, d := range Definitions() {
		if d.Kind != config.Kind {
			continue
		}
		for _, field := range d.Fields {
			value := config.FieldValue(field.Key)
			if field.Required && value != nil && strings.TrimSpace(*value) == "" {
				return fmt.Errorf("%s：请填写 %s", d.Name, field.Label)
			}
		}
	}
	return nil
}

// recognizeProvider 校验凭据后调用指定实现，并隐藏厂商错误中可能回显的密钥。
func recognizeProvider(ctx context.Context, config Config, path string) (string, error) {
	if err := ValidateProvider(config); err != nil {
		return "", err
	}
	for _, p := range providers {
		if config.Kind != p.definition.Kind {
			continue
		}
		text, err := p.recognize(ctx, config, path)
		if err != nil && ctx.Err() == nil && config.Kind != "system" {
			message := err.Error()
			for _, secret := range []string{config.APIKey, config.SecretKey, config.SecretID, config.AppID} {
				if secret = strings.TrimSpace(secret); secret != "" {
					message = strings.ReplaceAll(message, secret, "***")
				}
			}
			return "", fmt.Errorf("%s：%s", p.definition.Name, message)
		}
		return text, err
	}
	return "", fmt.Errorf("不支持的 OCR 服务类型：%s", config.Kind)
}

// cloudEndpoint 仅接受 HTTPS 域名根地址，协议路径由各厂商适配器固定。
func cloudEndpoint(raw, fallback string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		raw = fallback
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("OCR 服务地址必须为 HTTPS 域名根地址")
	}
	u.Path = "/"
	return u, nil
}
