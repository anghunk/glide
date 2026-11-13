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
		ui.Text(c, tr("上游格式")).FontSize(12).TextColor(p.Muted)
		if localizedSelect(c, &chosen, labels).FillWidth().Height(28).FontSize(12.5).Changed() {
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
	s.aiModelFieldFor(c, p, *config, &config.Model, "services")
}

// aiModelFieldFor 共享模型选择界面，直接绑定实际模型字段，并按页面隔离请求状态。
func (s *settingsUI) aiModelFieldFor(c *ui.Context, p pnPalette, config translation.Config, model *string, category string) {
	key := category + ":" + config.ID
	state := s.modelLists[key]
	if state != nil && !sameModelSource(state.source, config) {
		state = nil
	}
	ui.Column(c.Key("ai-model")).Shrink(0).Gap(6).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, tr("模型名称 *")).Grow(1).FontSize(12).TextColor(p.Muted)
			label := "获取模型"
			if state != nil && state.loading {
				label = "获取中…"
			}
			snapshot := config
			ui.Button(c, tr(label)).Height(26).FontSize(12).Disabled(state != nil && state.loading).OnClick(func() { s.fetchModels(snapshot, category) })
		})
		if state != nil && len(state.models) > 0 {
			chosen := *model
			if state.custom || !slices.Contains(state.models, chosen) {
				chosen = "自定义模型…"
			}
			options := append(slices.Clone(state.models), "自定义模型…")
			if localizedSelect(c, &chosen, options).FillWidth().Height(28).FontSize(12.5).Label(tr("选择模型")).Changed() {
				state.custom = chosen == "自定义模型…"
				if !state.custom {
					*model = chosen
				}
			}
			if state.custom || !slices.Contains(state.models, *model) {
				ui.TextInput(c, model).FillWidth().Height(28).FontSize(12.5).Label(tr("自定义模型名称")).Placeholder(tr("输入完整模型名称"))
			}
		} else {
			ui.TextInput(c, model).FillWidth().Height(28).FontSize(12.5).Label(tr("模型名称")).Placeholder(tr("获取模型列表，或输入自定义模型名称"))
		}
		if state != nil && state.errorText != "" {
			ui.Text(c, tr(state.errorText)).FontSize(12).TextColor(p.Danger).MaxLines(3)
		}
	})
}

// fetchModels 使用点击时的配置快照请求模型，过期或已删除服务的结果不会覆盖当前界面。
func (s *settingsUI) fetchModels(config translation.Config, category string) {
	key := category + ":" + config.ID
	if s.modelLists == nil {
		s.modelLists = make(map[string]*modelListState)
	}
	state := &modelListState{source: config, loading: true}
	s.modelLists[key] = state
	proxySettings := s.app.GetState().Settings
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		ctx = translation.WithProxy(ctx, proxySettings.UseSystemProxy, proxySettings.ProxyAddress)
		models, err := translation.ListModels(ctx, config)
		s.app.mainWindow.Update(func() {
			if s.modelLists[key] != state {
				return
			}
			state.loading = false
			found := false
			if category == "ocr" {
				for _, current := range s.draft.OCRProviders {
					if current.Kind == "openai" && current.ID == config.ID && sameModelSource(current.AIConfig(), config) {
						found = true
						break
					}
				}
			} else {
				for _, current := range s.draft.Providers {
					if current.ID == config.ID && sameModelSource(current, config) {
						found = true
						break
					}
				}
			}
			if !found {
				delete(s.modelLists, key)
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

// aiConnectionTest 在按钮内展示加载动画和结果图标，悬停时可查看耗时或失败原因。
func (s *settingsUI) aiConnectionTest(c *ui.Context, p pnPalette, config *translation.Config) {
	state := s.aiTests[config.ID]
	if state != nil && !sameAITestSource(state.source, *config) {
		state = nil
	}
	ui.Column(c.Key("ai-connection-test")).Shrink(0).Gap(6).Children(func() {
		label, tooltip := tr("测试连通性"), ""
		if state != nil {
			label = tr("测试中…")
			if !state.loading {
				label = fmt.Sprintf(tr("连接成功 · %.1f 秒"), state.duration.Seconds())
				if state.errorText != "" {
					label = tr("连接失败：") + state.errorText
				}
			}
			tooltip = label
		}
		snapshot := *config
		ui.Button(c, "").Height(28).FontSize(12.5).AlignSelf(ui.Start).Label(label).Tooltip(tooltip).
			Disabled(strings.TrimSpace(config.Model) == "" || state != nil && state.loading).
			Children(func() {
				opacity := float32(1)
				if state != nil {
					opacity = 0
				}
				// 保留默认文案的布局宽度，切换图标时按钮和周围内容不会跳动。
				ui.Text(c, tr("测试连通性")).SingleLine().Opacity(opacity)
				if state != nil {
					ui.Box(c).Absolute().Left(0).Top(0).FillWidth().FillHeight().Center().Children(func() {
						if state.loading {
							ui.Spinner(c).Size(14, 14).TextColor(p.Accent)
						} else if state.errorText != "" {
							pnIcon(c, "close", 16, p.Danger)
						} else {
							pnIcon(c, "check", 16, c.Theme().Success)
						}
					})
				}
			}).
			OnClick(func() { s.testAIConnection(snapshot) })
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
	proxySettings := s.app.GetState().Settings
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		ctx = translation.WithProxy(ctx, proxySettings.UseSystemProxy, proxySettings.ProxyAddress)
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
			time.AfterFunc(2*time.Second, func() {
				s.app.mainWindow.Update(func() {
					if s.aiTests[config.ID] == state {
						delete(s.aiTests, config.ID)
					}
				})
			})
		})
	}()
}
