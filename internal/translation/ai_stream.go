package translation

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// aiStream 累计可见译文，按协议识别完成标记，避免把中断或思考内容当作成功结果。
type aiStream struct {
	format   string
	text     strings.Builder
	progress func(string)
	done     bool
	stopped  bool
}

// streamAI 逐事件读取 SSE 或 Ollama NDJSON，正文总量沿用翻译响应大小限制。
// 返回错误时保留已生成的译文，供界面展示中断前的内容。
func streamAI(request *http.Request, format string, progress func(string)) (string, error) {
	request.Header.Set("Accept", "text/event-stream, application/json")
	if format == "ollama" {
		request.Header.Set("Accept", "application/x-ndjson, application/json")
	}
	response, err := receiveWithClient(request, httpClient)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	// 部分兼容服务忽略 stream 参数并返回普通 JSON，仍可读取完整译文。
	if format != "ollama" && strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "application/json") {
		body, err := readResponse(request, response)
		if err != nil {
			return "", err
		}
		text, err := aiResponseText(body, format)
		if err == nil && text != "" {
			progress(text)
		}
		return text, err
	}
	stream := aiStream{format: format, progress: progress}
	limited := &io.LimitedReader{R: response.Body, N: maxResponseBytes + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), maxResponseBytes+1)
	var data []string
	for scanner.Scan() {
		if limited.N == 0 {
			return stream.text.String(), errors.New("翻译服务响应过大")
		}
		line := scanner.Text()
		if format == "ollama" {
			if strings.TrimSpace(line) == "" {
				continue
			}
			err = stream.consume([]byte(line))
		} else if line == "" {
			if len(data) == 0 {
				continue
			}
			err = stream.consume([]byte(strings.Join(data, "\n")))
			data = nil
		} else if value, ok := strings.CutPrefix(line, "data:"); ok {
			data = append(data, strings.TrimPrefix(value, " "))
		}
		if err != nil {
			return stream.text.String(), err
		}
		if stream.done {
			return strings.TrimSpace(stream.text.String()), nil
		}
	}
	if ctxErr := request.Context().Err(); ctxErr != nil {
		return stream.text.String(), ctxErr
	}
	if scanner.Err() != nil {
		return stream.text.String(), errors.New("读取流式翻译响应失败，请重试")
	}
	// 兼容末尾省略空行的 SSE 服务。
	if len(data) > 0 {
		if err := stream.consume([]byte(strings.Join(data, "\n"))); err != nil {
			return stream.text.String(), err
		}
	}
	if !stream.done {
		return stream.text.String(), errors.New("AI 流式响应中断，译文尚未完成，请重试")
	}
	return strings.TrimSpace(stream.text.String()), nil
}

// append 将单个可见文字片段追加到累计译文，并同步通知调用方。
func (s *aiStream) append(text string) {
	if text != "" {
		s.text.WriteString(text)
		s.progress(s.text.String())
	}
}

// consume 解析一个完整事件，仅接收首个候选的正文，拒绝截断、拒答和工具调用结尾。
func (s *aiStream) consume(data []byte) error {
	incomplete := errors.New("AI 响应未完成或被拒绝，请缩小输入范围或检查模型")
	if strings.TrimSpace(string(data)) == "[DONE]" {
		if !s.stopped {
			return incomplete
		}
		s.done = true
		return nil
	}
	var event struct {
		Type         string          `json:"type"`
		Error        json.RawMessage `json:"error"`
		Delta        json.RawMessage `json:"delta"`
		Response     json.RawMessage `json:"response"`
		ContentBlock struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content_block"`
	}
	if err := decode(data, &event); err != nil {
		return err
	}
	if len(event.Error) > 0 && string(event.Error) != "null" || event.Type == "error" {
		message := errorMessage(data)
		if message == "" {
			message = "流式请求失败，请检查服务状态"
		}
		return fmt.Errorf("AI 服务返回错误：%s", message)
	}
	switch s.format {
	case "openai":
		var chunk struct {
			Choices []struct {
				Index int `json:"index"`
				Delta struct {
					Content string `json:"content"`
					Refusal string `json:"refusal"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := decode(data, &chunk); err != nil {
			return err
		}
		for _, choice := range chunk.Choices {
			if choice.Index != 0 {
				continue
			}
			if choice.Delta.Refusal != "" || choice.FinishReason != "" && choice.FinishReason != "stop" {
				return incomplete
			}
			s.append(choice.Delta.Content)
			if choice.FinishReason == "stop" {
				s.stopped, s.done = true, true
			}
		}
	case "responses":
		switch event.Type {
		case "response.output_text.delta":
			var text string
			if err := decode(event.Delta, &text); err != nil {
				return err
			}
			s.append(text)
		case "response.completed":
			text, err := aiResponseText(event.Response, "responses")
			if err != nil {
				return err
			}
			if text != strings.TrimSpace(s.text.String()) {
				s.text.Reset()
				s.append(text)
			}
			s.done = true
		case "response.failed", "response.incomplete", "response.refusal.delta":
			return incomplete
		}
	case "anthropic":
		var delta struct {
			Type       string `json:"type"`
			Text       string `json:"text"`
			StopReason string `json:"stop_reason"`
		}
		if len(event.Delta) > 0 {
			if err := decode(event.Delta, &delta); err != nil {
				return err
			}
		}
		switch event.Type {
		case "content_block_start":
			if event.ContentBlock.Type == "text" {
				s.append(event.ContentBlock.Text)
			}
		case "content_block_delta":
			if delta.Type == "text_delta" {
				s.append(delta.Text)
			}
		case "message_delta":
			if delta.StopReason != "" {
				if delta.StopReason != "end_turn" && delta.StopReason != "stop_sequence" {
					return incomplete
				}
				s.stopped = true
			}
		case "message_stop":
			if !s.stopped {
				return incomplete
			}
			s.done = true
		}
	case "gemini":
		var chunk struct {
			Candidates []struct {
				Index        int    `json:"index"`
				FinishReason string `json:"finishReason"`
				Content      struct {
					Parts []struct {
						Text    string `json:"text"`
						Thought bool   `json:"thought"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
		}
		if err := decode(data, &chunk); err != nil {
			return err
		}
		for _, candidate := range chunk.Candidates {
			if candidate.Index != 0 {
				continue
			}
			if candidate.FinishReason != "" && candidate.FinishReason != "STOP" {
				return incomplete
			}
			for _, part := range candidate.Content.Parts {
				if !part.Thought {
					s.append(part.Text)
				}
			}
			s.done = candidate.FinishReason == "STOP"
		}
	case "ollama":
		var chunk struct {
			Message    chatMessage `json:"message"`
			Done       bool        `json:"done"`
			DoneReason string      `json:"done_reason"`
		}
		if err := decode(data, &chunk); err != nil {
			return err
		}
		if chunk.Done && chunk.DoneReason != "" && chunk.DoneReason != "stop" {
			return incomplete
		}
		s.append(chunk.Message.Content)
		s.done = chunk.Done
	}
	return nil
}
