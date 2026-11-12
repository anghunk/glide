package main

import (
	"embed"
	"log"
	"strings"

	"github.com/egoist/mygo/ui"
)

// providerIconAssets 随应用分发固定版本的渠道 SVG 和原始许可，不依赖运行时网络。
//
//go:embed assets/providers/*.svg assets/providers/LICENSE
var providerIconAssets embed.FS

type providerArtwork struct {
	svg  *ui.SVG
	mono bool
}

// loadProviderIcons 只解析一次品牌图形；资源缺失或不兼容时记录原因并保留通用回退。
func loadProviderIcons() map[string]providerArtwork {
	definitions := []struct {
		kind, file string
		mono       bool
	}{
		{"google", "google-color.svg", false},
		{"bing", "bing-color.svg", false},
		{"google-cloud", "googlecloud-color.svg", false},
		{"azure", "azure-color.svg", false},
		{"tencent", "tencentcloud-color.svg", false},
		{"baidu", "baidu-color.svg", false},
		{"openai", "openai.svg", true},
		{"ollama", "ollama.svg", true},
	}
	icons := make(map[string]providerArtwork, len(definitions))
	for _, definition := range definitions {
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
