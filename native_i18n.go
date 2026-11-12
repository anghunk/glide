package main

import (
	"glide/internal/translation"
	"glide/locales"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// tr 按当前界面语言读取文案，缺失条目回退到简体中文原文。
func tr(message string) string {
	return locales.Text(mygo.App.Locale(), message)
}

// localizedSelect 显示本地化选项，仍将原始值写入配置，避免翻译影响业务判断。
func localizedSelect(c *ui.Context, selected *string, options []string) ui.Element {
	t := c.Theme()
	sel := ui.SelectBase(c, selected)
	sel.Trigger.Padding(5, 9).Gap(6).Radius(t.Radius).Background(t.Surface).Border(.5, t.Border).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
		ui.Text(c, tr(*selected)).Grow(1).MinWidth(0).SingleLine()
		pnIcon(c, "chevron-down", 12, t.TextMuted)
	})
	sel.Popup(func(panel ui.Element) {
		panel.Padding(4).Radius(t.Radius).Background(t.Background).Border(.5, t.Border).MaxHeight(320)
		ui.Scroll(c).FillWidth().MaxHeight(300).Children(func() {
			for _, option := range options {
				item := sel.Item(option).Padding(6, 10).Radius(t.Radius)
				if item.Highlighted() {
					item.Background(t.Accent).TextColor(t.AccentText)
				}
				item.Children(func() { ui.Text(c, tr(option)).SingleLine() })
			}
		})
	})
	return sel.Trigger
}

// configureLocale 应用已保存的界面语言；空值代表持续跟随系统。
func (a *GlideService) configureLocale() {
	mygo.App.SetLocale(a.settings.UILanguage)
	mygo.App.OnLocaleChanged(func(string) {
		if a.mainWindow != nil {
			a.mainWindow.SetTitle(tr("Glide 偏好设置"))
		}
		a.configureApplicationMenu()
		if a.trayMenu != nil {
			a.trayMenu.localize()
			a.refreshTrayMenu()
		}
		a.notifySettings()
	})
}

// localize 用原始文案刷新托盘标签，支持反复切换语言。
func (t *nativeTrayMenu) localize() {
	for item, source := range t.labels {
		item.SetLabel(tr(source))
	}
	t.tray.SetToolTip(tr("Glide · 划词翻译"))
}

// providerDisplayName 仅本地化内置服务名称，保留用户自定义名称。
func providerDisplayName(name string) string {
	if defaultProviderNames[name] {
		return tr(name)
	}
	return name
}

var defaultProviderNames = func() map[string]bool {
	names := map[string]bool{}
	for _, definition := range translation.Definitions() {
		names[definition.Name] = true
	}
	return names
}()
