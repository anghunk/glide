package main

import (
	"context"
	"slices"
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
	retries                          map[string]*homeChannelRetry
}

// homeChannelRetry 标记首页单渠道请求，替换整批翻译时取消并丢弃过期结果。
type homeChannelRetry struct {
	cancel context.CancelFunc
}

// newHomeUI 初始化首页，语言沿用当前配置。
func newHomeUI(app *GlideService) *homeUI {
	settings := app.GetState().Settings
	return &homeUI{app: app, source: settings.SourceLang, target: settings.TargetLang, cards: newPanelUI(app)}
}

// view 将输入区和渠道结果放在同一滚动区域，顶部操作栏两端对齐。
func (h *homeUI) view(c *ui.Context, p pnPalette, settings Settings) {
	ui.Scroll(c.Key("home-scroll")).Grow(1).MinHeight(0).FillWidth().Padding(14).Gap(12).Children(func() {
		ui.Column(c).Shrink(0).FillWidth().Gap(10).Children(func() {
			ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Row(c).Shrink(0).Gap(6).AlignItems(ui.Center).Children(func() {
					pnLanguageSelect(c.Key("home-source"), &h.source, true, 115, false)
					pnIcon(c, "chevron-right", 13, p.Faint)
					pnLanguageSelect(c.Key("home-target"), &h.target, false, 110, false)
				})
				ui.Spacer(c)
				ui.Row(c).Shrink(0).Gap(6).AlignItems(ui.Center).Children(func() {
					busy := h.app.ocrRunning()
					ocrLabel := "图片 OCR"
					if busy {
						ocrLabel = "识别中…"
					}
					ui.Button(c, tr(ocrLabel)).Height(26).FontSize(11).Disabled(busy).OnClick(func() { h.app.startOCR(false) })
					ui.Button(c, tr("截图")).Height(26).FontSize(11).Disabled(busy).OnClick(func() { h.app.startOCR(true) })
				})
			})
			ui.TextArea(c.Key("home-input"), &h.input).Height(140).FillWidth().Padding(12).Radius(9).Background(p.Group).Border(.5, p.FieldBorder).FontSize(14).LineHeight(1.6).Label(tr("翻译原文")).Placeholder(tr("输入需要翻译的文字…")).MaxLength(10000).OnTextKey(func(key ui.TextKey) bool {
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
			ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, tr("Enter 翻译 · 最多 10,000 字")).Grow(1).FontSize(11).TextColor(p.Faint)
				ui.PrimaryButton(c, tr(label)).Shrink(0).Height(28).Padding(4, 14).FontSize(12).Disabled(h.loading || strings.TrimSpace(h.input) == "").OnClick(h.submit)
			})
		})
		ui.Box(c).Height(.5).FillWidth().Shrink(0).Background(p.Divider)
		ui.Column(c.Key("home-results")).Shrink(0).FillWidth().Gap(10).Children(func() {
			if h.errorText != "" {
				ui.Text(c, tr(h.errorText)).FontSize(12).TextColor(p.Danger)
			}
			count := 0
			if h.response != nil {
				for _, result := range h.response.Results {
					for _, provider := range settings.Providers {
						if provider.ID == result.ProviderID {
							h.cards.resultCard(c.Key(provider.ID), result, provider, p, h.retryChannel)
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
							ui.Text(c, providerDisplayName(provider.Name)).FontSize(13).FontWeight(600)
						})
						ui.Text(c, tr("输入原文后在此显示译文")).FontSize(12).TextColor(p.Faint)
					})
				}
			}
			if count == 0 {
				ui.Text(c, tr("请在“翻译服务”中添加并启用服务。")).FontSize(12).TextColor(p.Muted)
			}
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
	for _, retry := range h.retries {
		retry.cancel()
	}
	h.retries = nil
	h.loading, h.errorText, h.response = true, "", nil
	req := TranslateRequest{Text: h.input, SourceLang: h.source, TargetLang: h.target}
	go func() {
		response, err := h.app.translate(context.Background(), req, func(progress TranslationResponse) {
			h.app.mainWindow.Update(func() { h.adoptResponse(progress) })
		})
		h.app.mainWindow.Update(func() {
			h.loading = false
			h.adoptResponse(response)
			if err != nil {
				h.errorText = err.Error()
			}
		})
	}()
}

// adoptResponse 合并整批进度，保留同一批次中已重新请求渠道的状态与结果。
func (h *homeUI) adoptResponse(response TranslationResponse) {
	response.Results = slices.Clone(response.Results)
	if h.response != nil && h.response.RequestID == response.RequestID {
		for i, result := range response.Results {
			if h.retries[result.ProviderID] == nil {
				continue
			}
			for _, current := range h.response.Results {
				if current.ProviderID == result.ProviderID {
					response.Results[i] = current
					break
				}
			}
		}
	}
	h.response = &response
}

// retryChannel 仅重译首页当前结果的指定渠道，保留其他渠道与浮窗状态。
// 异步完成后校验批次和请求标记，避免旧结果覆盖新一次翻译。
func (h *homeUI) retryChannel(providerID string) {
	if h.response == nil {
		return
	}
	index := slices.IndexFunc(h.response.Results, func(result ChannelResult) bool { return result.ProviderID == providerID })
	if index < 0 || h.response.Results[index].Pending {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	if h.retries == nil {
		h.retries = make(map[string]*homeChannelRetry)
	}
	if previous := h.retries[providerID]; previous != nil {
		previous.cancel()
	}
	retry := &homeChannelRetry{cancel: cancel}
	h.retries[providerID] = retry
	requestID := h.response.RequestID
	name := h.response.Results[index].ProviderName
	req := TranslateRequest{Text: h.response.Text, SourceLang: h.response.SourceLang, TargetLang: h.response.TargetLang, ProviderIDs: []string{providerID}}
	h.response.Results[index] = ChannelResult{ProviderID: providerID, ProviderName: name, Pending: true}
	go func() {
		defer cancel()
		response, err := h.app.translate(ctx, req, nil)
		h.app.mainWindow.Update(func() {
			if h.response == nil || h.response.RequestID != requestID || h.retries[providerID] != retry {
				return
			}
			result := ChannelResult{ProviderID: providerID, ProviderName: name}
			if len(response.Results) > 0 {
				result = response.Results[0]
			}
			if err != nil {
				result.Error = err.Error()
			}
			h.response.Results[index] = result
		})
	}()
}
