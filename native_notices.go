package main

import (
	"github.com/egoist/mygo/ui"
	"time"
)

// settingsNotice 将操作结果保存在所属设置分组，异步完成后不串到其他页面。
type settingsNotice struct {
	text      string
	failed    bool
	expiresAt time.Time
}

// showNotice 在主线程更新指定分组，必要提示保留三秒；空消息移除旧提示。
func (s *settingsUI) showNotice(scope, message string, err error) {
	if s.messages == nil {
		s.messages = map[string]settingsNotice{}
	}
	if err != nil {
		message = err.Error()
	}
	if message == "" {
		delete(s.messages, scope)
		return
	}
	s.messages[scope] = settingsNotice{text: message, failed: err != nil, expiresAt: time.Now().Add(3 * time.Second)}
}

// operationScope 定位当前操作所属的设置列表，供异步任务捕获。
func (s *settingsUI) operationScope() string {
	section := ""
	switch s.category {
	case "general":
		section = "系统权限"
	case "services":
		section = "服务配置"
	case "proxy":
		section = "系统代理"
	case "shortcut":
		section = "全局快捷键"
	case "history":
		section = "翻译历史"
	case "about":
		section = "隐私与数据"
	case "advanced":
		section = "WebDAV 同步"
		if s.backupDrawerOpen {
			section = "远程备份"
		}
	}
	return s.category + "/" + section
}

// saveScope 将自动保存错误放在实际编辑的设置下方。
func (s *settingsUI) saveScope() string {
	if s.category == "shortcut" && (s.state.Settings.PanelTranslateShortcut != s.draft.PanelTranslateShortcut || s.state.Settings.PanelCloseShortcut != s.draft.PanelCloseShortcut) {
		return "shortcut/翻译浮窗"
	}
	if s.category != "general" {
		return s.operationScope()
	}
	before, after := s.state.Settings, s.draft
	section := "翻译浮窗"
	switch {
	case before.ThemeMode != after.ThemeMode:
		section = "主题风格"
	case before.UILanguage != after.UILanguage:
		section = "界面语言"
	case before.HistoryLimit != after.HistoryLimit:
		section = "翻译历史"
	case before.SourceLang != after.SourceLang || before.TargetLang != after.TargetLang:
		section = "默认语言"
	case before.AutoSelection != after.AutoSelection:
		section = "取词方式"
	case before.TrayClickShowsPanel != after.TrayClickShowsPanel:
		section = "托盘"
	}
	return "general/" + section
}

// inlineNotice 显示当前分组的必要提示，到期请求重绘并清除；新提示独立计时。
func (s *settingsUI) inlineNotice(c *ui.Context, p pnPalette, section string) {
	scope := s.category + "/" + section
	notice, exists := s.messages[scope]
	if !exists {
		return
	}
	remaining := time.Until(notice.expiresAt)
	if remaining <= 0 {
		delete(s.messages, scope)
		return
	}
	c.After(remaining)
	color := p.Muted
	if notice.failed {
		color = p.Danger
	}
	ui.Text(c.Key("notice-"+section), tr(notice.text)).Shrink(0).FillWidth().Padding(3, 4).FontSize(12).LineHeight(1.5).TextColor(color).Selectable()
}
