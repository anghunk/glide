package main

import (
	"fmt"
	"log"
	"runtime"
	"strings"

	"github.com/egoist/mygo"
)

// nativeTrayMenu 持有系统菜单与当前快捷键；字段只在 MyGo 主线程读写。
type nativeTrayMenu struct {
	tray            *mygo.Tray
	menu            *mygo.Menu
	selectionItem   *mygo.MenuItem
	updateItem      *mygo.MenuItem
	shortcut        string
	selectionSource func() error
}

// newNativeTrayMenu 用系统菜单展示版本、翻译操作和设置帮助，不主动激活设置窗口。
func newNativeTrayMenu(a *GlideService) (*nativeTrayMenu, error) {
	a.mu.RLock()
	shortcut := a.hotkey
	a.mu.RUnlock()
	t := &nativeTrayMenu{shortcut: shortcut}
	t.updateItem = &mygo.MenuItem{ID: "check-updates", Label: "检查更新…", Click: func(*mygo.MenuItem, *mygo.Window) {
		a.showSettingsCategory("about")
		a.CheckForUpdates()
	}}
	t.selectionItem = &mygo.MenuItem{ID: "translate-selection", Label: "划词翻译", Accelerator: shortcut,
		Click: func(*mygo.MenuItem, *mygo.Window) {
			// Windows 托盘菜单会临时取得前台；恢复打开菜单前的应用，再读取它的选区。
			source := t.selectionSource
			t.selectionSource = nil
			go func() {
				if source != nil {
					if err := source(); err != nil {
						a.showPanelError(err.Error())
						return
					}
				}
				a.translateSelection()
			}()
		}}
	t.menu = mygo.NewMenu([]*mygo.MenuItem{
		{Label: "Glide " + mygo.App.Version(), Disabled: true},
		mygo.Separator(),
		{Label: "输入翻译", Click: func(*mygo.MenuItem, *mygo.Window) { a.OpenPanel() }},
		t.selectionItem,
		{Label: "剪贴板翻译", Click: func(*mygo.MenuItem, *mygo.Window) { a.showTranslation(mygo.Clipboard.ReadText()) }},
		mygo.Separator(),
		{Label: "设置…", Accelerator: "CmdOrCtrl+,", Click: func(*mygo.MenuItem, *mygo.Window) { a.ShowMain() }},
		{Label: "翻译历史", Click: func(*mygo.MenuItem, *mygo.Window) { a.showSettingsCategory("history") }},
		t.updateItem,
		mygo.Separator(),
		{Label: "帮助", Submenu: []*mygo.MenuItem{
			{Label: "使用说明", Click: func(*mygo.MenuItem, *mygo.Window) { a.showUsageHelp() }},
			{Label: "快捷键设置…", Click: func(*mygo.MenuItem, *mygo.Window) { a.showSettingsCategory("shortcut") }},
			{Label: "取词权限说明…", Click: func(*mygo.MenuItem, *mygo.Window) { a.showSettingsCategory("general") }},
		}},
		{Label: "关于 Glide", Click: func(*mygo.MenuItem, *mygo.Window) { a.showSettingsCategory("about") }},
		mygo.Separator(),
		{Label: "退出 Glide", Role: mygo.RoleQuit, Accelerator: "CmdOrCtrl+Q"},
	})
	tray, err := mygo.NewTray(mygo.TrayOptions{
		ID: "glide-main", Icon: trayIcon(), IconIsTemplate: trayIconIsTemplate(), ToolTip: "Glide · 划词翻译", Menu: t.menu,
	})
	if err != nil {
		return nil, err
	}
	t.tray = tray
	tray.OnClick(a.OpenPanel)
	tray.OnRightClick(func() { t.selectionSource = captureTraySelectionSource() })
	return t, nil
}

// refreshTrayMenu 同步实际已注册的组合键，保留注册冲突前仍可用的旧快捷键。
func (a *GlideService) refreshTrayMenu() {
	mygo.RunOnMain(func() {
		t := a.trayMenu
		if t == nil {
			return
		}
		a.mu.RLock()
		shortcut, quitting := a.hotkey, a.quitting
		a.mu.RUnlock()
		if quitting {
			return
		}
		changed := false
		if shortcut != t.shortcut {
			t.shortcut = shortcut
			t.selectionItem.SetAccelerator(shortcut)
			changed = true
		}
		if label := a.updateMenuLabel(); label != t.updateItem.Label {
			t.updateItem.SetLabel(label)
			changed = true
		}
		if changed {
			t.tray.SetMenu(t.menu)
		}
	})
}

// showSettingsCategory 从菜单定位设置分类，保留尚未保存的配置草稿。
func (a *GlideService) showSettingsCategory(category string) {
	mygo.RunOnMain(func() {
		if a.mainWindow == nil || a.settingsUI == nil {
			return
		}
		a.settingsUI.category, a.settingsUI.query, a.settingsUI.clearConfirm = category, "", false
		if category == "history" {
			a.settingsUI.loadHistory()
		}
		a.ShowMain()
		a.mainWindow.Invalidate()
	})
}

// showUsageHelp 展示随当前快捷键更新的离线说明，不依赖工作目录或外部网页。
func (a *GlideService) showUsageHelp() {
	a.mu.RLock()
	shortcut := a.settings.Shortcut
	a.mu.RUnlock()
	if runtime.GOOS == "darwin" {
		shortcut = strings.NewReplacer("CmdOrCtrl", "⌘", "Command", "⌘", "Cmd", "⌘", "Shift", "⇧", "Alt", "⌥", "Option", "⌥", "Control", "⌃", "Ctrl", "⌃", "+", "").Replace(shortcut)
	} else {
		shortcut = strings.ReplaceAll(shortcut, "CmdOrCtrl", "Ctrl")
	}
	a.showSettingsCategory("general")
	go func() {
		_, err := mygo.Dialog.Message(mygo.MessageOptions{
			Parent: a.mainWindow, Type: mygo.MessageInfo, Title: "Glide 使用说明", Message: "选中文字，即刻翻译",
			Detail:  fmt.Sprintf("1. 在其他应用中选中文字，按 %s 呼出译文。\n2. 也可从托盘选择“输入翻译”或“剪贴板翻译”。\n3. 在“设置 → 翻译服务”配置并启用渠道。\n4. 图钉可保留浮窗；未固定时，进入浮窗再切换应用会将它收起。\n\n无法取词时，请检查“通用”中的系统权限，或复制文字后使用剪贴板翻译。", shortcut),
			Buttons: []string{"知道了"},
		})
		if err != nil {
			log.Printf("打开使用说明失败：%v", err)
		}
	}()
}
