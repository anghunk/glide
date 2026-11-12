package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"glide/internal/translation"
)

// modelListState 隔离每个服务的获取状态，仅在连接配置相同时展示结果。
type modelListState struct {
	custom    bool
	source    translation.Config
	loading   bool
	models    []string
	errorText string
}

// sameModelSource 判断模型列表是否仍属于当前地址、密钥及协议。
func sameModelSource(a, b translation.Config) bool {
	return a.Endpoint == b.Endpoint && a.APIKey == b.APIKey && translation.AIFormat(a) == translation.AIFormat(b)
}

// aiFormatField 为原生请求选择上游协议，旧配置默认使用 Chat Completions。
func (s *settingsUI) aiFormatField(c *ui.Context, p pnPalette, config *translation.Config) {
	labels := []string{"OpenAI Chat Completions", "OpenAI Responses", "Anthropic Messages", "Gemini"}
	formats := []string{"openai", "responses", "anthropic", "gemini"}
	chosen := labels[0]
	for i, format := range formats {
		if translation.AIFormat(*config) == format {
			chosen = labels[i]
		}
	}
	ui.Column(c.Key("ai-format")).Shrink(0).Gap(6).Children(func() {
		ui.Text(c, "上游格式").FontSize(12).TextColor(p.Muted)
		if ui.Select(c, &chosen, labels).FillWidth().Height(28).FontSize(12.5).Changed() {
			for i, label := range labels {
				if chosen == label {
					config.Format = formats[i]
				}
			}
		}
	})
}

// aiModelField 保留自由输入，成功获取列表后允许在同一输入框中下拉选择。
func (s *settingsUI) aiModelField(c *ui.Context, p pnPalette, config *translation.Config) {
	state := s.modelLists[config.ID]
	if state != nil && !sameModelSource(state.source, *config) {
		state = nil
	}
	ui.Column(c.Key("ai-model")).Shrink(0).Gap(6).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, "模型名称 *").Grow(1).FontSize(12).TextColor(p.Muted)
			label := "获取模型"
			if state != nil && state.loading {
				label = "获取中…"
			}
			snapshot := *config
			ui.Button(c, label).Height(26).FontSize(12).Disabled(state != nil && state.loading).OnClick(func() { s.fetchModels(snapshot) })
		})
		if state != nil && len(state.models) > 0 {
			chosen := config.Model
			if state.custom || !slices.Contains(state.models, chosen) {
				chosen = "自定义模型…"
			}
			options := append(slices.Clone(state.models), "自定义模型…")
			if ui.Select(c, &chosen, options).FillWidth().Height(28).FontSize(12.5).Label("选择模型").Changed() {
				state.custom = chosen == "自定义模型…"
				if !state.custom {
					config.Model = chosen
				}
			}
			if state.custom || !slices.Contains(state.models, config.Model) {
				ui.TextInput(c, &config.Model).FillWidth().Height(28).FontSize(12.5).Label("自定义模型名称").Placeholder("输入完整模型名称")
			}
		} else {
			ui.TextInput(c, &config.Model).FillWidth().Height(28).FontSize(12.5).Label("模型名称").Placeholder("获取模型列表，或输入自定义模型名称")
		}
		if state != nil && state.errorText != "" {
			ui.Text(c, state.errorText).FontSize(12).TextColor(p.Danger).MaxLines(3)
		}
	})
}

// fetchModels 使用点击时的配置快照请求模型，过期或已删除服务的结果不会覆盖当前界面。
func (s *settingsUI) fetchModels(config translation.Config) {
	if s.modelLists == nil {
		s.modelLists = make(map[string]*modelListState)
	}
	state := &modelListState{source: config, loading: true}
	s.modelLists[config.ID] = state
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		models, err := translation.ListModels(ctx, config)
		s.app.mainWindow.Update(func() {
			if s.modelLists[config.ID] != state {
				return
			}
			state.loading = false
			found := false
			for _, current := range s.draft.Providers {
				if current.ID == config.ID && sameModelSource(current, config) {
					found = true
					break
				}
			}
			if !found {
				delete(s.modelLists, config.ID)
				return
			}
			if err != nil {
				state.errorText = err.Error()
				return
			}
			state.models = models
		})
	}()
}

// aiTestState 保存单个 AI 服务的测试结果，配置改变后不再展示旧结果。
type aiTestState struct {
	source    translation.Config
	loading   bool
	errorText string
	duration  time.Duration
}

// sameAITestSource 确认结果属于相同连接配置及模型，忽略名称和启用状态。
func sameAITestSource(a, b translation.Config) bool {
	return sameModelSource(a, b) && a.Model == b.Model
}

// aiConnectionTest 提供独立测试入口，展示真实模型调用的结果与耗时。
func (s *settingsUI) aiConnectionTest(c *ui.Context, p pnPalette, config *translation.Config) {
	state := s.aiTests[config.ID]
	if state != nil && !sameAITestSource(state.source, *config) {
		state = nil
	}
	ui.Column(c.Key("ai-connection-test")).Shrink(0).Gap(6).Children(func() {
		label := "测试连通性"
		if state != nil && state.loading {
			label = "测试中…"
		}
		snapshot := *config
		ui.Button(c, label).Height(28).FontSize(12.5).AlignSelf(ui.Start).
			Disabled(strings.TrimSpace(config.Model) == "" || state != nil && state.loading).
			OnClick(func() { s.testAIConnection(snapshot) })
		if state == nil || state.loading {
			return
		}
		text, color := fmt.Sprintf("连接成功 · %.1f 秒", state.duration.Seconds()), c.Theme().Success
		if state.errorText != "" {
			text, color = "连接失败："+state.errorText, p.Danger
		}
		ui.Text(c, text).FontSize(12).TextColor(color).MaxLines(3).Tooltip(text)
	})
}

// testAIConnection 使用当前配置执行简短翻译，不写入历史、不改变翻译浮窗。
func (s *settingsUI) testAIConnection(config translation.Config) {
	if s.aiTests == nil {
		s.aiTests = make(map[string]*aiTestState)
	}
	if previous := s.aiTests[config.ID]; previous != nil && previous.loading && sameAITestSource(previous.source, config) {
		return
	}
	state := &aiTestState{source: config, loading: true}
	s.aiTests[config.ID] = state
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		started := time.Now()
		_, err := translation.Translate(ctx, config, translation.Request{Text: "Hello", SourceLang: "en", TargetLang: "zh-CN"})
		duration := time.Since(started)
		s.app.mainWindow.Update(func() {
			if s.aiTests[config.ID] != state {
				return
			}
			found := false
			for _, current := range s.draft.Providers {
				if current.ID == config.ID && sameAITestSource(current, config) {
					found = true
					break
				}
			}
			if !found {
				delete(s.aiTests, config.ID)
				return
			}
			state.loading, state.duration = false, duration
			if err != nil {
				state.errorText = err.Error()
			}
		})
	}()
}
