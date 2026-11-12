package translation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxResponseBytes = 4 << 20

var httpClient = &http.Client{
	Timeout: 120 * time.Second,
	CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("翻译服务重定向过多")
		}
		if len(via) > 0 {
			original := via[0]
			if original.URL.Scheme == "https" && request.URL.Scheme != "https" {
				return errors.New("翻译服务不能重定向到未加密连接")
			}
			if request.URL.Host != original.URL.Host {
				// POST 正文可能含签名和原文，307/308 会原样重发，因此连同认证请求拒绝跨站跳转。
				if original.Method != http.MethodGet && original.Method != http.MethodHead ||
					original.Header.Get("Authorization") != "" ||
					original.Header.Get("Ocp-Apim-Subscription-Key") != "" ||
					original.Header.Get("X-Goog-Api-Key") != "" {
					return errors.New("已阻止携带认证或正文的跨站重定向，请直接配置最终服务地址")
				}
			}
		}
		return nil
	},
}

// serviceURL 解析绝对 HTTP(S) 地址，为只填写域名的配置补充默认 API 路径。
func serviceURL(raw, fallback, defaultPath string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		raw = fallback
	}
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil || parsed.Fragment != "" {
		return nil, errors.New("服务地址必须是有效的 http:// 或 https:// 地址，且不能包含用户名或片段")
	}
	if parsed.Path == "" || parsed.Path == "/" {
		parsed.Path = defaultPath
	}
	return parsed, nil
}

// jsonRequest 构造 JSON 请求，保留调用方的取消与超时信息。
func jsonRequest(ctx context.Context, method string, endpoint *url.URL, value any) (*http.Request, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("无法编码翻译请求：%w", err)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), bytes.NewReader(data))
	if err == nil {
		request.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	return request, err
}

// send 限制响应大小并统一 HTTP 错误，避免把包含原文或密钥的请求 URL 回传界面。
func send(request *http.Request) ([]byte, error) {
	return sendWithClient(request, httpClient)
}

// sendWithClient 支持为网页渠道使用隔离的会话 Cookie。
func sendWithClient(request *http.Request, client *http.Client) ([]byte, error) {
	if request.Header.Get("User-Agent") == "" {
		request.Header.Set("User-Agent", "Glide/0.1 (+desktop translation)")
	}
	request.Header.Set("Accept", "application/json, text/plain, */*")
	response, err := client.Do(request)
	if err != nil {
		if ctxErr := request.Context().Err(); ctxErr != nil {
			return nil, ctxErr
		}
		var timeout interface{ Timeout() bool }
		if errors.As(err, &timeout) && timeout.Timeout() {
			return nil, errors.New("翻译请求超时，请检查网络或稍后重试")
		}
		return nil, errors.New("无法连接翻译服务，请检查网络和服务地址")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		if ctxErr := request.Context().Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, errors.New("读取翻译响应失败")
	}
	if len(body) > maxResponseBytes {
		return nil, errors.New("翻译服务响应过大")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		switch response.StatusCode {
		case http.StatusUnauthorized:
			return nil, errors.New("认证失败（401），请检查渠道密钥")
		case http.StatusForbidden:
			return nil, errors.New("访问被拒绝（403），请检查服务权限；内置渠道可能需要网页验证")
		case http.StatusTooManyRequests:
			return nil, errors.New("请求过于频繁或额度不足（429），请稍后重试或切换渠道")
		default:
			if message := errorMessage(body); message != "" {
				return nil, fmt.Errorf("翻译服务返回 HTTP %d：%s", response.StatusCode, message)
			}
			return nil, fmt.Errorf("翻译服务返回 HTTP %d，请检查渠道配置或稍后重试", response.StatusCode)
		}
	}
	return body, nil
}

// decode 将服务响应解码到明确结构，HTML 验证页不会被当作翻译成功。
func decode(body []byte, output any) error {
	if err := json.Unmarshal(body, output); err != nil {
		return errors.New("翻译服务响应格式异常；内置渠道可能暂时不可用或需要网页验证")
	}
	return nil
}

// errorMessage 只读取常见 JSON 错误字段，不显示服务端返回的 HTML 或完整响应。
func errorMessage(body []byte) string {
	var value struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	if json.Unmarshal(body, &value) != nil {
		return ""
	}
	message := value.Message
	if len(value.Error) > 0 {
		var text string
		if json.Unmarshal(value.Error, &text) == nil {
			message = text
		} else {
			var detail struct {
				Message string `json:"message"`
			}
			if json.Unmarshal(value.Error, &detail) == nil {
				message = detail.Message
			}
		}
	}
	message = strings.Join(strings.Fields(message), " ")
	runes := []rune(message)
	if len(runes) > 240 {
		return string(runes[:240]) + "…"
	}
	return message
}

// require 在发起网络请求之前给出缺失配置的具体提示。
func require(fields ...string) error {
	for index := 0; index+1 < len(fields); index += 2 {
		if strings.TrimSpace(fields[index+1]) == "" {
			return fmt.Errorf("请先在渠道设置中填写%s", fields[index])
		}
	}
	return nil
}
