package ocr

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type baiduToken struct {
	value   string
	expires time.Time
}

var baiduTokens = struct {
	sync.Mutex
	entries map[string]baiduToken
}{entries: map[string]baiduToken{}}

// baiduAccessToken 以内存缓存减少令牌请求，缓存键包含服务地址和凭据摘要。
func baiduAccessToken(ctx context.Context, config Config, endpoint *url.URL) (string, error) {
	key := hashHex([]byte(endpoint.Host + "\x00" + strings.TrimSpace(config.APIKey) + "\x00" + strings.TrimSpace(config.SecretKey)))
	baiduTokens.Lock()
	cached := baiduTokens.entries[key]
	baiduTokens.Unlock()
	if time.Now().Before(cached.expires) {
		return cached.value, nil
	}
	u := *endpoint
	u.Path = "/oauth/2.0/token"
	req, _, err := formRequest(ctx, &u, url.Values{"grant_type": {"client_credentials"}, "client_id": {strings.TrimSpace(config.APIKey)}, "client_secret": {strings.TrimSpace(config.SecretKey)}})
	if err != nil {
		return "", err
	}
	var tokenResponse struct {
		AccessToken      string `json:"access_token"`
		ExpiresIn        int    `json:"expires_in"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := sendJSON(req, &tokenResponse); err != nil {
		return "", err
	}
	if tokenResponse.AccessToken == "" {
		return "", serviceError(tokenResponse.Error, tokenResponse.ErrorDescription)
	}
	baiduTokens.Lock()
	if len(baiduTokens.entries) >= 32 {
		clear(baiduTokens.entries)
	}
	baiduTokens.entries[key] = baiduToken{value: tokenResponse.AccessToken, expires: time.Now().Add(time.Duration(max(0, tokenResponse.ExpiresIn-60)) * time.Second)}
	baiduTokens.Unlock()
	return tokenResponse.AccessToken, nil
}

// baidu 使用 OCR 应用的 OAuth 凭据，不复用百度翻译的 APP ID 签名。
func baidu(ctx context.Context, config Config, image []byte) (string, error) {
	encoded, err := imageBase64(image, 4<<20)
	if err != nil {
		return "", err
	}
	u, err := cloudEndpoint(config.Endpoint, "https://aip.baidubce.com")
	if err != nil {
		return "", err
	}
	token, err := baiduAccessToken(ctx, config, u)
	if err != nil {
		return "", err
	}
	u.Path = "/rest/2.0/ocr/v1/" + firstNonempty(config.Method, "general_basic")
	u.RawQuery = url.Values{"access_token": {token}}.Encode()
	req, _, err := formRequest(ctx, u, url.Values{"image": {encoded}, "language_type": {firstNonempty(config.Language, "CHN_ENG")}, "detect_direction": {"true"}})
	if err != nil {
		return "", err
	}
	var result struct {
		ErrorCode int    `json:"error_code"`
		ErrorMsg  string `json:"error_msg"`
		Words     []struct {
			Words string `json:"words"`
		} `json:"words_result"`
	}
	if err := sendJSON(req, &result); err != nil {
		return "", err
	}
	if result.ErrorCode != 0 {
		if result.ErrorCode == 110 || result.ErrorCode == 111 {
			// 失效令牌不保留到下次识别，不自动重复收费请求。
			baiduTokens.Lock()
			clear(baiduTokens.entries)
			baiduTokens.Unlock()
		}
		return "", serviceError(result.ErrorCode, result.ErrorMsg)
	}
	var lines []string
	for _, line := range result.Words {
		lines = append(lines, line.Words)
	}
	return strings.Join(lines, "\n"), nil
}

// xfyun 对请求地址进行 HMAC 签名，并解析 Base64 包装的分页识别结果。
func xfyun(ctx context.Context, config Config, image []byte) (string, error) {
	encoded, err := imageBase64(image, 4<<20)
	if err != nil {
		return "", err
	}
	u, err := cloudEndpoint(config.Endpoint, "https://api.xf-yun.com")
	if err != nil {
		return "", err
	}
	u.Path = "/v1/private/sf8e6aca1"
	date := time.Now().UTC().Format(http.TimeFormat)
	origin := "host: " + u.Host + "\ndate: " + date + "\nPOST " + u.Path + " HTTP/1.1"
	signature := base64.StdEncoding.EncodeToString(mac256([]byte(strings.TrimSpace(config.SecretKey)), origin))
	authorization := fmt.Sprintf(`api_key="%s", algorithm="hmac-sha256", headers="host date request-line", signature="%s"`, strings.TrimSpace(config.APIKey), signature)
	u.RawQuery = url.Values{"authorization": {base64.StdEncoding.EncodeToString([]byte(authorization))}, "host": {u.Host}, "date": {date}}.Encode()
	encoding := "png"
	if len(image) > 2 && image[0] == 0xff && image[1] == 0xd8 {
		encoding = "jpg"
	}
	req, _, err := jsonRequest(ctx, u, map[string]any{
		"header":    map[string]any{"app_id": strings.TrimSpace(config.AppID), "status": 3},
		"parameter": map[string]any{"sf8e6aca1": map[string]any{"category": "ch_en_public_cloud", "result": map[string]string{"encoding": "utf8", "compress": "raw", "format": "json"}}},
		"payload":   map[string]any{"sf8e6aca1_data_1": map[string]any{"encoding": encoding, "status": 3, "image": encoded}},
	})
	if err != nil {
		return "", err
	}
	var response struct {
		Header struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"header"`
		Payload struct {
			Result struct {
				Text string `json:"text"`
			} `json:"result"`
		} `json:"payload"`
	}
	if err := sendJSON(req, &response); err != nil {
		return "", err
	}
	if response.Header.Code != 0 {
		return "", serviceError(response.Header.Code, response.Header.Message)
	}
	data, err := base64.StdEncoding.DecodeString(response.Payload.Result.Text)
	if err != nil {
		return "", fmt.Errorf("讯飞识别结果不是有效的 Base64")
	}
	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Pages   []struct {
			Exception int `json:"exception"`
			Lines     []struct {
				Exception int `json:"exception"`
				Words     []struct {
					Content string `json:"content"`
				} `json:"words"`
			} `json:"lines"`
		} `json:"pages"`
	}
	if json.Unmarshal(data, &result) != nil {
		return "", fmt.Errorf("无法解析讯飞识别结果")
	}
	if result.Code != 0 {
		return "", serviceError(result.Code, result.Message)
	}
	var lines []string
	for _, page := range result.Pages {
		if page.Exception != 0 {
			return "", serviceError(page.Exception, "讯飞页面识别失败")
		}
		for _, line := range page.Lines {
			if line.Exception != 0 {
				return "", serviceError(line.Exception, "讯飞文本行识别失败")
			}
			var text strings.Builder
			for _, word := range line.Words {
				text.WriteString(word.Content)
			}
			lines = append(lines, text.String())
		}
	}
	return strings.Join(lines, "\n"), nil
}

