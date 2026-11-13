package main

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"glide/internal/ocr"
)

// ocrDisplayName 仅本地化内置名称，保留用户为实例设置的名称。
func ocrDisplayName(name string) string {
	for _, definition := range ocr.Definitions() {
		if name == definition.Name {
			return tr(name)
		}
	}
	return name
}

// selectOCRProvider 在配置刷新或删除后保留有效的详情选中项。
func (s *settingsUI) selectOCRProvider() {
	for _, provider := range s.draft.OCRProviders {
		if provider.ID == s.ocrProviderID {
			return
		}
	}
	s.ocrProviderID = ""
	if len(s.draft.OCRProviders) > 0 {
		s.ocrProviderID = s.draft.OCRProviders[0].ID
	}
}

// addOCRProvider 创建独立的识别服务实例，首次添加时自动启用。
func (s *settingsUI) addOCRProvider(definition ocr.Definition) {
	if s.saving {
		return
	}
	if len(s.draft.OCRProviders) >= 24 {
		s.showNotice("ocr/服务配置", "", fmt.Errorf("%s", tr("最多配置 24 个 OCR 服务")))
		return
	}
	provider := ocr.Config{ID: fmt.Sprintf("%s-%d", definition.Kind, time.Now().UnixNano()), Kind: definition.Kind, Name: definition.Name, Enabled: len(s.draft.OCRProviders) == 0}
	names := make(map[string]bool, len(s.draft.OCRProviders))
	for _, item := range s.draft.OCRProviders {
		names[item.Name] = true
	}
	for n := 2; names[provider.Name]; n++ {
		provider.Name = fmt.Sprintf("%s %d", definition.Name, n)
	}
	s.draft.OCRProviders = append(s.draft.OCRProviders, provider)
	s.ocrProviderID, s.query = provider.ID, ""
}

// removeOCRProvider 删除当前实例；删除启用项后需由用户选择另一个服务。
func (s *settingsUI) removeOCRProvider() {
	for _, provider := range s.draft.OCRProviders {
		if provider.ID != s.ocrProviderID {
			continue
		}
		id := provider.ID
		s.confirmDeletion("删除服务", tr("确定删除“")+ocrDisplayName(provider.Name)+"”？", func() {
			for i, item := range s.draft.OCRProviders {
				if item.ID == id {
					s.draft.OCRProviders = append(s.draft.OCRProviders[:i], s.draft.OCRProviders[i+1:]...)
					s.selectOCRProvider()
					return
				}
			}
		})
		return
	}
}

