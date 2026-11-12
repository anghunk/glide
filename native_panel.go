package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"glide/internal/translation"
)

// panelUI 只保存主线程上的编辑与展开状态；后台结果由服务快照提供。
type panelUI struct {
	app          *GlideService
	initialized  bool
	requestID    string
	inputSession uint64
	draft        string
	sourceLang   string
	targetLang   string
	collapsed    map[string]bool
	copiedID     string
	copiedUntil  time.Time
	message      string
}

// newPanelUI 创建紧凑浮窗状态，不发起网络请求或读取文件。
func newPanelUI(app *GlideService) *panelUI {
	return &panelUI{app: app, collapsed: make(map[string]bool)}
}

// syncSnapshot 每批新选区只同步一次原文，逐渠道返回结果时保留用户的在途编辑。
func (p *panelUI) syncSnapshot(state PanelState, settings Settings) {
	if !p.initialized {
		p.sourceLang, p.targetLang = settings.SourceLang, settings.TargetLang
		p.initialized = true
	}
	if response := state.Response; response != nil && response.RequestID != "" && response.RequestID != p.requestID {
		p.requestID = response.RequestID
		p.draft, p.sourceLang, p.targetLang = response.Text, response.SourceLang, response.TargetLang
		p.inputSession++
		p.message, p.copiedID = "", ""
	}
	if p.sourceLang == "" {
		p.sourceLang = "auto"
	}
	if p.targetLang == "" || p.targetLang == "auto" {
		p.targetLang = "zh-CN"
	}
}

// view 构建白灰色紧凑浮窗；编辑、切换与复制均直接调用 Go 服务。
func (p *panelUI) view(c *ui.Context) {
	colors := pnApplyTheme(c)
	state := p.app.GetPanelState()
	settings := p.app.GetState().Settings
	p.syncSnapshot(state, settings)
	if p.copiedID != "" {
		remaining := time.Until(p.copiedUntil)
		if remaining > 0 {
			c.After(remaining)
		} else {
			p.copiedID = ""
		}
	}
	providers := make(map[string]translation.Config, len(settings.Providers))
	for _, provider := range settings.Providers {
		providers[provider.ID] = provider
	}
	c.Root().Background(colors.Surface)
	ui.Column(c.Key("translation-panel")).Fill().Background(colors.Surface).TextColor(colors.Text).Radius(11).Border(1, colors.Divider).Clip().Children(func() {
		p.toolbar(c, state, colors)
		ui.Column(c.Key("panel-composer")).Shrink(0).Padding(0, 12, 10).Gap(8).Children(func() {
			if settings.ShowSourceInput {
				input := ui.TextArea(c.Key(struct {
					Kind    string
					Session uint64
				}{"panel-source", p.inputSession}), &p.draft).
					Height(76).FillWidth().Padding(9, 10).Radius(7).FontSize(13).
					Background(colors.Group).Border(1, colors.FieldBorder).
					Label("需要翻译的原文").Placeholder("选中文字后按快捷键，或在此输入…").MaxLength(10000).
					OnTextKey(func(key ui.TextKey) bool {
						if key.Key == ui.KeyEnter && key.Mods == ui.Cmd {
							p.draft = key.Text()
							p.submit()
							return true
						}
						return false
					})
				if input.Changed() {
					p.message = ""
				}
			}
			ui.Row(c.Key("panel-language-actions")).Gap(6).Children(func() {
				if settings.ShowLanguageBar {
					pnLanguageSelect(c.Key("panel-source-language"), &p.sourceLang, true, 116)
					p.iconButton(c.Key("panel-swap-languages"), "swap", "交换语言", false, colors).OnClick(p.swapLanguages)
					pnLanguageSelect(c.Key("panel-target-language"), &p.targetLang, false, 108)
				}
				ui.Spacer(c)
				ui.PrimaryButton(c.Key("panel-translate"), "翻译").Height(28).Padding(4, 12).FontSize(12).
					Tooltip("重新翻译原文（⌘ / Ctrl + Enter）").Disabled(strings.TrimSpace(p.draft) == "").OnClick(p.submit)
			})
		})
		ui.Divider(c).Height(.5).Background(colors.Divider)
		ui.Scroll(c.Key("panel-results")).Grow(1).MinHeight(50).Padding(10, 12).Gap(8).Children(func() {
			if state.Error != "" {
				p.notice(c, state.Error, colors.Danger, colors)
			}
			if p.message != "" {
				p.notice(c, p.message, colors.Attention, colors)
			}
			if state.Response != nil {
				for _, result := range state.Response.Results {
					p.resultCard(c.Key(result.ProviderID), result, providers[result.ProviderID], colors)
				}
			} else if state.Loading {
				ui.Row(c).Gap(8).Padding(16, 4).Children(func() {
					ui.Spinner(c).Size(14, 14)
					ui.Text(c, "正在准备翻译…").TextColor(colors.Muted).FontSize(12)
				})
			} else if state.Error == "" {
				ui.Column(c).Padding(24, 12).Gap(9).AlignItems(ui.Center).Children(func() {
					pnIcon(c, "globe", 26, colors.Faint)
					ui.Text(c, "选中文字，即刻翻译").FontSize(14).FontWeight(600)
					ui.Text(c, "使用 "+settings.Shortcut+" 唤起浮窗").FontSize(11).TextColor(colors.Muted).TextAlign(ui.Center)
				})
			}
		})
	})
	if c.Shortcut(ui.Cmd, ui.KeyEnter) {
		p.submit()
	}
	if c.Shortcut(0, ui.KeyEscape) {
		p.app.HidePanel()
	}
}

