package main

// focusPanelInput 在界面线程请求原文输入框焦点，控件尚未显示时由 Handle 保留请求。
func (a *GlideService) focusPanelInput() {
	if a.panelWindow != nil && a.panelUI != nil {
		a.panelWindow.Update(func() { a.panelUI.inputFocus.Focus() })
	}
}

// notifyPanel 合并后台结果的重绘请求；空闲窗口不轮询、不持续绘制。
func (a *GlideService) notifyPanel() {
	if a.panelWindow != nil {
		a.panelWindow.Invalidate()
	}
}

// notifySettings 在设置改变后刷新原生窗口，浮窗会直接读取最新显示偏好。
func (a *GlideService) notifySettings() {
	a.refreshTrayMenu()
	if a.mainWindow != nil {
		a.mainWindow.Invalidate()
	}
	a.notifyPanel()
}
