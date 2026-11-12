package translation

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// google 调用无需密钥的网页接口；接口变更、网络限制或验证码均直接返回错误。
func google(ctx context.Context, config Config, input Request) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if utf8.RuneCountInString(input.Text) > 5000 {
		return "", "", errors.New("谷歌内置渠道单次最多翻译 5000 个字符，请分段翻译")
	}
	endpoint, err := serviceURL(config.Endpoint, "https://translate.googleapis.com/translate_a/single", "/translate_a/single")
	if err != nil {
		return "", "", err
	}
	query := endpoint.Query()
	query.Set("client", "gtx")
	query.Set("sl", input.SourceLang)
	query.Set("tl", input.TargetLang)
	query.Set("dt", "t")
	query.Set("q", input.Text)
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", "", err
	}
	body, err := send(request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return "", "", errors.New("谷歌翻译请求超时，请检查网络及系统代理设置后重试")
		}
		return "", "", err
	}
	var response []json.RawMessage
	if err := decode(body, &response); err != nil {
		return "", "", err
	}
	if len(response) == 0 {
		return "", "", errors.New("谷歌内置渠道未返回翻译结果")
	}
	var sentences [][]json.RawMessage
	if err := decode(response[0], &sentences); err != nil {
		return "", "", err
	}
	var translated strings.Builder
	for _, sentence := range sentences {
		if len(sentence) == 0 {
			continue
		}
		var text string
		if json.Unmarshal(sentence[0], &text) == nil {
			translated.WriteString(text)
		}
	}
	var detected string
	if len(response) > 2 {
		_ = json.Unmarshal(response[2], &detected)
	}
	return translated.String(), detected, nil
}

// googleCloud 使用官方 Basic v2 接口，以请求头传递 API Key。
func googleCloud(ctx context.Context, config Config, input Request) (string, string, error) {
	if err := require(" API Key", config.APIKey); err != nil {
		return "", "", err
	}
	endpoint, err := serviceURL(config.Endpoint, "https://translation.googleapis.com/language/translate/v2", "/language/translate/v2")
	if err != nil {
		return "", "", err
	}
	payload := map[string]any{"q": input.Text, "target": input.TargetLang, "format": "text"}
	if input.SourceLang != "auto" {
		payload["source"] = input.SourceLang
	}
	request, err := jsonRequest(ctx, http.MethodPost, endpoint, payload)
	if err != nil {
		return "", "", err
	}
	request.Header.Set("X-Goog-Api-Key", config.APIKey)
	body, err := send(request)
	if err != nil {
		return "", "", err
	}
	var response struct {
		Data struct {
			Translations []struct {
				Text     string `json:"translatedText"`
				Detected string `json:"detectedSourceLanguage"`
			} `json:"translations"`
		} `json:"data"`
	}
	if err := decode(body, &response); err != nil {
		return "", "", err
	}
	if len(response.Data.Translations) == 0 {
		return "", "", errors.New("Google Cloud 未返回翻译结果")
	}
	return html.UnescapeString(response.Data.Translations[0].Text), response.Data.Translations[0].Detected, nil
}
