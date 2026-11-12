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
		{Label: "Glide " + mygo.App.Version(), Click: func(*mygo.MenuItem, *mygo.Window) {
			if err := a.OpenExternal("https://github.com/anghunk/glide"); err != nil {
				log.Printf("打开 GitHub 仓库失败：%v", err)
			}
		}},
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
		{Label: "关于", Click: func(*mygo.MenuItem, *mygo.Window) { a.showAbout() }},
		mygo.Separator(),
		{Label: "退出", Role: mygo.RoleQuit, Accelerator: "CmdOrCtrl+Q"},
	})
	tray, err := mygo.NewTray(mygo.TrayOptions{
		ID: "glide-main", Icon: trayIcon(), IconIsTemplate: trayIconIsTemplate(), ToolTip: "Glide · 划词翻译",
	})
	if err != nil {
		return nil, err
	}
	t.tray = tray
	tray.OnClick(func() {
		a.mu.RLock()
		showSettings := a.settings.TrayClickShowsPanel
		a.mu.RUnlock()
		if showSettings {
			a.ShowMain()
		}
	})
	// 不绑定原生托盘菜单，确保 macOS 左键事件到达 OnClick；仅右键主动弹出菜单。
	tray.OnRightClick(func() {
		t.selectionSource = captureTraySelectionSource()
		t.menu.Popup(nil)
	})
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
		if shortcut != t.shortcut {
			t.shortcut = shortcut
			t.selectionItem.SetAccelerator(shortcut)
		}
		if label := a.updateMenuLabel(); label != t.updateItem.Label {
			t.updateItem.SetLabel(label)
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

// configureApplicationMenu 为 macOS 菜单栏提供中文系统操作和应用设置入口。
func (a *GlideService) configureApplicationMenu() {
	if runtime.GOOS != "darwin" {
		return
	}
	mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
		{Label: "Glide", Submenu: []*mygo.MenuItem{
			{Label: "关于 Glide", Click: func(*mygo.MenuItem, *mygo.Window) { a.showAbout() }},
			{Label: "检查更新…", Disabled: mygo.IsDev(), Click: func(*mygo.MenuItem, *mygo.Window) { a.CheckForUpdates() }},
			mygo.Separator(),
			{Label: "偏好设置…", Accelerator: "CmdOrCtrl+,", Click: func(*mygo.MenuItem, *mygo.Window) { a.ShowMain() }},
			mygo.Separator(),
			{Label: "服务", Role: mygo.RoleServices},
			mygo.Separator(),
			{Label: "隐藏 Glide", Role: mygo.RoleHide},
			{Label: "隐藏其他", Role: mygo.RoleHideOthers},
			{Label: "全部显示", Role: mygo.RoleUnhide},
			mygo.Separator(),
			{Label: "退出 Glide", Role: mygo.RoleQuit},
		}},
		{Label: "文件", Role: mygo.RoleFileMenu},
		{Label: "编辑", Role: mygo.RoleEditMenu},
		{Label: "显示", Role: mygo.RoleViewMenu},
		{Label: "窗口", Role: mygo.RoleWindowMenu},
		{Label: "帮助", Role: mygo.RoleHelp, Submenu: []*mygo.MenuItem{
			{Label: "Glide 使用说明", Click: func(*mygo.MenuItem, *mygo.Window) { a.showUsageHelp() }},
		}},
	}))
}

// showAbout 打开独立的关于窗口，不改变偏好设置的当前页面。
func (a *GlideService) showAbout() {
	if runtime.GOOS == "darwin" {
		mygo.App.ShowAboutPanel(mygo.AboutPanelOptions{
			ApplicationName: "Glide", ApplicationVersion: mygo.App.Version(), Credits: "原生桌面划词翻译工具",
		})
		return
	}
	a.showAppMessage("关于 Glide", "Glide "+mygo.App.Version(), "原生桌面划词翻译工具")
}

// showAppMessage 显示独立消息弹窗，失败时记录日志。
func (a *GlideService) showAppMessage(title, message, detail string) {
	go func() {
		_, err := mygo.Dialog.Message(mygo.MessageOptions{
			Type: mygo.MessageInfo, Title: title, Message: message, Detail: detail, Buttons: []string{"好"},
		})
		if err != nil {
			log.Printf("打开%s失败：%v", title, err)
		}
	}()
}