// toolbar 提供固定、偏好设置和关闭入口，空白标题区域可拖动浮窗。
func (p *panelUI) toolbar(c *ui.Context, state PanelState, colors pnPalette) {
	ui.Row(c.Key("panel-toolbar")).Height(38).Shrink(0).Padding(5, 8).Gap(3).Children(func() {
		pinLabel := "固定浮窗"
		if state.Pinned {
			pinLabel = "取消固定"
		}
		p.iconButton(c.Key("panel-pin"), "pin", pinLabel, state.Pinned, colors).OnClick(func() {
			p.app.SetPanelPinned(!state.Pinned)
		})
		ui.Row(c.Key("panel-drag-area")).Grow(1).Height(28).Padding(0, 4).DragWindow().Children(func() {
			if state.Loading {
				ui.Spinner(c).Size(11, 11)
			}
		})
		p.iconButton(c.Key("panel-settings"), "settings", "偏好设置", false, colors).OnClick(p.app.ShowMain)
		p.iconButton(c.Key("panel-close"), "close", "关闭浮窗", false, colors).OnClick(p.app.HidePanel)
	})
}

// iconButton 绘制具备真实可访问名称和悬停反馈的紧凑图标按钮。
func (p *panelUI) iconButton(c *ui.Context, name, label string, selected bool, colors pnPalette) ui.Element {
	button := ui.ButtonBase(c).Size(27, 27).Radius(6).Center().Label(label).Tooltip(label)
	color := colors.Muted
	if selected {
		button.Background(colors.Selected)
		color = colors.Accent
	} else if button.Hovered() {
		button.Background(colors.Hover)
		color = colors.Text
	}
	return button.Children(func() { pnIcon(c, name, 15, color) })
}

