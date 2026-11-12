package main

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// homeUI 保存首页独立的输入和翻译状态，与划词浮窗互不覆盖。
type homeUI struct {
	app                              *GlideService
	input, source, target, errorText string
	loading                          bool
	response                         *TranslationResponse
	cards                            *panelUI
}

// newHomeUI 初始化首页，语言沿用当前配置。
func newHomeUI(app *GlideService) *homeUI {
	settings := app.GetState().Settings
	return &homeUI{app: app, source: settings.SourceLang, target: settings.TargetLang, cards: newPanelUI(app)}
}

// view 以上下布局展示原文和已启用渠道的逐项翻译结果。
func (h *homeUI) view(c *ui.Context, p pnPalette, settings Settings) {
	ui.Column(c).Fill().Padding(14).Gap(12).Children(func() {
		ui.Column(c).Shrink(0).FillWidth().Gap(10).Children(func() {
			ui.Row(c).Gap(6).Children(func() {
				ui.Text(c, "原文").Grow(1).FontSize(14).FontWeight(600)
				busy := h.app.ocrRunning()
				ocrLabel := "图片 OCR"
				if busy {
					ocrLabel = "识别中…"
				}
				ui.Button(c, ocrLabel).Height(26).FontSize(11).Disabled(busy).OnClick(func() { h.app.startOCR(false) })
				ui.Button(c, "截图").Height(26).FontSize(11).Disabled(busy).OnClick(func() { h.app.startOCR(true) })
			})
			ui.Row(c).Gap(6).Children(func() {
				pnLanguageSelect(c.Key("home-source"), &h.source, true, 115)
				pnIcon(c, "chevron-right", 13, p.Faint)
				pnLanguageSelect(c.Key("home-target"), &h.target, false, 110)
			})
			ui.TextArea(c.Key("home-input"), &h.input).Height(140).FillWidth().Padding(12).Radius(9).Background(p.Group).Border(.5, p.FieldBorder).FontSize(14).LineHeight(1.6).Label("翻译原文").Placeholder("输入需要翻译的文字…").MaxLength(10000).OnTextKey(func(key ui.TextKey) bool {
				if key.Key == ui.KeyEnter && (key.Mods == 0 || key.Mods == ui.Cmd) {
					h.input = key.Text()
					h.submit()
					return true
				}
				return false
			})
			label := "翻译"
			if h.loading {
				label = "翻译中…"
			}
			ui.PrimaryButton(c, label).FillWidth().Height(32).Disabled(h.loading || strings.TrimSpace(h.input) == "").OnClick(h.submit)
			ui.Text(c, "Enter 翻译 · 最多 10,000 字").FontSize(11).TextColor(p.Faint)
		})
		ui.Box(c).Height(.5).FillWidth().Shrink(0).Background(p.Divider)
		ui.Column(c).Grow(1).MinHeight(0).FillWidth().Gap(10).Children(func() {
			ui.Text(c, "翻译对比").FontSize(14).FontWeight(600)
			ui.Scroll(c.Key("home-results")).Grow(1).FillWidth().Gap(10).Children(func() {
				if h.errorText != "" {
					ui.Text(c, h.errorText).FontSize(12).TextColor(p.Danger)
				}
				count := 0
				if h.response != nil {
					for _, result := range h.response.Results {
						for _, provider := range settings.Providers {
							if provider.ID == result.ProviderID {
								h.cards.resultCard(c.Key(provider.ID), result, provider, p)
								count++
								break
							}
						}
					}
				} else {
					for _, provider := range settings.Providers {
						if !provider.Enabled {
							continue
						}
						count++
						ui.Column(c.Key(provider.ID)).Shrink(0).Padding(12).Gap(10).Radius(9).Background(p.Group).Children(func() {
							ui.Row(c).Gap(8).Children(func() {
								pnProviderIcon(c, providerIconKind(provider), 20, p.Text)
								ui.Text(c, provider.Name).FontSize(13).FontWeight(600)
							})
							ui.Text(c, "输入原文后在此显示译文").FontSize(12).TextColor(p.Faint)
						})
					}
				}
				if count == 0 {
					ui.Text(c, "请在“翻译服务”中添加并启用服务。").FontSize(12).TextColor(p.Muted)
				}
			})
		})
	})
	if h.cards.copiedID != "" {
		if remaining := time.Until(h.cards.copiedUntil); remaining > 0 {
			c.After(remaining)
		} else {
			h.cards.copiedID = ""
		}
	}
	if c.Shortcut(ui.Cmd, ui.KeyEnter) {
		h.submit()
	}
}

// submit 后台并行翻译并逐项刷新结果，防止重复提交。
func (h *homeUI) submit() {
	if h.loading || strings.TrimSpace(h.input) == "" {
		return
	}
	h.loading, h.errorText, h.response = true, "", nil
	req := TranslateRequest{Text: h.input, SourceLang: h.source, TargetLang: h.target}
	go func() {
		response, err := h.app.translate(context.Background(), req, func(progress TranslationResponse) {
			h.app.mainWindow.Update(func() { h.response = &progress })
		})
		h.app.mainWindow.Update(func() {
			h.loading = false
			h.response = &response
			if err != nil {
				h.errorText = err.Error()
			}
		})
	}()
}
