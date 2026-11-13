package main

import (
	"embed"
	"log"
	"strings"

	"github.com/egoist/mygo/ui"
	"glide/internal/ocr"
	"glide/internal/translation"
)

// providerIconAssets 随应用分发固定版本的渠道 SVG 和原始许可，不依赖运行时网络。
//
//go:embed assets/providers/*.svg assets/providers/*.png assets/providers/LICENSE
var providerIconAssets embed.FS

type providerArtwork struct {
	image ui.ImageSource
	mono  bool
}

// providerIconChoices 统一管理本地可选图标及原始文件，品牌资源随应用离线分发。
var providerIconChoices = []struct {
	kind, label, file string
	mono              bool
}{
	{"", "默认图标", "", false},
	{"system", "系统 OCR", "", false},
	{"google", "Google", "google-color.svg", false},
	{"bing", "必应", "bing-color.svg", false},
	{"google-cloud", "Google Cloud", "googlecloud-color.svg", false},
	{"azure", "Azure", "azure-color.svg", false},
	{"tencent", "腾讯云", "tencentcloud-color.svg", false},
	{"aliyun", "阿里云", "alibabacloud-color.svg", false},
	{"volcengine", "火山引擎", "volcengine-color.svg", false},
	{"baidu", "百度", "baidu-color.svg", false},
	{"huawei", "华为云", "huaweicloud-color.svg", false},
	{"xfyun", "科大讯飞", "iflytekcloud-color.svg", false},
	{"youdao", "网易有道", "youdao.png", false},
	{"deepl", "DeepL", "deepl.svg", true},
	{"openai", "OpenAI", "openai.svg", true},
	{"anthropic", "Anthropic", "anthropic.svg", true},
	{"claude", "Claude", "claude-color.svg", false},
	{"gemini", "Gemini", "gemini-color.svg", false},
	{"deepseek", "DeepSeek", "deepseek-color.svg", false},
	{"qwen", "通义千问", "qwen-color.svg", false},
	{"doubao", "豆包", "doubao-color.svg", false},
	{"kimi", "Kimi", "kimi-color.svg", false},
	{"hunyuan", "腾讯混元", "hunyuan-color.svg", false},
	{"zhipu", "智谱", "zhipu-color.svg", false},
	{"minimax", "MiniMax", "minimax-color.svg", false},
	{"spark", "讯飞星火", "spark-color.svg", false},
	{"siliconcloud", "硅基流动", "siliconcloud-color.svg", false},
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
		icon, err := ui.DecodeImage(data)
		if err != nil {
			log.Printf("解析渠道图标 %s 失败：%v", definition.kind, err)
			continue
		}
		width, height := ui.ImageSize(icon)
		if width <= 0 || height <= 0 {
			log.Printf("渠道图标 %s 没有有效尺寸", definition.kind)
			continue
		}
		icons[definition.kind] = providerArtwork{image: icon, mono: definition.mono}
	}
	return icons
}

var providerIcons = loadProviderIcons()

// pnProviderIcon 绘制渠道品牌资源；单色图跟随 color，彩色图保留原色。
// size 使用逻辑像素，未知渠道或不可解析的资源回退为同尺寸的通用地球图标。
func pnProviderIcon(c *ui.Context, kind string, size float32, color ui.Color) ui.Element {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "system" {
		return pnIcon(c, "screenshot", size, color).Size(size, size).Shrink(0)
	}
	icon, exists := providerIcons[kind]
	if !exists || icon.image == nil {
		return pnIcon(c, "globe", size, color).Size(size, size).Shrink(0)
	}
	if icon.mono {
		if svg, ok := icon.image.(*ui.SVG); ok {
			return ui.Icon(c, svg).Size(size, size).Shrink(0).TextColor(color)
		}
	}
	return ui.Image(c, icon.image).Size(size, size).Shrink(0).TextColor(color)
}

// providerIconKind 优先使用显示图标，保留服务类型用于实际请求。
func providerIconKind(config translation.Config) string {
	if config.Icon != "" {
		return config.Icon
	}
	return config.Kind
}

// ocrProviderIconKind 优先显示自定义图标，默认按实际识别渠道选择品牌。
func ocrProviderIconKind(config ocr.Config) string {
	if config.Icon != "" {
		return config.Icon
	}
	return config.Kind
}

// serviceIconPicker 为翻译和 OCR 提供同一图标入口，分类用于区分可能相同的实例 ID。
func (s *settingsUI) serviceIconPicker(c *ui.Context, p pnPalette, category, id, iconKind string) {
	ui.Column(c.Key("provider-icon")).Shrink(0).Gap(6).Children(func() {
		ui.Text(c, tr("显示图标")).FontSize(12).TextColor(p.Muted)
		ui.ButtonBase(c).Size(30, 30).AlignSelf(ui.Start).Center().Radius(7).Border(.5, p.FieldBorder).Label(tr("修改服务图标")).Tooltip(tr("选择内置图标")).Children(func() {
			pnProviderIcon(c, iconKind, 19, p.Text)
		}).OnClick(func() {
			s.iconPickerCategory, s.iconPickerProviderID = category, id
			s.iconPickerOpen = true
		})
	})
}

// providerIconDialog 按服务标识查找最新草稿，避免自动保存后仍修改过期配置指针。
func (s *settingsUI) providerIconDialog(c *ui.Context, p pnPalette) {
	var name, defaultKind string
	var icon *string
	switch s.iconPickerCategory {
	case "services":
		for i := range s.draft.Providers {
			config := &s.draft.Providers[i]
			if config.ID == s.iconPickerProviderID {
				name, defaultKind, icon = providerDisplayName(config.Name), config.Kind, &config.Icon
				break
			}
		}
	case "ocr":
		for i := range s.draft.OCRProviders {
			config := &s.draft.OCRProviders[i]
			if config.ID == s.iconPickerProviderID {
				name, defaultKind, icon = ocrDisplayName(config.Name), config.Kind, &config.Icon
				break
			}
		}
	}
	if icon == nil || s.category != s.iconPickerCategory {
		s.iconPickerOpen = false
		return
	}
	ui.DialogBase(c.Key("provider-icon-dialog"), &s.iconPickerOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, .22))
		panel.Width(500).Padding(20).Gap(14).Radius(14).Background(p.Surface).TextColor(p.Text).Border(.5, p.FieldBorder).Shadow(0, 8, 28, 0, ui.RGBA(0, 0, 0, .18)).Label(tr("选择服务图标"))
		ui.Row(c).Gap(10).Children(func() {
			ui.Text(c, tr("选择服务图标")).Grow(1).FontSize(16).FontWeight(650)
			ui.ButtonBase(c).Size(26, 26).Radius(6).Center().Label(tr("关闭图标选择")).Tooltip(tr("关闭")).Children(func() { pnIcon(c, "close", 15, p.Muted) }).OnClick(func() { s.iconPickerOpen = false })
		})
		ui.Text(c, name+tr(" · 选择后自动保存")).FontSize(12).TextColor(p.Muted).SingleLine()
		ui.Scroll(c).Height(350).FillWidth().Children(func() {
			ui.Row(c).FillWidth().Wrap().Gap(8).Children(func() {
				for _, choice := range providerIconChoices {
					kind := choice.kind
					selected := *icon == kind
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
								displayKind = defaultKind
							}
							pnProviderIcon(c, displayKind, 25, p.Text)
							ui.Text(c, tr(choice.label)).FontSize(10.5).SingleLine().TextColor(p.Muted)
						})
					}).OnClick(func() { *icon = kind; s.iconPickerOpen = false })
				}
			})
		})
	})
}
