package main

import (
	"context"
	"log"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"glide/internal/selection"
)

// createWindows 预载设置页与紧凑翻译浮窗，启动时仅显示托盘。
func (a *GlideService) createWindows() {
	a.settingsUI = newSettingsUI(a)
	a.panelUI = newPanelUI(a)
	settingsTitleBar := mygo.TitleBarHidden
	if runtime.GOOS == "darwin" {
		settingsTitleBar = mygo.TitleBarHiddenInset
	}
	a.mainWindow = mygo.NewWindow(mygo.WindowOptions{
		Title: tr("Glide 偏好设置"), Content: ui.View(a.settingsUI.view), Width: 840, Height: 620, MinWidth: 780, MinHeight: 540,
		BackgroundColor: "#f6f7f8", StateKey: "preferences-compact-v2", Hidden: true,
		TitleBarStyle: settingsTitleBar, HideWindowButtons: runtime.GOOS != "darwin",
	})
	a.mainWindow.OnShow(func() {
		if runtime.GOOS == "darwin" {
			mygo.App.Dock.Show()
		}
	})
	a.mainWindow.OnHide(func() {
		if runtime.GOOS == "darwin" {
			mygo.App.Dock.Hide()
		}
	})
	a.mainWindow.OnClose(func(e *mygo.CloseEvent) {
		a.mu.RLock()
		quitting := a.quitting
		a.mu.RUnlock()
		if !quitting {
			e.PreventDefault()
			a.HideMain()
		}
	})
	a.panelWindow = mygo.NewWindow(mygo.WindowOptions{
		Title: tr("Glide"), Content: ui.View(a.panelUI.view), Width: 400, Height: 420, MinWidth: 360, MinHeight: 1, MaxWidth: 420,
		Panel: true, Frameless: true, CornerRadius: 12, Hidden: true, BackgroundColor: "#ffffff", DisableMaximize: true,
	})
	a.panelWindow.OnMove(a.notifyPanel)
	a.panelWindow.OnBlur(func() {
		a.mu.RLock()
		pinned, quitting := a.panel.Pinned, a.quitting
		a.mu.RUnlock()
		if !pinned && !quitting {
			a.HidePanel()
		}
	})
	a.panelWindow.OnClose(func(e *mygo.CloseEvent) {
		a.mu.RLock()
		quitting := a.quitting
		a.mu.RUnlock()
		if !quitting {
			e.PreventDefault()
			a.HidePanel()
		}
	})
	trayMenu, err := newNativeTrayMenu(a)
	if err != nil {
		log.Printf("创建托盘失败：%v", err)
		// 托盘不可用时保留设置入口，以免后台应用失去可操作的窗口。
		a.mainWindow.Show()
	} else {
		a.trayMenu = trayMenu
	}
	go a.configureTriggers()
	a.configureApplicationMenu()
	a.startUpdater()
}

// configureTriggers 更新热键及鼠标取词；注册新热键失败时保留旧热键。
func (a *GlideService) configureTriggers() {
	a.triggersMu.Lock()
	defer a.triggersMu.Unlock()
	defer a.notifySettings()
	a.mu.Lock()
	if a.quitting {
		a.mu.Unlock()
		return
	}
	settings := cloneSettings(a.settings)
	if a.watchCancel != nil {
		a.watchCancel()
		a.watchCancel = nil
	}
	if a.watchStop != nil {
		a.watchStop()
		a.watchStop = nil
	}
	a.watchError = ""
	a.mu.Unlock()
	a.configureShortcuts(settings)
	if !settings.AutoSelection {
		return
	}
	cap := selection.Status()
	if !cap.AutomaticSupported || !cap.PermissionGranted {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	stop, err := selection.StartWatching(ctx, func(s selection.Selection) {
		text := strings.TrimSpace(s.Text)
		if text == "" {
			return
		}
		a.mu.Lock()
		if !a.settings.AutoSelection || ctx.Err() != nil || (text == a.lastSelection && time.Since(a.lastSelectionAt) < time.Second) {
			a.mu.Unlock()
			return
		}
		a.lastSelection = text
		a.lastSelectionAt = time.Now()
		a.mu.Unlock()
		a.showTranslation(text)
	})
	a.mu.Lock()
	if a.quitting {
		cancel()
		if stop != nil {
			stop()
		}
	} else if err != nil {
		cancel()
		a.watchError = "自动划词启动失败：" + err.Error()
	} else {
		a.watchCancel = cancel
		a.watchStop = stop
	}
	a.mu.Unlock()
}

// OpenPanel 打开可直接输入原文的浮窗，保留已有译文和编辑内容。
func (a *GlideService) OpenPanel() {
	if a.panelWindow == nil {
		return
	}
	if !a.panelWindow.IsVisible() {
		a.positionPanel()
	}
	a.panelWindow.Show()
	a.focusPanelInput()
	a.notifyPanel()
}

// translateSelection 优先读取当前选区，读取失败或为空时使用此刻的剪贴板文本。
func (a *GlideService) translateSelection() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s, err := selection.Read(ctx)
	if err != nil || strings.TrimSpace(s.Text) == "" {
		s.Text = mygo.Clipboard.ReadText()
	}
	a.showTranslation(s.Text)
}

