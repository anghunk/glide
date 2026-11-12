package translation

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// tencent 调用腾讯云经典 TextTranslate 接口，使用 TC3-HMAC-SHA256 对请求签名。
func tencent(ctx context.Context, config Config, input Request) (string, string, error) {
	if err := require(" SecretId", config.SecretID, " SecretKey", config.SecretKey); err != nil {
		return "", "", err
	}
	if utf8.RuneCountInString(input.Text) >= 6000 {
		return "", "", errors.New("腾讯翻译单次文本需少于 6000 个字符，请分段翻译")
	}
	endpoint, err := serviceURL(config.Endpoint, "https://tmt.tencentcloudapi.com", "/")
	if err != nil {
		return "", "", err
	}
	if endpoint.Path != "/" || endpoint.RawQuery != "" {
		return "", "", errors.New("腾讯翻译服务地址请填写域名根地址，不包含路径或查询参数")
	}
	source, target := input.SourceLang, input.TargetLang
	if source == "zh-CN" {
		source = "zh"
	}
	if target == "zh-CN" {
		target = "zh"
	}
	payload, err := json.Marshal(map[string]any{
		"SourceText": input.Text, "Source": source, "Target": target, "ProjectId": 0,
	})
	if err != nil {
		return "", "", err
	}
	now := time.Now().UTC()
	timestamp := strconv.FormatInt(now.Unix(), 10)
	date := now.Format("2006-01-02")
	const contentType = "application/json; charset=utf-8"
	const signedHeaders = "content-type;host;x-tc-action"
	canonicalHeaders := "content-type:" + contentType + "\nhost:" + strings.ToLower(endpoint.Host) + "\nx-tc-action:texttranslate\n"
	canonicalRequest := "POST\n/\n\n" + canonicalHeaders + "\n" + signedHeaders + "\n" + sha256Hex(payload)
	credentialScope := date + "/tmt/tc3_request"
	stringToSign := "TC3-HMAC-SHA256\n" + timestamp + "\n" + credentialScope + "\n" + sha256Hex([]byte(canonicalRequest))
	dateKey := hmacSHA256([]byte("TC3"+config.SecretKey), date)
	serviceKey := hmacSHA256(dateKey, "tmt")
	signingKey := hmacSHA256(serviceKey, "tc3_request")
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), strings.NewReader(string(payload)))
	if err != nil {
		return "", "", err
	}
	region := strings.TrimSpace(config.Region)
	if region == "" {
		region = "ap-guangzhou"
	}
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("X-TC-Action", "TextTranslate")
	request.Header.Set("X-TC-Version", "2018-03-21")
	request.Header.Set("X-TC-Timestamp", timestamp)
	request.Header.Set("X-TC-Region", region)
	request.Header.Set("Authorization", "TC3-HMAC-SHA256 Credential="+config.SecretID+"/"+credentialScope+", SignedHeaders="+signedHeaders+", Signature="+signature)
	body, err := send(request)
	if err != nil {
		return "", "", err
	}
	var response struct {
		Response struct {
			TargetText string `json:"TargetText"`
			Source     string `json:"Source"`
			Error      *struct {
				Code    string `json:"Code"`
				Message string `json:"Message"`
			} `json:"Error"`
		} `json:"Response"`
	}
	if err := decode(body, &response); err != nil {
		return "", "", err
	}
	if response.Response.Error != nil {
		return "", "", fmt.Errorf("腾讯翻译：%s（%s）", response.Response.Error.Message, response.Response.Error.Code)
	}
	return response.Response.TargetText, response.Response.Source, nil
}

// hmacSHA256 计算 TC3 签名过程所需的二进制 HMAC。
func hmacSHA256(key []byte, text string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(text))
	return mac.Sum(nil)
}

// sha256Hex 返回小写十六进制的 SHA-256 摘要。
func sha256Hex(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// baiduLanguage 将通用语言代码转换为百度通用翻译 API 的代码。
func baiduLanguage(language string) string {
	if mapped, found := map[string]string{
		"zh-CN": "zh", "zh-TW": "cht", "ja": "jp", "ko": "kor", "fr": "fra",
		"es": "spa", "ar": "ara", "vi": "vie",
	}[language]; found {
		return mapped
	}
	return language
}

// baidu 使用官方要求的 MD5(appid+原文+salt+密钥) 签名，签名前不编码原文。
func baidu(ctx context.Context, config Config, input Request) (string, string, error) {
	if err := require(" APP ID", config.AppID, "密钥", config.SecretKey); err != nil {
		return "", "", err
	}
	if len(input.Text) > 6000 {
		return "", "", errors.New("百度翻译单次最多支持 6000 字节，请分段翻译")
	}
	endpoint, err := serviceURL(config.Endpoint, "https://fanyi-api.baidu.com/api/trans/vip/translate", "/api/trans/vip/translate")
	if err != nil {
		return "", "", err
	}
	saltBytes := make([]byte, 16)
	if _, err := rand.Read(saltBytes); err != nil {
		return "", "", errors.New("无法生成百度翻译签名随机值")
	}
	salt := hex.EncodeToString(saltBytes)
	// MD5 是百度接口指定的签名协议，不能替换成其他摘要算法。
	signature := md5.Sum([]byte(config.AppID + input.Text + salt + config.SecretKey))
	form := url.Values{
		"q": {input.Text}, "from": {baiduLanguage(input.SourceLang)}, "to": {baiduLanguage(input.TargetLang)},
		"appid": {config.AppID}, "salt": {salt}, "sign": {hex.EncodeToString(signature[:])},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	body, err := send(request)
	if err != nil {
		return "", "", err
	}
	var response struct {
		From        string          `json:"from"`
		ErrorCode   json.RawMessage `json:"error_code"`
		ErrorMsg    string          `json:"error_msg"`
		TransResult []struct {
			Text string `json:"dst"`
		} `json:"trans_result"`
	}
	if err := decode(body, &response); err != nil {
		return "", "", err
	}
	code := strings.Trim(string(response.ErrorCode), "\"")
	if code != "" && code != "0" && code != "52000" && code != "null" {
		if message, found := map[string]string{
			"52001": "请求超时，请稍后重试", "52003": "APP ID 不正确或服务未开通",
			"54001": "签名错误，请检查 APP ID 与密钥", "54003": "请求频率过高，请稍后重试",
			"54004": "账户余额不足", "54005": "长文本请求频繁，请稍后重试",
			"58000": "客户端 IP 未获授权", "58001": "当前语言方向不受支持", "58002": "服务暂时关闭，请检查账户权限",
		}[code]; found {
			return "", "", fmt.Errorf("百度翻译：%s（%s）", message, code)
		}
		return "", "", fmt.Errorf("百度翻译：%s（%s）", response.ErrorMsg, code)
	}
	var translations []string
	for _, translation := range response.TransResult {
		translations = append(translations, translation.Text)
	}
	return strings.Join(translations, "\n"), response.From, nil
}
