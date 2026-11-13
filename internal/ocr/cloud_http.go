package ocr

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"glide/internal/translation"
)

var cloudClient = &http.Client{
	Timeout:   90 * time.Second,
	Transport: translation.NewTransport(),
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("OCR 请求不允许重定向，请配置最终服务地址")
	},
}

// cloudRecognizer 统一读取图片，BMP/TIFF 转为无损 PNG 后交给厂商适配器。
func cloudRecognizer(recognize func(context.Context, Config, []byte) (string, error)) func(context.Context, Config, string) (string, error) {
	return func(ctx context.Context, config Config, path string) (string, error) {
		f, err := os.Open(path)
		if err != nil {
			return "", err
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, (32<<20)+1))
		if err != nil {
			return "", err
		}
		if len(data) > 32<<20 {
			return "", errors.New("图片不能超过 32 MB")
		}
		size, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return "", errors.New("无法读取 OCR 图片")
		}
		if size.Width <= 0 || size.Height <= 0 || int64(size.Width)*int64(size.Height) > 40000000 {
			return "", errors.New("图片尺寸过大，请裁剪后重试（最多 4000 万像素）")
		}
		if format != "png" && format != "jpeg" {
			img, _, err := image.Decode(bytes.NewReader(data))
			if err != nil {
				return "", errors.New("无法转换 OCR 图片")
			}
			var buffer bytes.Buffer
			if err := png.Encode(&boundedImageWriter{buffer: &buffer}, img); err != nil {
				return "", err
			}
			data = buffer.Bytes()
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return recognize(ctx, config, data)
	}
}

type boundedImageWriter struct{ buffer *bytes.Buffer }

// Write 限制转码后的内存使用，避免高熵 BMP/TIFF 生成过大的上传文件。
func (w *boundedImageWriter) Write(data []byte) (int, error) {
	if w.buffer.Len()+len(data) > 10<<20 {
		return 0, errors.New("转码后的图片超过 10 MB，请裁剪后重试")
	}
	return w.buffer.Write(data)
}

// imageBase64 按厂商上限检查编码后的大小，绝不将图片放进错误信息。
func imageBase64(data []byte, maxBytes int) (string, error) {
	if base64.StdEncoding.EncodedLen(len(data)) > maxBytes {
		return "", fmt.Errorf("图片 Base64 编码后不能超过 %d MB，请裁剪或压缩后重试", maxBytes>>20)
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

// newRequest 保留取消信息，并按实际正文设置 Content-Type。
func newRequest(ctx context.Context, endpoint *url.URL, contentType string, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err == nil {
		req.Header.Set("Content-Type", contentType)
	}
	return req, err
}

// jsonRequest 对固定结构编码；调用方只传可由 JSON 表达的值。
func jsonRequest(ctx context.Context, endpoint *url.URL, body any) (*http.Request, []byte, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, nil, err
	}
	req, err := newRequest(ctx, endpoint, "application/json", data)
	return req, data, err
}

// formRequest 使用表单转义，确保图片中的加号和斜杠正确传输。
func formRequest(ctx context.Context, endpoint *url.URL, form url.Values) (*http.Request, []byte, error) {
	data := []byte(form.Encode())
	req, err := newRequest(ctx, endpoint, "application/x-www-form-urlencoded", data)
	return req, data, err
}

// sendJSON 统一限制响应体并隐藏含凭据的请求 URL，厂商业务错误由适配器处理。
func sendJSON(req *http.Request, result any) error {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Glide OCR")
	response, err := cloudClient.Do(req)
	if err != nil {
		if req.Context().Err() != nil {
			return req.Context().Err()
		}
		return errors.New("无法连接 OCR 服务，请检查网络、代理和服务地址")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil {
		if req.Context().Err() != nil {
			return req.Context().Err()
		}
		return errors.New("读取 OCR 响应失败")
	}
	if len(body) > 4<<20 {
		return errors.New("OCR 响应超过 4 MB")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var failure struct {
			Message          string `json:"message"`
			ErrorMsg         string `json:"error_msg"`
			ErrorDescription string `json:"error_description"`
			Response         struct{ Error *struct{ Message string } }
			ResponseMetadata struct{ Error *struct{ Message string } }
			Header           struct{ Message string }
		}
		_ = json.Unmarshal(body, &failure)
		message := firstNonempty(failure.Message, failure.ErrorMsg, failure.ErrorDescription, failure.Header.Message)
		if failure.Response.Error != nil {
			message = firstNonempty(message, failure.Response.Error.Message)
		}
		if failure.ResponseMetadata.Error != nil {
			message = firstNonempty(message, failure.ResponseMetadata.Error.Message)
		}
		return fmt.Errorf("HTTP %d：%s", response.StatusCode, firstNonempty(shortMessage(message), "请检查服务开通状态、权限、额度和图片限制"))
	}
	if err := json.Unmarshal(body, result); err != nil {
		return errors.New("OCR 服务响应不是有效 JSON")
	}
	return nil
}

// serviceError 将业务错误限制为简短文本，避免展示整份响应。
func serviceError(code any, message string) error {
	return fmt.Errorf("%v：%s", code, firstNonempty(shortMessage(message), "服务调用失败，请检查凭据、权限和额度"))
}

// shortMessage 合并空白并截断厂商提示，避免长响应撑满错误界面。
func shortMessage(message string) string {
	text := []rune(strings.Join(strings.Fields(message), " "))
	if len(text) > 240 {
		text = append(text[:240], '…')
	}
	return string(text)
}

// firstNonempty 返回首个去除首尾空白后非空的值。
func firstNonempty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// hashHex 返回 SHA-256 摘要的小写十六进制表示。
func hashHex(data []byte) string { digest := sha256.Sum256(data); return hex.EncodeToString(digest[:]) }

// mac256 使用原始字节密钥计算 HMAC-SHA256，供各厂商逐级派生签名。
func mac256(key []byte, text string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(text))
	return mac.Sum(nil)
}

// nonce 生成请求签名所需的随机标识。
func nonce() string { var data [16]byte; _, _ = rand.Read(data[:]); return hex.EncodeToString(data[:]) }

// canonicalHeaders 按协议顺序构造已签名头；Host 从 URL 读取，不依赖 Header 中的特殊键。
func canonicalHeaders(req *http.Request, keys []string) (string, string) {
	sort.Strings(keys)
	var headers strings.Builder
	for _, key := range keys {
		value := req.Header.Get(key)
		if key == "host" {
			value = req.URL.Host
		}
		headers.WriteString(key + ":" + strings.TrimSpace(value) + "\n")
	}
	return headers.String(), strings.Join(keys, ";")
}
