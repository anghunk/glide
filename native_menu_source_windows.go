//go:build windows

package main

import (
	"errors"
	"os"
	"syscall"
	"time"
	"unsafe"
)

var trayUser32 = syscall.NewLazyDLL("user32.dll")

// captureTraySelectionSource 记录 MyGo 展示菜单之前的前台窗口，恢复时校验窗口与进程身份。
// 它不模拟复制；若用户点击托盘时前台已是系统任务栏，则要求改用快捷键取词。
func captureTraySelectionSource() func() error {
	getForeground := trayUser32.NewProc("GetForegroundWindow")
	getPID := trayUser32.NewProc("GetWindowThreadProcessId")
	isWindow := trayUser32.NewProc("IsWindow")
	getClass := trayUser32.NewProc("GetClassNameW")
	setForeground := trayUser32.NewProc("SetForegroundWindow")
	window, _, _ := getForeground.Call()
	var pid uint32
	getPID.Call(window, uintptr(unsafe.Pointer(&pid)))
	var class [128]uint16
	getClass.Call(window, uintptr(unsafe.Pointer(&class[0])), uintptr(len(class)))
	name := syscall.UTF16ToString(class[:])
	valid := window != 0 && pid != 0 && pid != uint32(os.Getpid()) && name != "Shell_TrayWnd" && name != "Shell_SecondaryTrayWnd" && name != "NotifyIconOverflowWindow"
	return func() error {
		if !valid {
			return errors.New(tr("没有找到原应用。请回到原应用选中文字，再使用翻译快捷键。"))
		}
		stillValid := func() bool {
			alive, _, _ := isWindow.Call(window)
			var currentPID uint32
			getPID.Call(window, uintptr(unsafe.Pointer(&currentPID)))
			return alive != 0 && currentPID == pid
		}
		if !stillValid() {
			return errors.New(tr("原应用窗口已关闭，请重新选中文字。"))
		}
		setForeground.Call(window)
		// 跨输入队列的激活可能异步完成；仅在后台有界等待，不占用菜单所在的主线程。
		deadline := time.NewTimer(500 * time.Millisecond)
		defer deadline.Stop()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			if !stillValid() {
				return errors.New(tr("原应用窗口已关闭，请重新选中文字。"))
			}
			current, _, _ := getForeground.Call()
			if current == window {
				return nil
			}
			select {
			case <-deadline.C:
				return errors.New(tr("无法返回原应用。请在原应用中使用翻译快捷键。"))
			case <-ticker.C:
			}
		}
	}
}