// positionPanel 按偏好定位并聚焦浮窗，使唤醒后的首次失焦即可收起；固定且可见时保留当前位置。
func (a *GlideService) positionPanel() {
	a.mu.RLock()
	pinned, quitting, followsMouse := a.panel.Pinned, a.quitting, a.settings.PanelFollowsMouse
	a.mu.RUnlock()
	if quitting {
		return
	}
	a.focusPanelInput()
	if pinned && a.panelWindow.IsVisible() {
		a.panelWindow.Show()
		return
	}
	point := mygo.Screen.CursorScreenPoint()
	area := mygo.Screen.DisplayNearestPoint(point).WorkArea
	bounds := a.panelWindow.Bounds()
	width, height := bounds.Width, bounds.Height
	minimum, maximum := panelHeightLimits(area.Height)
	a.panelWindow.SetMinimumSize(min(360, max(1, area.Width-16)), minimum)
	a.panelWindow.SetMaximumSize(min(420, max(1, area.Width-16)), maximum)
	if width > area.Width-16 || height > maximum {
		a.panelWindow.SetSize(min(width, max(1, area.Width-16)), min(height, maximum))
		bounds = a.panelWindow.Bounds()
		width, height = bounds.Width, bounds.Height
	}
	if !followsMouse {
		a.panelWindow.SetPosition(area.X+(area.Width-width)/2, area.Y+(area.Height-height)/2)
		a.panelWindow.Show()
		return
	}
	x, y := point.X+16, point.Y+20
	if x+width > area.X+area.Width-8 {
		x = point.X - width - 16
	}
	if y+height > area.Y+area.Height-8 {
		y = area.Y + area.Height - height - 8
	}
	x = max(area.X+8, min(x, area.X+area.Width-width-8))
	y = max(area.Y+8, min(y, area.Y+area.Height-height-8))
	a.panelWindow.SetPosition(x, y)
	a.panelWindow.Show()
}

// showPanelError 将权限或取词错误显示在同一浮窗中。
func (a *GlideService) showPanelError(message string) {
	a.mu.Lock()
	a.panelGeneration++
	a.cancelPanelRequestsLocked()
	a.panel = PanelState{Error: message, Pinned: a.panel.Pinned}
	a.mu.Unlock()
	a.positionPanel()
	a.notifyPanel()
}

// showTranslation 取消旧浮窗请求并逐渠道发布结果，过期回调不会覆盖新选区。
func (a *GlideService) showTranslation(text string) {
	a.showTranslationRequest(TranslateRequest{Text: text})
}

// showTranslationRequest 保留浮窗内编辑的语言与渠道配置，复用原来的窗口和进度通知。
func (a *GlideService) showTranslationRequest(req TranslateRequest) {
	a.runPanelTranslation(req, true)
}