// youdao 使用 v3 签名；签名输入取图片 Base64 首尾各十字符及完整长度。
func youdao(ctx context.Context, config Config, image []byte) (string, error) {
	encoded, err := imageBase64(image, 10<<20)
	if err != nil {
		return "", err
	}
	u, err := cloudEndpoint(config.Endpoint, "https://openapi.youdao.com")
	if err != nil {
		return "", err
	}
	u.Path = "/ocrapi"
	salt, stamp := nonce(), strconv.FormatInt(time.Now().Unix(), 10)
	input := encoded
	if len(encoded) > 20 {
		input = encoded[:10] + strconv.Itoa(len(encoded)) + encoded[len(encoded)-10:]
	}
	appID := strings.TrimSpace(config.AppID)
	sign := hashHex([]byte(appID + input + salt + stamp + strings.TrimSpace(config.SecretKey)))
	req, _, err := formRequest(ctx, u, url.Values{
		"img": {encoded}, "langType": {firstNonempty(config.Language, "zh-CHS")}, "detectType": {"10012"}, "imageType": {"1"}, "appKey": {appID},
		"salt": {salt}, "sign": {sign}, "docType": {"json"}, "signType": {"v3"}, "curtime": {stamp}, "angle": {"1"},
	})
	if err != nil {
		return "", err
	}
	var result struct {
		ErrorCode json.RawMessage `json:"errorCode"`
		Result    struct {
			Regions []struct {
				Lines []struct {
					Text string `json:"text"`
				} `json:"lines"`
			} `json:"regions"`
		} `json:"Result"`
	}
	if err := sendJSON(req, &result); err != nil {
		return "", err
	}
	code := strings.Trim(string(result.ErrorCode), `"`)
	if code != "0" {
		return "", serviceError(code, "有道 OCR 调用失败，请检查应用绑定、识别语言和额度")
	}
	var lines []string
	for _, region := range result.Result.Regions {
		for _, line := range region.Lines {
			lines = append(lines, line.Text)
		}
	}
	return strings.Join(lines, "\n"), nil
}