// resultCard 展示单个真实翻译渠道，折叠与复制互不影响其他渠道。
func (p *panelUI) resultCard(c *ui.Context, result ChannelResult, provider translation.Config, colors pnPalette) {
	ui.Column(c).Shrink(0).Radius(8).Background(colors.Group).Border(.5, colors.Divider).Clip().Children(func() {
		ui.Row(c).Height(38).Padding(5, 8, 5, 10).Gap(7).Children(func() {
			ui.Box(c).Size(21, 21).Radius(5).Background(colors.Surface).Center().Children(func() {
				pnProviderIcon(c, provider.Kind, 16, colors.Text)
			})
			name := result.ProviderName
			if name == "" {
				name = "翻译渠道"
			}
			ui.Text(c, name).Grow(1).MinWidth(0).SingleLine().FontSize(12).FontWeight(600)
			if provider.Model != "" && (provider.Kind == "openai" || provider.Kind == "ollama") {
				ui.Text(c, provider.Model).MaxWidth(104).SingleLine().FontSize(10).TextColor(colors.Muted).
					Padding(3, 5).Radius(4).Background(colors.Surface)
			}
			icon, label := "chevron-down", "收起译文"
			if p.collapsed[result.ProviderID] {
				icon, label = "chevron-right", "展开译文"
			}
			p.iconButton(c.Key("collapse"), icon, label, false, colors).OnClick(func() {
				p.collapsed[result.ProviderID] = !p.collapsed[result.ProviderID]
			})
		})
		if p.collapsed[result.ProviderID] {
			return
		}
		ui.Column(c.Key("result-content")).Padding(0, 11, 10).Gap(9).Children(func() {
			if result.Pending {
				ui.Row(c).Gap(7).Padding(5, 0).Children(func() {
					ui.Spinner(c).Size(12, 12)
					ui.Text(c, "正在翻译…").TextColor(colors.Muted).FontSize(12)
				})
			} else {
				if result.Text != "" {
					ui.Text(c.Key("translated-text"), result.Text).Selectable().FontSize(13).LineHeight(1.55).TextColor(colors.Text)
				}
				if result.Error != "" {
					ui.Text(c, result.Error).Selectable().FontSize(11).LineHeight(1.5).TextColor(colors.Danger)
				}
				ui.Row(c).Height(23).Gap(5).Children(func() {
					metadata := panelDuration(result.DurationMS)
					if result.DetectedLang != "" && result.DetectedLang != "auto" {
						metadata = "检测到" + pnLanguageName(panelLanguageCode(result.DetectedLang)) + " · " + metadata
					}
					ui.Text(c, metadata).Grow(1).MinWidth(0).SingleLine().FontSize(10).TextColor(colors.Faint)
					if result.Text != "" {
						copied := p.copiedID == result.ProviderID
						label := "复制译文"
						icon := "copy"
						if copied {
							label, icon = "已复制", "check"
						}
						p.iconButton(c.Key("copy-result"), icon, label, copied, colors).Size(24, 23).OnClick(func() {
							p.app.CopyText(result.Text)
							p.copiedID, p.copiedUntil = result.ProviderID, time.Now().Add(1500*time.Millisecond)
						})
					}
				})
			}
		})
	})
}

// notice 显示权限、请求或输入错误，保留文本选择以便复制诊断信息。
func (p *panelUI) notice(c *ui.Context, message string, color ui.Color, colors pnPalette) {
	ui.Column(c).Shrink(0).Padding(10).Radius(7).Background(colors.Group).Border(.5, colors.Divider).Children(func() {
		ui.Text(c, message).Selectable().FontSize(11).LineHeight(1.5).TextColor(color)
	})
}

// submit 复制本次编辑值后启动后台翻译，避免网络请求阻塞界面线程。
func (p *panelUI) submit() {
	text := strings.TrimSpace(p.draft)
	if text == "" {
		p.message = "请输入需要翻译的文字"
		return
	}
	p.message = ""
	p.app.TranslatePanel(TranslateRequest{Text: text, SourceLang: p.sourceLang, TargetLang: p.targetLang})
}

// swapLanguages 在自动检测模式下优先采用本次已检测语种，保持目标语言有效。
func (p *panelUI) swapLanguages() {
	source := p.sourceLang
	if source == "auto" {
		state := p.app.GetPanelState()
		if state.Response != nil {
			for _, result := range state.Response.Results {
				if result.DetectedLang != "" && result.DetectedLang != "auto" {
					source = panelLanguageCode(result.DetectedLang)
					break
				}
			}
		}
		if source == "auto" {
			if p.targetLang == "en" {
				source = "zh-CN"
			} else {
				source = "en"
			}
		}
	}
	p.sourceLang, p.targetLang = p.targetLang, source
}

// panelLanguageCode 统一不同渠道返回的常见中文代码，保证交换后仍可在语言栏选择。
func panelLanguageCode(code string) string {
	switch strings.ToLower(code) {
	case "zh", "zh-hans", "zh-cn", "chs":
		return "zh-CN"
	case "zh-hant", "zh-tw", "cht":
		return "zh-TW"
	default:
		return code
	}
}

// panelDuration 将真实请求耗时显示为适合紧凑卡片的短文本。
func panelDuration(milliseconds int64) string {
	if milliseconds < 1000 {
		return fmt.Sprintf("%d ms", milliseconds)
	}
	return fmt.Sprintf("%.1f 秒", float64(milliseconds)/1000)
}
