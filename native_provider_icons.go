package main

import (
	"embed"
	"log"
	"strings"

	"github.com/egoist/mygo/ui"
	"glide/internal/translation"
)

// providerIconAssets 随应用分发固定版本的渠道 SVG 和原始许可，不依赖运行时网络。
//
//go:embed assets/providers/*.svg assets/providers/LICENSE
var providerIconAssets embed.FS

type providerArtwork struct {
	svg  *ui.SVG
	mono bool
}

// providerIconChoices 统一管理本地可选图标及原始文件，品牌资源随应用离线分发。
var providerIconChoices = []struct {
	kind, label, file string
	mono              bool
}{
	{"", "默认图标", "", false},
	{"google", "Google", "google-color.svg", false},
	{"bing", "必应", "bing-color.svg", false},
	{"google-cloud", "Google Cloud", "googlecloud-color.svg", false},
	{"azure", "Azure", "azure-color.svg", false},
	{"tencent", "腾讯云", "tencentcloud-color.svg", false},
	{"baidu", "百度", "baidu-color.svg", false},
	{"deepl", "DeepL", "deepl.svg", true},
	{"openai", "OpenAI", "openai.svg", true},
	{"anthropic", "Anthropic", "anthropic.svg", true},
	{"claude", "Claude", "claude-color.svg", false},
	{"gemini", "Gemini", "gemini-color.svg", false},
	{"deepseek", "DeepSeek", "deepseek-color.svg", false},
	{"qwen", "通义千问", "qwen-color.svg", false},
	{"doubao", "豆包", "doubao-color.svg", false},
	{"kimi", "Kimi", "kimi-color.svg", false},
	{"grok", "Grok", "grok.svg", true},
	{"mistral", "Mistral", "mistral-color.svg", false},
	{"perplexity", "Perplexity", "perplexity-color.svg", false},
	{"copilot", "Copilot", "copilot-color.svg", false},
	{"huggingface", "Hugging Face", "huggingface-color.svg", false},
	{"ollama", "Ollama", "ollama.svg", true},
	{"globe", "通用图标", "", false},
}

// loadProviderIcons 只解析一次品牌图形；资源缺失或不兼容时记录原因并保留通用回退。
func loadProviderIcons() map[string]providerArtwork {
	icons := make(map[string]providerArtwork, len(providerIconChoices))
	for _, definition := range providerIconChoices {
		if definition.file == "" {
			continue
		}
		data, err := providerIconAssets.ReadFile("assets/providers/" + definition.file)
		if err != nil {
			log.Printf("读取渠道图标 %s 失败：%v", definition.kind, err)
			continue
		}
		icon, err := ui.ParseSVG(data)
		if err != nil {
			log.Printf("解析渠道图标 %s 失败：%v", definition.kind, err)
			continue
		}
		width, height := icon.Size()
		if width <= 0 || height <= 0 {
			log.Printf("渠道图标 %s 没有有效尺寸", definition.kind)
			continue
		}
		icons[definition.kind] = providerArtwork{svg: icon, mono: definition.mono}
	}
	return icons
}

var providerIcons = loadProviderIcons()

// pnProviderIcon 绘制指定渠道的品牌 SVG；单色图跟随 color，彩色图保留原色。
// size 使用逻辑像素，未知渠道或不可解析的资源回退为同尺寸的通用地球图标。
func pnProviderIcon(c *ui.Context, kind string, size float32, color ui.Color) ui.Element {
	icon, exists := providerIcons[strings.ToLower(strings.TrimSpace(kind))]
	if !exists || icon.svg == nil {
		return pnIcon(c, "globe", size, color).Size(size, size).Shrink(0)
	}
	if icon.mono {
		return ui.Icon(c, icon.svg).Size(size, size).Shrink(0).TextColor(color)
	}
	return ui.Image(c, icon.svg).Size(size, size).Shrink(0).TextColor(color)
}

// providerIconKind 优先使用显示图标，保留服务类型用于实际请求。
func providerIconKind(config translation.Config) string {
	if config.Icon != "" {
		return config.Icon
	}
	return config.Kind
}

// providerIconPicker 只显示当前图标与修改按钮，点击后再打开选择弹窗。
func (s *settingsUI) providerIconPicker(c *ui.Context, p pnPalette, config *translation.Config) {
	ui.Column(c.Key("provider-icon")).Shrink(0).Gap(6).Children(func() {
		ui.Text(c, tr("显示图标")).FontSize(12).TextColor(p.Muted)
		id := config.ID
		ui.ButtonBase(c).Size(30, 30).AlignSelf(ui.Start).Center().Radius(7).Border(.5, p.FieldBorder).Label(tr("修改服务图标")).Tooltip(tr("选择内置图标")).Children(func() {
			pnProviderIcon(c, providerIconKind(*config), 19, p.Text)
		}).OnClick(func() { s.iconPickerProviderID = id; s.iconPickerOpen = true })
	})
}

// providerIconDialog 按服务标识查找最新草稿，避免自动保存后仍修改过期配置指针。
func (s *settingsUI) providerIconDialog(c *ui.Context, p pnPalette) {
	var config *translation.Config
	for i := range s.draft.Providers {
		if s.draft.Providers[i].ID == s.iconPickerProviderID {
			config = &s.draft.Providers[i]
			break
		}
	}
	if config == nil || s.category != "services" {
		s.iconPickerOpen = false
	}
	ui.DialogBase(c.Key("provider-icon-dialog"), &s.iconPickerOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, .22))
		panel.Width(500).Padding(20).Gap(14).Radius(14).Background(p.Surface).TextColor(p.Text).Border(.5, p.FieldBorder).Shadow(0, 8, 28, 0, ui.RGBA(0, 0, 0, .18)).Label(tr("选择服务图标"))
		ui.Row(c).Gap(10).Children(func() {
			ui.Text(c, tr("选择服务图标")).Grow(1).FontSize(16).FontWeight(650)
			ui.ButtonBase(c).Size(26, 26).Radius(6).Center().Label(tr("关闭图标选择")).Tooltip(tr("关闭")).Children(func() { pnIcon(c, "close", 15, p.Muted) }).OnClick(func() { s.iconPickerOpen = false })
		})
		ui.Text(c, tr(config.Name+tr(" · 选择后自动保存"))).FontSize(12).TextColor(p.Muted).SingleLine()
		ui.Scroll(c).Height(350).FillWidth().Children(func() {
			ui.Row(c).FillWidth().Wrap().Gap(8).Children(func() {
				for _, choice := range providerIconChoices {
					kind := choice.kind
					selected := config.Icon == kind
					button := ui.ButtonBase(c.Key(kind)).Width(84).Height(62).Radius(9).Border(.5, p.FieldBorder).Label(tr(choice.label)).Tooltip(tr(choice.label))
					if selected {
						button.Background(p.Group).BorderColor(p.Accent)
					} else if button.Hovered() {
						button.Background(p.Hover)
					}
					button.Children(func() {
						ui.Column(c).Fill().Center().Gap(7).Children(func() {
							displayKind := kind
							if displayKind == "" {
								displayKind = config.Kind
							}
							pnProviderIcon(c, displayKind, 25, p.Text)
							ui.Text(c, tr(choice.label)).FontSize(10.5).SingleLine().TextColor(p.Muted)
						})
					}).OnClick(func() { config.Icon = kind; s.iconPickerOpen = false })
				}
			})
		})
	})
}
