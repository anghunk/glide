package main

import (
	"embed"
	"github.com/egoist/mygo"
	"log"
	"strings"

	"github.com/egoist/mygo/ui"
)

// pnPalette 集中保存 PaneNest 的表面、文字和语义色，供设置页与浮窗复用。
type pnPalette struct {
	Window, Surface, Group, Text, Muted, Faint                       ui.Color
	Accent, Hover, Selected, Divider, FieldBorder, Attention, Danger ui.Color
}

// pnColors 按当前主题给出独立的深浅配色，避免在暗色模式直接反转浅色层级。
func pnColors(dark bool) pnPalette {
	if dark {
		return pnPalette{Window: ui.Hex("#141418"), Surface: ui.Hex("#232328"), Group: ui.Hex("#323237"), Text: ui.Hex("#ececf0"), Muted: ui.Hex("#a9a9b4"), Faint: ui.Hex("#83838f"), Accent: ui.Hex("#6b93ff"), Hover: ui.RGBA(255, 255, 255, .05), Selected: ui.Hex("#303036"), Divider: ui.RGBA(255, 255, 255, .045), FieldBorder: ui.RGBA(255, 255, 255, .13), Attention: ui.Hex("#f0a33a"), Danger: ui.Hex("#ff6961")}
	}
	return pnPalette{Window: ui.Hex("#eceef4"), Surface: ui.Hex("#ffffff"), Group: ui.Hex("#f3f3f4"), Text: ui.Hex("#1d1d1f"), Muted: ui.Hex("#6e6e78"), Faint: ui.Hex("#a3a3ad"), Accent: ui.Hex("#3b6cf6"), Hover: ui.RGBA(0, 0, 0, .04), Selected: ui.Hex("#ffffff"), Divider: ui.RGBA(0, 0, 0, .04), FieldBorder: ui.RGBA(0, 0, 0, .13), Attention: ui.Hex("#c27b0f"), Danger: ui.Hex("#d93a35")}
}

// pnApplyTheme 根据主题偏好配置控件与配色；跟随系统时使用每帧的系统主题。
func pnApplyTheme(c *ui.Context, mode string) pnPalette {
	t := *c.Theme()
	switch mode {
	case "light":
		t = *ui.LightTheme()
	case "dark":
		t = *ui.DarkTheme()
	}
	p := pnColors(t.Dark)
	t.Background, t.Surface, t.Text, t.TextMuted = p.Window, p.Surface, p.Text, p.Muted
	t.SurfaceHover, t.SurfacePressed, t.Border = p.Group, p.Hover, p.FieldBorder
	t.Accent, t.AccentHover, t.AccentPressed = p.Accent, p.Accent, p.Accent
	t.AccentText, t.Focus, t.Danger = ui.Hex("#ffffff"), p.Accent, p.Danger
	t.Radius, t.FontSize, t.Spacing = 7, 13.5, 3.5
	t.FocusRingWidth, t.FocusRingOffset = 2.5, 1
	c.SetTheme(&t)
	return p
}

//go:embed assets/lucide/*.svg assets/lucide/LICENSE
var lucideAssets embed.FS

var pnIcons = func() map[string]*ui.SVG {
	files := map[string]string{"screenshot": "screenshot", "home": "home", "eye": "eye", "eye-off": "eye-off", "settings": "settings", "globe": "globe", "keyboard": "keyboard", "history": "history", "shield": "shield-check", "info": "info", "search": "search", "plus": "plus", "minus": "minus", "trash": "trash-2", "check": "check", "pin": "pin", "close": "x", "copy": "copy", "refresh": "refresh-cw", "chevron-down": "chevron-down", "chevron-right": "chevron-right", "swap": "arrow-right-left", "bolt": "zap"}
	icons := make(map[string]*ui.SVG, len(files))
	for name, file := range files {
		data, err := lucideAssets.ReadFile("assets/lucide/" + file + ".svg")
		if err != nil {
			log.Printf("读取通用图标 %s 失败：%v", name, err)
			continue
		}
		icon, err := ui.ParseSVG(data)
		if err != nil {
			log.Printf("解析通用图标 %s 失败：%v", name, err)
			continue
		}
		icons[name] = icon
	}
	return icons
}()

// pnIcon 使用统一线条图标，未识别的名称回退为信息图标。
func pnIcon(c *ui.Context, name string, size float32, color ui.Color) ui.Element {
	icon := pnIcons[name]
	if icon == nil {
		icon = pnIcons["info"]
	}
	return ui.Icon(c, icon).FontSize(size).TextColor(color)
}

type pnLanguage struct{ Code, Name string }

var pnLanguages = []pnLanguage{{"zh-CN", "简体中文"}, {"zh-TW", "繁體中文"}, {"en", "英语"}, {"ja", "日语"}, {"ko", "韩语"}, {"fr", "法语"}, {"de", "德语"}, {"es", "西班牙语"}, {"ru", "俄语"}, {"pt", "葡萄牙语"}, {"it", "意大利语"}, {"ar", "阿拉伯语"}, {"th", "泰语"}, {"vi", "越南语"}}

// pnLanguageName 将服务代码显示为语言名称，未知代码保留原值。
func pnLanguageName(code string) string {
	if code == "smart" {
		return "智能互译"
	}
	if code == "auto" {
		return "自动识别"
	}
	for _, language := range pnLanguages {
		if language.Code == code {
			return language.Name
		}
	}
	return code
}

// pnLanguageSelect 显示语言名称并写回代码；compact 保持给定宽度，适用于紧凑浮窗。
// 返回本帧是否发生选择变化，显示文字截断时可通过悬停查看完整名称。
func pnLanguageSelect(c *ui.Context, code *string, includeAuto bool, width float32, compact bool) bool {
	if !compact && strings.HasPrefix(mygo.App.Locale(), "en") {
		width = max(width, 160)
	}
	options := make([]string, 0, len(pnLanguages)+1)
	if includeAuto {
		options = append(options, "自动识别")
	} else {
		options = append(options, "智能互译")
	}
	for _, language := range pnLanguages {
		options = append(options, language.Name)
	}
	chosen := pnLanguageName(*code)
	changed := localizedSelect(c, &chosen, options).Width(width).MinWidth(0).Height(28).Radius(14).FontSize(12.5).Tooltip(tr(chosen)).Changed()
	if changed {
		if chosen == "智能互译" {
			*code = "smart"
		} else if chosen == "自动识别" {
			*code = "auto"
		} else {
			for _, language := range pnLanguages {
				if language.Name == chosen {
					*code = language.Code
					break
				}
			}
		}
	}
	return changed
}
