package translation

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// AIFormat 返回实际协议，兼容未设置格式的旧配置。
func AIFormat(config Config) string {
	if config.Format == "" {
		return "openai"
	}
	return config.Format
}

// aiEndpoint 由基础地址或完整生成接口地址构造所选协议的接口地址。
func aiEndpoint(config Config, operation string) (*url.URL, error) {
	fallback, root := "https://api.openai.com/v1", "/v1"
	switch AIFormat(config) {
	case "openai", "responses":
	case "anthropic":
		fallback = "https://api.anthropic.com/v1"
	case "gemini":
		fallback, root = "https://generativelanguage.googleapis.com/v1beta", "/v1beta"
	default:
		return nil, errors.New("不支持的 AI 上游格式")
	}
	endpoint, err := serviceURL(config.Endpoint, fallback, root)
	if err != nil {
		return nil, err
	}
	path := strings.TrimRight(endpoint.Path, "/")
	for _, suffix := range []string{"/chat/completions", "/responses", "/messages"} {
		path = strings.TrimSuffix(path, suffix)
	}
	if i := strings.Index(path, "/models/"); i >= 0 {
		path = path[:i]
	}
	path = strings.TrimSuffix(path, "/models")
	endpoint.Path = path + "/" + operation
	endpoint.RawPath = ""
	return endpoint, nil
}

// aiAuth 根据协议设置认证，密钥不放入 URL 查询参数。
func aiAuth(request *http.Request, config Config) {
	switch AIFormat(config) {
	case "anthropic":
		request.Header.Set("anthropic-version", "2023-06-01")
		if config.APIKey != "" {
			request.Header.Set("x-api-key", config.APIKey)
		}
	case "gemini":
		if config.APIKey != "" {
			request.Header.Set("x-goog-api-key", config.APIKey)
		}
	default:
		if config.APIKey != "" {
			request.Header.Set("Authorization", "Bearer "+config.APIKey)
		}
	}
}

// ListModels 获取所选上游的模型列表，处理分页并去重；不改变当前模型名称。
func ListModels(ctx context.Context, config Config) (models []string, err error) {
	defer func() {
		if err != nil {
			err = redactError(err, config)
		}
	}()
	endpoint, err := aiEndpoint(config, "models")
	if err != nil {
		return nil, err
	}
	models = []string{}
	seenPages := map[string]bool{}
	for page := 0; page < 100; page++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return nil, errors.New("无法创建模型列表请求")
		}
		aiAuth(request, config)
		body, err := send(request)
		if err != nil {
			return nil, err
		}
		var response struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
			Models []struct {
				Name    string   `json:"name"`
				Methods []string `json:"supportedGenerationMethods"`
			} `json:"models"`
			HasMore       bool   `json:"has_more"`
			LastID        string `json:"last_id"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := decode(body, &response); err != nil {
			return nil, err
		}
		if message := errorMessage(body); message != "" {
			return nil, fmt.Errorf("获取模型失败：%s", message)
		}
		for _, model := range response.Data {
			if model.ID != "" {
				models = append(models, model.ID)
			}
		}
		for _, model := range response.Models {
			if len(model.Methods) > 0 && !slices.Contains(model.Methods, "generateContent") {
				continue
			}
			if model.Name != "" {
				models = append(models, strings.TrimPrefix(model.Name, "models/"))
			}
		}
		cursor, param := "", ""
		if AIFormat(config) == "anthropic" && response.HasMore {
			cursor, param = response.LastID, "after_id"
		}
		if AIFormat(config) == "gemini" {
			cursor, param = response.NextPageToken, "pageToken"
		}
		if cursor == "" {
			slices.Sort(models)
			models = slices.Compact(models)
			if len(models) == 0 {
				return nil, errors.New("服务未返回可用模型，请自定义模型名称")
			}
			return models, nil
		}
		if seenPages[cursor] {
			return nil, errors.New("模型列表分页重复，请自定义模型名称")
		}
		seenPages[cursor] = true
		query := endpoint.Query()
		query.Set(param, cursor)
		endpoint.RawQuery = query.Encode()
	}
	return nil, errors.New("模型列表分页过多，请自定义模型名称")
}

// aiTranslate 按协议构建请求并提取完整文本，不将思考内容视为译文。
func aiTranslate(ctx context.Context, config Config, input Request) (string, string, error) {
	if AIFormat(config) == "openai" {
		return openAI(ctx, config, input)
	}
	if err := require("模型名称", config.Model); err != nil {
		return "", "", err
	}
	messages := translationMessages(input)
	operation := ""
	var payload map[string]any
	switch AIFormat(config) {
	case "responses":
		operation = "responses"
		payload = map[string]any{"model": config.Model, "instructions": messages[0].Content, "input": input.Text, "stream": false, "store": false}
	case "anthropic":
		operation = "messages"
		payload = map[string]any{"model": config.Model, "system": messages[0].Content, "messages": messages[1:], "max_tokens": 8192, "stream": false}
	case "gemini":
		model := strings.TrimPrefix(config.Model, "models/")
		if strings.ContainsAny(model, "/?#") {
			return "", "", errors.New("Gemini 模型名称无效")
		}
		operation = "models/" + model + ":generateContent"
		payload = map[string]any{"systemInstruction": map[string]any{"parts": []any{map[string]string{"text": messages[0].Content}}}, "contents": []any{map[string]any{"role": "user", "parts": []any{map[string]string{"text": input.Text}}}}}
	default:
		return "", "", errors.New("不支持的 AI 上游格式")
	}
	endpoint, err := aiEndpoint(config, operation)
	if err != nil {
		return "", "", err
	}
	request, err := jsonRequest(ctx, http.MethodPost, endpoint, payload)
	if err != nil {
		return "", "", err
	}
	aiAuth(request, config)
	body, err := send(request)
	if err != nil {
		return "", "", err
	}
	text, err := aiResponseText(body, AIFormat(config))
	return text, "", err
}
