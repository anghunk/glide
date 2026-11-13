package ocr

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// tencent 使用 TC3-HMAC-SHA256 调用通用或高精度文字识别。
func tencent(ctx context.Context, config Config, image []byte) (string, error) {
	encoded, err := imageBase64(image, 10<<20)
	if err != nil {
		return "", err
	}
	u, err := cloudEndpoint(config.Endpoint, "https://ocr.tencentcloudapi.com")
	if err != nil {
		return "", err
	}
	action := firstNonempty(config.Method, "GeneralBasicOCR")
	body := map[string]any{"ImageBase64": encoded}
	if action == "GeneralAccurateOCR" {
		// 高精度接口不接受 LanguageType，使用 MulOCR 自动识别多语种。
		body["ConfigID"] = "MulOCR"
	} else {
		body["LanguageType"] = firstNonempty(config.Language, "auto")
	}
	req, data, err := jsonRequest(ctx, u, body)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	stamp, date := strconv.FormatInt(now.Unix(), 10), now.Format("2006-01-02")
	req.Header.Set("X-TC-Action", action)
	req.Header.Set("X-TC-Version", "2018-11-19")
	req.Header.Set("X-TC-Timestamp", stamp)
	req.Header.Set("X-TC-Region", firstNonempty(config.Region, "ap-guangzhou"))
	// TC3 要求签名中的 Action 值转为小写。
	headers := "content-type:application/json\nhost:" + u.Host + "\nx-tc-action:" + strings.ToLower(action) + "\n"
	const signed = "content-type;host;x-tc-action"
	canonical := "POST\n/\n\n" + headers + "\n" + signed + "\n" + hashHex(data)
	scope := date + "/ocr/tc3_request"
	key := mac256(mac256(mac256([]byte("TC3"+strings.TrimSpace(config.SecretKey)), date), "ocr"), "tc3_request")
	signature := hex.EncodeToString(mac256(key, "TC3-HMAC-SHA256\n"+stamp+"\n"+scope+"\n"+hashHex([]byte(canonical))))
	req.Header.Set("Authorization", "TC3-HMAC-SHA256 Credential="+strings.TrimSpace(config.SecretID)+"/"+scope+", SignedHeaders="+signed+", Signature="+signature)
	var result struct {
		Response struct {
			TextDetections []struct{ DetectedText string }
			Error          *struct{ Code, Message string }
		}
	}
	if err := sendJSON(req, &result); err != nil {
		return "", err
	}
	if result.Response.Error != nil {
		return "", serviceError(result.Response.Error.Code, result.Response.Error.Message)
	}
	lines := make([]string, 0, len(result.Response.TextDetections))
	for _, item := range result.Response.TextDetections {
		lines = append(lines, item.DetectedText)
	}
	return strings.Join(lines, "\n"), nil
}

// aliyun 使用 ACS3 签名上传二进制图片，解析读光返回的内嵌 JSON 字符串。
func aliyun(ctx context.Context, config Config, image []byte) (string, error) {
	if len(image) > 10<<20 {
		return "", fmt.Errorf("图片不能超过 10 MB")
	}
	u, err := cloudEndpoint(config.Endpoint, "https://ocr-api.cn-hangzhou.aliyuncs.com")
	if err != nil {
		return "", err
	}
	req, err := newRequest(ctx, u, "application/octet-stream", image)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-Acs-Action", "RecognizeGeneral")
	req.Header.Set("X-Acs-Version", "2021-07-07")
	req.Header.Set("X-Acs-Date", time.Now().UTC().Format("2006-01-02T15:04:05Z"))
	req.Header.Set("X-Acs-Signature-Nonce", nonce())
	req.Header.Set("X-Acs-Content-Sha256", hashHex(image))
	headers, signed := canonicalHeaders(req, []string{"content-type", "host", "x-acs-action", "x-acs-content-sha256", "x-acs-date", "x-acs-signature-nonce", "x-acs-version"})
	canonical := "POST\n/\n\n" + headers + "\n" + signed + "\n" + hashHex(image)
	signature := hex.EncodeToString(mac256([]byte(strings.TrimSpace(config.SecretKey)), "ACS3-HMAC-SHA256\n"+hashHex([]byte(canonical))))
	req.Header.Set("Authorization", "ACS3-HMAC-SHA256 Credential="+strings.TrimSpace(config.SecretID)+",SignedHeaders="+signed+",Signature="+signature)
	var result struct {
		Data          json.RawMessage
		Code, Message string
	}
	if err := sendJSON(req, &result); err != nil {
		return "", err
	}
	if result.Code != "" && result.Code != "200" && result.Code != "Success" {
		return "", serviceError(result.Code, result.Message)
	}
	data := result.Data
	var text string
	if json.Unmarshal(data, &text) == nil {
		data = []byte(text)
	}
	var content struct {
		Content string `json:"content"`
		Words   []struct {
			Word string `json:"word"`
		} `json:"prism_wordsInfo"`
	}
	if json.Unmarshal(data, &content) != nil {
		return "", fmt.Errorf("无法解析阿里云识别结果")
	}
	lines := make([]string, 0, len(content.Words))
	for _, word := range content.Words {
		lines = append(lines, word.Word)
	}
	if len(lines) > 0 {
		return strings.Join(lines, "\n"), nil
	}
	return content.Content, nil
}

