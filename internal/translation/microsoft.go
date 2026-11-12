package translation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

var bingTokenPattern = regexp.MustCompile(`params_AbusePreventionHelper\s*=\s*(\[[^\r\n;]+?\])`)
var bingIGPattern = regexp.MustCompile(`\bIG\s*:\s*"([A-Za-z0-9]+)"`)
var bingIIDPattern = regexp.MustCompile(`data-iid=["']([^"']+)["']`)

// microsoftResponse 是必应网页接口与 Azure Translator 共用的响应片段。
type microsoftResponse struct {
	Detected struct {
		Language string `json:"language"`
	} `json:"detectedLanguage"`
	Translations []struct {
		Text string `json:"text"`
	} `json:"translations"`
}

// microsoftLanguage 将统一语言代码映射为微软的中文脚本代码。
func microsoftLanguage(language string) string {
	switch language {
	case "zh-CN":
		return "zh-Hans"
	case "zh-TW":
		return "zh-Hant"
	case "auto":
		return "auto-detect"
	default:
		return language
	}
}

// bing 读取公开翻译页的会话参数后调用网页翻译接口，不处理验证码或验证挑战。
func bing(ctx context.Context, config Config, input Request) (string, string, error) {
	if utf8.RuneCountInString(input.Text) > 1000 {
		return "", "", errors.New("必应内置渠道单次最多翻译 1000 个字符，请分段翻译或使用微软官方渠道")
	}
	base, err := serviceURL(config.Endpoint, "https://www.bing.com", "/")
	if err != nil {
		return "", "", err
	}
	if base.Path != "/" && base.Path != "" && base.Path != "/translator" {
		return "", "", errors.New("必应内置渠道请填写网站根地址，例如 https://www.bing.com")
	}
	base.Path = "/translator"
	base.RawQuery = ""
	pageRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return "", "", err
	}
	const userAgent = "Mozilla/5.0 (compatible; Glide/0.1)"
	pageRequest.Header.Set("User-Agent", userAgent)
	jar, err := cookiejar.New(nil)
	if err != nil {
		return "", "", err
	}
	client := *httpClient
	client.Jar = jar
	finalPageURL := pageRequest.URL
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if err := httpClient.CheckRedirect(request, via); err != nil {
			return err
		}
		finalPageURL = request.URL
		return nil
	}
	page, err := sendWithClient(pageRequest, &client)
	if err != nil {
		return "", "", err
	}
	match := bingTokenPattern.FindSubmatch(page)
	if len(match) < 2 {
		return "", "", errors.New("必应内置渠道无法取得会话，网页可能需要验证或接口已变更，请尝试微软官方渠道")
	}
	var credentials []json.RawMessage
	if json.Unmarshal(match[1], &credentials) != nil || len(credentials) < 2 {
		return "", "", errors.New("必应内置渠道会话格式已变更")
	}
	key := strings.Trim(string(credentials[0]), "\"")
	var token string
	if json.Unmarshal(credentials[1], &token) != nil || key == "" || token == "" {
		return "", "", errors.New("必应内置渠道未提供有效会话参数")
	}
	ig := ""
	if match := bingIGPattern.FindSubmatch(page); len(match) > 1 {
		ig = string(match[1])
	} else {
		data := make([]byte, 16)
		if _, err := rand.Read(data); err != nil {
			return "", "", errors.New("无法生成必应会话标识")
		}
		ig = strings.ToUpper(hex.EncodeToString(data))
	}
	iid := "translator.5024"
	if match := bingIIDPattern.FindSubmatch(page); len(match) > 1 {
		iid = string(match[1])
	}
	// 必应会按地区重定向网站；向实际提供会话的域名提交，避免 POST 被 301 改成 GET。
	base.Scheme = finalPageURL.Scheme
	base.Host = finalPageURL.Host
	base.Path = "/ttranslatev3"
	base.RawQuery = url.Values{"isVertical": {"1"}, "IG": {ig}, "IID": {iid + ".1"}}.Encode()
	form := url.Values{
		"fromLang": {microsoftLanguage(input.SourceLang)}, "to": {microsoftLanguage(input.TargetLang)},
		"text": {input.Text}, "token": {token}, "key": {key},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("Referer", finalPageURL.String())
	request.Header.Set("Origin", base.Scheme+"://"+base.Host)
	body, err := sendWithClient(request, &client)
	if err != nil {
		return "", "", err
	}
	return parseMicrosoft(body)
}

// azure 调用微软官方 Translator v3 接口，源语言为 auto 时不发送 from 参数。
func azure(ctx context.Context, config Config, input Request) (string, string, error) {
	if err := require("订阅密钥", config.APIKey); err != nil {
		return "", "", err
	}
	endpoint, err := serviceURL(config.Endpoint, "https://api.cognitive.microsofttranslator.com", "/translate")
	if err != nil {
		return "", "", err
	}
	path := strings.TrimRight(endpoint.Path, "/")
	if !strings.HasSuffix(path, "/translate") {
		endpoint.Path = path + "/translate"
	}
	query := endpoint.Query()
	query.Set("api-version", "3.0")
	query.Set("to", microsoftLanguage(input.TargetLang))
	if input.SourceLang != "auto" {
		query.Set("from", microsoftLanguage(input.SourceLang))
	} else {
		query.Del("from")
	}
	endpoint.RawQuery = query.Encode()
	request, err := jsonRequest(ctx, http.MethodPost, endpoint, []map[string]string{{"Text": input.Text}})
	if err != nil {
		return "", "", err
	}
	request.Header.Set("Ocp-Apim-Subscription-Key", config.APIKey)
	if config.Region != "" {
		request.Header.Set("Ocp-Apim-Subscription-Region", config.Region)
	}
	body, err := send(request)
	if err != nil {
		return "", "", err
	}
	return parseMicrosoft(body)
}

// parseMicrosoft 提取译文并处理必应可能以 HTTP 200 返回的业务错误。
func parseMicrosoft(body []byte) (string, string, error) {
	if message := errorMessage(body); message != "" {
		return "", "", fmt.Errorf("微软翻译返回错误：%s", message)
	}
	var response []microsoftResponse
	if err := decode(body, &response); err != nil {
		return "", "", err
	}
	if len(response) == 0 || len(response[0].Translations) == 0 {
		return "", "", errors.New("微软翻译未返回译文；内置渠道可能暂时不可用")
	}
	return response[0].Translations[0].Text, response[0].Detected.Language, nil
}