// ocrServices 沿用翻译服务的列表和详情布局，仅列出已有识别实现。
func (s *settingsUI) ocrServices(c *ui.Context, p pnPalette) {
	s.selectOCRProvider()
	ui.Row(c).Grow(1).MinWidth(0).FillWidth().Padding(12).Gap(12).Children(func() {
		ui.Column(c).Width(176).Shrink(0).FillHeight().Gap(8).Children(func() {
			if len(s.draft.OCRProviders) == 0 {
				ui.Text(c, tr("尚未配置服务")).Grow(1).FontSize(12).TextColor(p.Muted).Padding(12, 4)
			} else {
				ui.List(c.Key("ocr-services-list"), &s.ocrProviderList, len(s.draft.OCRProviders)).Grow(1).FillWidth().Gap(3).Rows(func(row ui.ListRow) {
					provider := &s.draft.OCRProviders[row.Index]
					id := provider.ID
					background := ui.Transparent
					if id == s.ocrProviderID {
						background = p.Group
					}
					ui.Row(row.Context.Key(id)).Height(38).Padding(5).Gap(5).Radius(8).Background(background).Children(func() {
						ui.ButtonBase(c).Grow(1).MinWidth(0).Gap(5).Justify(ui.Start).AlignItems(ui.Center).Tooltip(ocrDisplayName(provider.Name)).Children(func() {
							ui.Box(c).Size(22, 22).Shrink(0).Radius(6).Background(p.Surface).Center().Children(func() { pnIcon(c, "screenshot", 17, p.Text) })
							ui.Text(c, ocrDisplayName(provider.Name)).Grow(1).MinWidth(0).FontSize(12).SingleLine().TextAlign(ui.Start)
						}).OnClick(func() { s.ocrProviderID = id })
						toggle := ui.Switch(c, &provider.Enabled).Size(28, 17).Shrink(0).Label(tr("启用") + ocrDisplayName(provider.Name))
						if toggle.Changed() && provider.Enabled {
							for i := range s.draft.OCRProviders {
								if s.draft.OCRProviders[i].ID != id {
									s.draft.OCRProviders[i].Enabled = false
								}
							}
						}
					}).ContextMenu(func(menu *ui.Menu) {
						s.ocrProviderID = id
						if menu.Item(tr("删除")).Disabled(s.saving).Chosen() {
							s.removeOCRProvider()
						}
					})
				})
			}
			ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
				ui.MenuButton(c, tr("添加服务"), func(menu *ui.Menu) {
					for _, definition := range ocr.Definitions() {
						if menu.Item(tr(definition.Name)).Chosen() {
							s.addOCRProvider(definition)
						}
					}
				}).Grow(1).Height(29).AlignItems(ui.Center).FontSize(12)
				ui.ButtonBase(c).Size(29, 29).Center().Radius(7).Border(.5, p.FieldBorder).Label(tr("删除选中服务")).Tooltip(tr("删除选中服务")).Disabled(s.ocrProviderID == "").Children(func() { pnIcon(c, "trash", 14, p.Muted) }).OnClick(s.removeOCRProvider)
			})
		})
		ui.Box(c).Width(1).Shrink(0).FillHeight().Background(p.FieldBorder)
		ui.Scroll(c.Key("ocr-service-editor")).Grow(1).MinWidth(0).FillHeight().Gap(14).Padding(4, 2, 4, 0).Children(func() {
			var selected *ocr.Config
			for i := range s.draft.OCRProviders {
				if s.draft.OCRProviders[i].ID == s.ocrProviderID {
					selected = &s.draft.OCRProviders[i]
					break
				}
			}
			if selected == nil {
				ui.Text(c, tr("选择一个服务，设置连接方式。")).FontSize(13).TextColor(p.Muted).Padding(30, 0)
				s.inlineNotice(c, p, "服务配置")
				return
			}
			ui.Column(c.Key(selected.ID)).Shrink(0).Gap(14).Transition(settingsPageTransition()).Children(func() {
				ui.Row(c).Gap(9).Children(func() {
					ui.Box(c).Size(32, 32).Shrink(0).Radius(9).Background(p.Group).Center().Children(func() { pnIcon(c, "screenshot", 23, p.Text) })
					ui.Text(c, ocrDisplayName(selected.Name)).FontSize(15).FontWeight(650)
				})
				s.field(c, p, "服务名称", &selected.Name, "为此服务起个名字", false)
				ui.Text(c, tr("每次使用一个 OCR 服务，启用其他服务会自动停用当前服务。")).FontSize(12).TextColor(p.Muted).LineHeight(1.4)
				if selected.Kind == "system" {
					s.systemOCRDetails(c, p, selected.Enabled)
				}
				s.inlineNotice(c, p, "服务配置")
			})
		})
	})
}

// systemOCRDetails 展示系统引擎的使用条件；操作入口使用已保存的启用服务。
func (s *settingsUI) systemOCRDetails(c *ui.Context, p pnPalette, enabled bool) {
	engine, detail := "系统 OCR", "图片 OCR 暂仅支持 macOS 和 Windows"
	supported := true
	switch s.state.Platform.OS {
	case "darwin":
		engine = "macOS Vision"
		detail = "使用 macOS 内置文字识别，无需配置密钥。首次截图需在系统设置中允许 Glide 使用屏幕录制。"
	case "windows":
		engine = "Windows OCR"
		detail = "使用 Windows 内置文字识别，无需配置密钥。请在系统中安装所需语言的 OCR 功能。"
	default:
		supported = false
	}
	busy := s.app.ocrRunning()
	s.group(c, p, "识别服务", func() {
		s.row(c, p, "系统 OCR", detail, func() { ui.Text(c, tr(engine)).FontSize(12).TextColor(p.Muted) })
		s.row(c, p, "本地识别", "图片在本机识别，不会上传；识别出的文字会发送至已启用的翻译服务。", func() {})
	})
	s.group(c, p, "识别翻译", func() {
		s.row(c, p, "截图 OCR 翻译", "框选屏幕区域，识别文字后自动翻译；临时截图在识别后删除。", func() {
			ui.Button(c, tr("截图")).Disabled(busy || !supported || !enabled || s.dirty() || s.autoSaving).OnClick(func() { s.app.startOCR(true) })
		})
		s.row(c, p, "图片 OCR", "支持 PNG、JPEG、BMP、TIFF，最大 32 MB、4000 万像素。", func() {
			ui.Button(c, tr("选择图片")).Disabled(busy || !supported || !enabled || s.dirty() || s.autoSaving).OnClick(func() { s.app.startOCR(false) })
		})
	})
	if busy {
		ui.Text(c, tr("识别中…")).FontSize(12).TextColor(p.Muted)
	}
}