// volcengine 使用火山引擎 V4 签名及 OCRNormal 的表单协议。
func volcengine(ctx context.Context, config Config, image []byte) (string, error) {
	encoded, err := imageBase64(image, 4<<20)
	if err != nil {
		return "", err
	}
	u, err := cloudEndpoint(config.Endpoint, "https://visual.volcengineapi.com")
	if err != nil {
		return "", err
	}
	u.RawQuery = url.Values{"Action": {"OCRNormal"}, "Version": {"2020-08-26"}}.Encode()
	req, data, err := formRequest(ctx, u, url.Values{"image_base64": {encoded}})
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	stamp, date := now.Format("20060102T150405Z"), now.Format("20060102")
	req.Header.Set("X-Date", stamp)
	req.Header.Set("X-Content-Sha256", hashHex(data))
	headers, signed := canonicalHeaders(req, []string{"content-type", "host", "x-content-sha256", "x-date"})
	canonical := "POST\n/\n" + u.RawQuery + "\n" + headers + "\n" + signed + "\n" + hashHex(data)
	region := firstNonempty(config.Region, "cn-north-1")
	scope := date + "/" + region + "/cv/request"
	key := mac256(mac256(mac256(mac256([]byte(strings.TrimSpace(config.SecretKey)), date), region), "cv"), "request")
	signature := hex.EncodeToString(mac256(key, "HMAC-SHA256\n"+stamp+"\n"+scope+"\n"+hashHex([]byte(canonical))))
	req.Header.Set("Authorization", "HMAC-SHA256 Credential="+strings.TrimSpace(config.SecretID)+"/"+scope+", SignedHeaders="+signed+", Signature="+signature)
	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Lines []string `json:"line_texts"`
		} `json:"data"`
		ResponseMetadata struct {
			Error *struct{ Code, Message string }
		}
	}
	if err := sendJSON(req, &result); err != nil {
		return "", err
	}
	if result.ResponseMetadata.Error != nil {
		return "", serviceError(result.ResponseMetadata.Error.Code, result.ResponseMetadata.Error.Message)
	}
	if result.Code != 10000 {
		return "", serviceError(result.Code, result.Message)
	}
	return strings.Join(result.Data.Lines, "\n"), nil
}

// huawei 使用 AK/SK 直接签名，项目 ID 和区域必须与已开通的 OCR 资源一致。
func huawei(ctx context.Context, config Config, image []byte) (string, error) {
	encoded, err := imageBase64(image, 10<<20)
	if err != nil {
		return "", err
	}
	region := firstNonempty(config.Region, "cn-north-4")
	u, err := cloudEndpoint(config.Endpoint, "https://ocr."+region+".myhuaweicloud.com")
	if err != nil {
		return "", err
	}
	u.Path = "/v2/" + strings.TrimSpace(config.ProjectID) + "/ocr/general-text"
	req, data, err := jsonRequest(ctx, u, map[string]any{"image": encoded, "detect_direction": true})
	if err != nil {
		return "", err
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	req.Header.Set("X-Sdk-Date", stamp)
	headers, signed := canonicalHeaders(req, []string{"content-type", "host", "x-sdk-date"})
	// 华为签名规范要求 URI 尾部补斜杠，实际请求路径保持接口定义。
	canonical := "POST\n" + u.EscapedPath() + "/\n\n" + headers + "\n" + signed + "\n" + hashHex(data)
	signature := hex.EncodeToString(mac256([]byte(strings.TrimSpace(config.SecretKey)), "SDK-HMAC-SHA256\n"+stamp+"\n"+hashHex([]byte(canonical))))
	req.Header.Set("Authorization", "SDK-HMAC-SHA256 Access="+strings.TrimSpace(config.SecretID)+", SignedHeaders="+signed+", Signature="+signature)
	var result struct {
		ErrorCode string `json:"error_code"`
		ErrorMsg  string `json:"error_msg"`
		Result    struct {
			Words []struct {
				Words string `json:"words"`
			} `json:"words_block_list"`
		} `json:"result"`
	}
	if err := sendJSON(req, &result); err != nil {
		return "", err
	}
	if result.ErrorCode != "" {
		return "", serviceError(result.ErrorCode, result.ErrorMsg)
	}
	var lines []string
	for _, line := range result.Result.Words {
		lines = append(lines, line.Words)
	}
	return strings.Join(lines, "\n"), nil
}
