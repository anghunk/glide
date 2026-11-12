package translation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Definitions 返回设置界面可使用的渠道定义，每次调用均返回独立切片。
func Definitions() []Definition {
	return []Definition{
		{Kind: "google", Name: "谷歌翻译 · 内置", Description: "无需密钥，使用非官方网页接口；可用性受网络、限流和接口变更影响。", Fields: []Field{
			{Key: "endpoint", Label: "服务地址", Placeholder: "https://translate.googleapis.com/translate_a/single"},
		}},
		{Kind: "bing", Name: "必应翻译 · 内置", Description: "无需密钥，使用非官方网页接口；验证或限流时可切换官方渠道。", Fields: []Field{
			{Key: "endpoint", Label: "网站地址", Placeholder: "https://www.bing.com"},
		}},
		{Kind: "google-cloud", Name: "Google Cloud Translation", Description: "Google 官方 Basic v2 API，需要启用 Cloud Translation 服务。", RequiresKey: true, Fields: []Field{
			{Key: "apiKey", Label: "API Key", Secret: true, Required: true},
			{Key: "endpoint", Label: "服务地址", Placeholder: "https://translation.googleapis.com/language/translate/v2"},
		}},
		{Kind: "azure", Name: "微软翻译 · Azure", Description: "微软官方 Translator v3 API；区域密钥需同时填写所属区域。", RequiresKey: true, Fields: []Field{
			{Key: "apiKey", Label: "订阅密钥", Secret: true, Required: true},
			{Key: "region", Label: "资源区域", Placeholder: "例如 eastasia，全局资源可留空"},
			{Key: "endpoint", Label: "服务地址", Placeholder: "https://api.cognitive.microsofttranslator.com"},
		}},
		{Kind: "tencent", Name: "腾讯翻译", Description: "腾讯云 TMT TextTranslate API，使用 SecretId / SecretKey 签名。", RequiresKey: true, Fields: []Field{
			{Key: "secretId", Label: "SecretId", Required: true},
			{Key: "secretKey", Label: "SecretKey", Secret: true, Required: true},
			{Key: "region", Label: "区域", Placeholder: "ap-guangzhou"},
			{Key: "endpoint", Label: "服务地址", Placeholder: "https://tmt.tencentcloudapi.com"},
		}},
		{Kind: "baidu", Name: "百度翻译", Description: "百度翻译开放平台通用文本翻译 API，需要 APP ID 与密钥。", RequiresKey: true, Fields: []Field{
			{Key: "appId", Label: "APP ID", Required: true},
			{Key: "secretKey", Label: "密钥", Secret: true, Required: true},
			{Key: "endpoint", Label: "服务地址", Placeholder: "https://fanyi-api.baidu.com/api/trans/vip/translate"},
		}},
		{Kind: "openai", Name: "AI 翻译", Description: "支持 OpenAI、Anthropic 与 Gemini 协议，可获取模型列表。", Fields: []Field{
			{Key: "endpoint", Label: "API 地址", Placeholder: "https://api.openai.com/v1"},
			{Key: "apiKey", Label: "API Key", Placeholder: "无认证的本地服务可留空", Secret: true},
			{Key: "model", Label: "模型名称", Placeholder: "填写服务支持的完整模型名称", Required: true},
		}},
		{Kind: "ollama", Name: "Ollama", Description: "使用本机或自托管 Ollama；模型需预先安装，默认地址为本机 11434 端口。", Fields: []Field{
			{Key: "endpoint", Label: "服务地址", Placeholder: "http://127.0.0.1:11434"},
			{Key: "model", Label: "模型名称", Placeholder: "与 ollama list 中的名称一致", Required: true},
			{Key: "apiKey", Label: "访问密钥", Placeholder: "仅配置了认证的代理服务需要", Secret: true},
		}},
	}
}

// DefaultConfigs 提供首启渠道，仅启用不需要账户的必应内置翻译。
func DefaultConfigs() []Config {
	definitions := Definitions()
	configs := make([]Config, 0, len(definitions))
	for _, definition := range definitions {
		config := Config{ID: definition.Kind, Kind: definition.Kind, Name: definition.Name, Enabled: definition.Kind == "bing"}
		if definition.Kind == "tencent" {
			config.Region = "ap-guangzhou"
		}
		configs = append(configs, config)
	}
	return configs
}

// Translate 调用配置指定的渠道，继承调用方取消信号；空输入与超长文本直接返回错误。
func Translate(ctx context.Context, config Config, request Request) (result Result, err error) {
	started := time.Now()
	defer func() {
		result.ProviderID = config.ID
		result.DurationMS = time.Since(started).Milliseconds()
		if err != nil {
			err = redactError(err, config)
		}
	}()
	if strings.TrimSpace(request.Text) == "" {
		return result, errors.New("请输入或选中需要翻译的文字")
	}
	if !utf8.ValidString(request.Text) {
		return result, errors.New("待翻译文字不是有效的 UTF-8 文本")
	}
	if utf8.RuneCountInString(request.Text) > 20000 {
		return result, errors.New("单次最多翻译 20000 个字符，请分段翻译")
	}
	request.SourceLang = canonicalLanguage(request.SourceLang)
	request.TargetLang = canonicalLanguage(request.TargetLang)
	if request.TargetLang == "auto" {
		return result, errors.New("请选择目标语言")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	var text, detected string
	switch config.Kind {
	case "google":
		text, detected, err = google(ctx, config, request)
	case "google-cloud":
		text, detected, err = googleCloud(ctx, config, request)
	case "bing":
		text, detected, err = bing(ctx, config, request)
	case "azure":
		text, detected, err = azure(ctx, config, request)
	case "tencent":
		text, detected, err = tencent(ctx, config, request)
	case "baidu":
		text, detected, err = baidu(ctx, config, request)
	case "openai":
		text, detected, err = aiTranslate(ctx, config, request)
	case "ollama":
		text, detected, err = ollama(ctx, config, request)
	default:
		err = fmt.Errorf("不支持的翻译渠道：%s", config.Kind)
	}
	if err != nil {
		return result, err
	}
	if strings.TrimSpace(text) == "" {
		return result, errors.New("翻译服务未返回译文")
	}
	result.Text = text
	if detected != "" && detected != "auto" {
		result.DetectedLang = canonicalLanguage(detected)
	} else if request.SourceLang != "auto" {
		result.DetectedLang = request.SourceLang
	}
	return result, nil
}

// canonicalLanguage 将中文变体和历史语言代码统一为应用内部使用的形式。
func canonicalLanguage(value string) string {
	value = strings.TrimSpace(value)
	switch strings.ToLower(value) {
	case "", "auto", "auto-detect":
		return "auto"
	case "zh", "zh-cn", "zh-hans", "zh-sg":
		return "zh-CN"
	case "zh-tw", "zh-hant", "zh-hk", "cht":
		return "zh-TW"
	case "jp":
		return "ja"
	case "kor":
		return "ko"
	case "fra":
		return "fr"
	case "spa":
		return "es"
	case "ara":
		return "ar"
	case "vie":
		return "vi"
	}
	return value
}

// redactError 在向界面传递服务端错误前，移除可能被回显的认证值。
func redactError(err error, config Config) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	message := err.Error()
	for _, secret := range []string{config.APIKey, config.SecretKey, config.SecretID, config.AppID} {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[已隐藏]")
		}
	}
	return errors.New(message)
}