// runPanelTranslation 在新选区出现时定位浮窗，窗内重译则保留位置与键盘焦点。
func (a *GlideService) runPanelTranslation(req TranslateRequest, reposition bool) {
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" {
		a.showPanelError("剪贴板中没有文字，请复制文字或输入原文后翻译。")
		return
	}
	a.mu.Lock()
	a.cancelPanelRequestsLocked()
	ctx, cancel := context.WithCancel(context.Background())
	a.panelCancel = cancel
	a.panelGeneration++
	generation := a.panelGeneration
	a.panel = PanelState{Loading: true, Pinned: a.panel.Pinned}
	a.mu.Unlock()
	if reposition || !a.panelWindow.IsVisible() {
		a.positionPanel()
	}
	a.notifyPanel()
	go func() {
		response, err := a.translate(ctx, req, func(response TranslationResponse) {
			a.mu.Lock()
			if a.panelGeneration != generation {
				a.mu.Unlock()
				return
			}
			a.updatePanelResponseLocked(response)
			a.mu.Unlock()
			a.notifyPanel()
		})
		a.mu.Lock()
		if a.panelGeneration != generation {
			a.mu.Unlock()
			return
		}
		if response.RequestID != "" {
			a.updatePanelResponseLocked(response)
		} else {
			a.panel.Loading = false
		}
		if err != nil {
			a.panel.Error = err.Error()
		}
		a.mu.Unlock()
		a.notifyPanel()
	}()
}

// shutdown 终止鼠标监听、浮窗翻译和全局快捷键。
func (a *GlideService) shutdown() {
	a.stopUpdater()
	a.mu.Lock()
	a.quitting = true
	if a.ocrCancel != nil {
		a.ocrCancel()
	}
	a.cancelPanelRequestsLocked()
	if a.watchCancel != nil {
		a.watchCancel()
	}
	if a.watchStop != nil {
		a.watchStop()
	}
	a.mu.Unlock()
	mygo.GlobalShortcut.UnregisterAll()
}

// configureShortcuts 整体更新全局快捷键；任一注册失败时恢复旧绑定。
// 调用方须持有 triggersMu。
func (a *GlideService) configureShortcuts(settings Settings) {
	a.mu.RLock()
	previous := map[string]string{"划词翻译": a.hotkey, "输入翻译": a.extraHotkeys["输入翻译"], "剪贴板翻译": a.extraHotkeys["剪贴板翻译"], "截图 OCR 翻译": a.extraHotkeys["截图 OCR 翻译"]}
	a.mu.RUnlock()
	desired := map[string]string{"划词翻译": settings.Shortcut, "输入翻译": settings.InputShortcut, "剪贴板翻译": settings.ClipboardShortcut, "截图 OCR 翻译": settings.OCRShortcut}
	actions := map[string]func(){
		"截图 OCR 翻译": func() { a.startOCR(true) },
		"划词翻译":      func() { go a.translateSelection() },
		"输入翻译":      a.OpenPanel,
		"剪贴板翻译":     func() { a.showTranslation(mygo.Clipboard.ReadText()) },
	}
	unchanged := true
	for name, key := range desired {
		if previous[name] != key {
			unchanged = false
		}
	}
	if unchanged {
		return
	}
	for _, key := range previous {
		if key != "" {
			mygo.GlobalShortcut.Unregister(key)
		}
	}
	registered := []string{}
	for _, name := range []string{"划词翻译", "输入翻译", "剪贴板翻译", "截图 OCR 翻译"} {
		key := desired[name]
		if key == "" {
			continue
		}
		if err := mygo.GlobalShortcut.Register(key, actions[name]); err != nil {
			for _, added := range registered {
				mygo.GlobalShortcut.Unregister(added)
			}
			message := name + "快捷键注册失败：" + err.Error()
			for oldName, oldKey := range previous {
				if oldKey == "" {
					continue
				}
				if restoreErr := mygo.GlobalShortcut.Register(oldKey, actions[oldName]); restoreErr != nil {
					previous[oldName] = ""
					message += "；恢复" + oldName + "失败：" + restoreErr.Error()
				}
			}
			a.mu.Lock()
			a.hotkey, a.extraHotkeys, a.hotkeyError = previous["划词翻译"], previous, message
			a.mu.Unlock()
			log.Print(message)
			return
		}
		registered = append(registered, key)
	}
	a.mu.Lock()
	a.hotkey, a.extraHotkeys, a.hotkeyError = settings.Shortcut, desired, ""
	a.mu.Unlock()
}
