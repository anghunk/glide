package translation

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// deeplLanguage 将通用语言代码转换为 DeepL 代码；中文源语言使用 ZH，目标保留简繁体。
func deeplLanguage(language string, target bool) string {
	switch language {
	case "zh-CN", "zh-TW":
		if !target {
			return "ZH"
		}
		if language == "zh-TW" {
			return "ZH-HANT"
		}
		return "ZH-HANS"
	default:
		return strings.ToUpper(language)
	}
}

// deepl 调用官方文本翻译 API，空地址时根据 Free 密钥后缀选择服务。
func deepl(ctx context.Context, config Config, input Request) (string, string, error) {
	key := strings.TrimSpace(config.APIKey)
	if err := require("DeepL API Key", key); err != nil {
		return "", "", err
	}
	fallback := "https://api.deepl.com/v2/translate"
	if strings.HasSuffix(key, ":fx") {
		fallback = "https://api-free.deepl.com/v2/translate"
	}
	endpoint, err := serviceURL(config.Endpoint, fallback, "/v2/translate")
	if err != nil {
		return "", "", err
	}
	if strings.TrimRight(endpoint.Path, "/") == "/v2" {
		endpoint.Path = "/v2/translate"
	}
	payload := map[string]any{"text": []string{input.Text}, "target_lang": deeplLanguage(input.TargetLang, true)}
	if input.SourceLang != "auto" {
		payload["source_lang"] = deeplLanguage(input.SourceLang, false)
	}
	request, err := jsonRequest(ctx, http.MethodPost, endpoint, payload)
	if err != nil {
		return "", "", err
	}
	request.Header.Set("Authorization", "DeepL-Auth-Key "+key)
	body, err := send(request)
	if err != nil {
		return "", "", err
	}
	var response struct {
		Translations []struct {
			Text     string `json:"text"`
			Detected string `json:"detected_source_language"`
		} `json:"translations"`
	}
	if err := decode(body, &response); err != nil {
		return "", "", err
	}
	if len(response.Translations) != 1 {
		return "", "", errors.New("DeepL 未返回预期的翻译结果")
	}
	return response.Translations[0].Text, strings.ToLower(response.Translations[0].Detected), nil
}

// deeplx 调用自托管 DeepLX 的 /translate 接口，可选令牌使用 Bearer 认证。
func deeplx(ctx context.Context, config Config, input Request) (string, string, error) {
	endpoint, err := serviceURL(config.Endpoint, "http://127.0.0.1:1188/translate", "/translate")
	if err != nil {
		return "", "", err
	}
	payload := map[string]string{
		"text": input.Text, "source_lang": "auto", "target_lang": deeplLanguage(input.TargetLang, true),
	}
	if input.SourceLang != "auto" {
		payload["source_lang"] = deeplLanguage(input.SourceLang, false)
	}
	request, err := jsonRequest(ctx, http.MethodPost, endpoint, payload)
	if err != nil {
		return "", "", err
	}
	if key := strings.TrimSpace(config.APIKey); key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	body, err := send(request)
	if err != nil {
		return "", "", err
	}
	var response struct {
		Code   int    `json:"code"`
		Data   string `json:"data"`
		Source string `json:"source_lang"`
	}
	if err := decode(body, &response); err != nil {
		return "", "", err
	}
	if response.Code != http.StatusOK {
		if message := errorMessage(body); message != "" {
			return "", "", fmt.Errorf("DeepLX：%s（%d）", message, response.Code)
		}
		return "", "", fmt.Errorf("DeepLX 翻译失败（%d），请检查服务状态", response.Code)
	}
	return response.Data, strings.ToLower(response.Source), nil
}
