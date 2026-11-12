//go:build darwin

package selection

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"

	"github.com/ebitengine/purego"
)

type cgPoint struct{ X, Y float64 }

type macAPI struct {
	trusted        func() bool
	axSystem       func() uintptr
	axCopy         func(uintptr, uintptr, *uintptr) int32
	axPID          func(uintptr, *int32) int32
	axTimeout      func(uintptr, float32) int32
	stringCreate   func(uintptr, string, uint32) uintptr
	stringLength   func(uintptr) int64
	stringMaxSize  func(int64, uint32) int64
	stringCString  func(uintptr, []byte, int64, uint32) bool
	stringTypeID   func() uintptr
	typeID         func(uintptr) uintptr
	release        func(uintptr)
	eventCreate    func(uintptr) uintptr
	eventLocation  func(uintptr) cgPoint
	eventInteger   func(uintptr, uint32) int64
	tapCreate      func(uint32, uint32, uint32, uint64, uintptr, uintptr) uintptr
	tapEnable      func(uintptr, bool)
	portSource     func(uintptr, uintptr, int64) uintptr
	runLoopCurrent func() uintptr
	runLoopAdd     func(uintptr, uintptr, uintptr)
	runLoopRunMode func(uintptr, float64, bool) int32
	runLoopStop    func(uintptr)
	mode           uintptr
}

var mac struct {
	once sync.Once
	api  macAPI
	err  error
}

// loadMac 只加载一次系统框架；所有函数均通过纯 Go 调用，不需要 cgo。
func loadMac() (*macAPI, error) {
	mac.once.Do(func() {
		defer func() {
			if r := recover(); r != nil {
				mac.err = fmt.Errorf("加载 macOS 取词接口失败：%v", r)
			}
		}()
		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			mac.err = err
			return
		}
		app, err := purego.Dlopen("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			mac.err = err
			return
		}
		a := &mac.api
		purego.RegisterLibFunc(&a.trusted, app, "AXIsProcessTrusted")
		purego.RegisterLibFunc(&a.axSystem, app, "AXUIElementCreateSystemWide")
		purego.RegisterLibFunc(&a.axCopy, app, "AXUIElementCopyAttributeValue")
		purego.RegisterLibFunc(&a.axPID, app, "AXUIElementGetPid")
		purego.RegisterLibFunc(&a.axTimeout, app, "AXUIElementSetMessagingTimeout")
		purego.RegisterLibFunc(&a.stringCreate, cf, "CFStringCreateWithCString")
		purego.RegisterLibFunc(&a.stringLength, cf, "CFStringGetLength")
		purego.RegisterLibFunc(&a.stringMaxSize, cf, "CFStringGetMaximumSizeForEncoding")
		purego.RegisterLibFunc(&a.stringCString, cf, "CFStringGetCString")
		purego.RegisterLibFunc(&a.stringTypeID, cf, "CFStringGetTypeID")
		purego.RegisterLibFunc(&a.typeID, cf, "CFGetTypeID")
		purego.RegisterLibFunc(&a.release, cf, "CFRelease")
		purego.RegisterLibFunc(&a.eventCreate, app, "CGEventCreate")
		purego.RegisterLibFunc(&a.eventLocation, app, "CGEventGetLocation")
		purego.RegisterLibFunc(&a.eventInteger, app, "CGEventGetIntegerValueField")
		purego.RegisterLibFunc(&a.tapCreate, app, "CGEventTapCreate")
		purego.RegisterLibFunc(&a.tapEnable, app, "CGEventTapEnable")
		purego.RegisterLibFunc(&a.portSource, cf, "CFMachPortCreateRunLoopSource")
		purego.RegisterLibFunc(&a.runLoopCurrent, cf, "CFRunLoopGetCurrent")
		purego.RegisterLibFunc(&a.runLoopAdd, cf, "CFRunLoopAddSource")
		purego.RegisterLibFunc(&a.runLoopRunMode, cf, "CFRunLoopRunInMode")
		purego.RegisterLibFunc(&a.runLoopStop, cf, "CFRunLoopStop")
		// RunLoop 按 CFString 值比较模式名；创建同值字符串，避免解引用动态库的数据符号。
		// 此模式由全局 API 持有，生命周期与进程相同。
		a.mode = a.stringCreate(0, "kCFRunLoopDefaultMode", 0x08000100)
		if a.mode == 0 {
			mac.err = fmt.Errorf("无法创建 macOS 事件循环模式")
		}
	})
	return &mac.api, mac.err
}

// Status 检查辅助功能权限而不主动弹出授权提示。
func Status() Capability {
	a, err := loadMac()
	if err != nil {
		return Capability{Message: err.Error()}
	}
	granted := a.trusted()
	message := "已支持辅助功能取词；部分应用或图片文字需使用复制翻译。"
	if !granted {
		message = "需要在系统设置 → 隐私与安全性 → 辅助功能中开启 Glide。自动划词可能还需输入监控权限。"
	}
	return Capability{Supported: true, AutomaticSupported: true, PermissionGranted: granted, Message: message}
}

// OpenPermissionSettings 打开辅助功能授权页，由用户决定是否授权。
func OpenPermissionSettings() error {
	return exec.Command("open", "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility").Run()
}

func readNative() (Selection, error) {
	a, err := loadMac()
	if err != nil {
		return Selection{}, err
	}
	if !a.trusted() {
		return Selection{}, ErrPermission
	}
	system := a.axSystem()
	if system == 0 {
		return Selection{}, ErrNoSelection
	}
	defer a.release(system)
	a.axTimeout(system, 0.6)
	focusedKey := a.stringCreate(0, "AXFocusedUIElement", 0x08000100)
	defer a.release(focusedKey)
	var focused uintptr
	if a.axCopy(system, focusedKey, &focused) != 0 || focused == 0 {
		return Selection{}, ErrNoSelection
	}
	defer a.release(focused)
	var pid int32
	if a.axPID(focused, &pid) == 0 && pid == int32(os.Getpid()) {
		return Selection{}, ErrNoSelection
	}
	a.axTimeout(focused, 0.6)
	selectedKey := a.stringCreate(0, "AXSelectedText", 0x08000100)
	defer a.release(selectedKey)
	var selected uintptr
	if a.axCopy(focused, selectedKey, &selected) != 0 || selected == 0 {
		return Selection{}, ErrNoSelection
	}
	defer a.release(selected)
	if a.typeID(selected) != a.stringTypeID() {
		return Selection{}, ErrNoSelection
	}
	length := a.stringLength(selected)
	if length == 0 {
		return Selection{}, ErrNoSelection
	}
	if length > 100000 {
		return Selection{}, fmt.Errorf("选中文字过长，请缩小选区后重试")
	}
	buffer := make([]byte, a.stringMaxSize(length, 0x08000100)+1)
	if !a.stringCString(selected, buffer, int64(len(buffer)), 0x08000100) {
		return Selection{}, ErrNoSelection
	}
	end := 0
	for end < len(buffer) && buffer[end] != 0 {
		end++
	}
	s := Selection{Text: string(buffer[:end])}
	if event := a.eventCreate(0); event != 0 {
		p := a.eventLocation(event)
		s.Point = Point{int(p.X), int(p.Y)}
		a.release(event)
	}
	return s, nil
}

// StartWatching 只监听鼠标拖选和双击，不监听键盘、不修改或阻断系统输入。
// 内部使用独立 OS 线程和 CFRunLoop，可从任意 goroutine 调用；stop 幂等且非阻塞。
func StartWatching(parent context.Context, callback func(Selection)) (func(), error) {
	if callback == nil {
		return nil, fmt.Errorf("取词回调不能为空")
	}
	a, err := loadMac()
	if err != nil {
		return nil, err
	}
	if !a.trusted() {
		return nil, ErrPermission
	}
	ctx, cancel := context.WithCancel(parent)
	type started struct {
		runloop uintptr
		err     error
	}
	ready := make(chan started, 1)
	signals := make(chan Point, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer cancel()
		var down Point
		var pressed bool
		var tap uintptr
		cb := purego.NewCallback(func(proxy uintptr, kind uint32, event uintptr, refcon uintptr) uintptr {
			if kind == 0xfffffffe || kind == 0xffffffff {
				if tap != 0 {
					a.tapEnable(tap, true)
				}
				return event
			}
			p := a.eventLocation(event)
			point := Point{int(p.X), int(p.Y)}
			if kind == 1 {
				down = point
				pressed = true
			}
			if kind == 2 && pressed {
				pressed = false
				if dragged(down, point) || a.eventInteger(event, 1) >= 2 {
					select {
					case signals <- point:
					default:
					}
				}
			}
			return event
		})
		// kCGSessionEventTap + kCGEventTapOptionListenOnly: 仅观察鼠标按下和松开。
		tap = a.tapCreate(1, 0, 1, (1<<1)|(1<<2), cb, 0)
		if tap == 0 {
			ready <- started{err: fmt.Errorf("无法监听划词，请在系统设置中授予辅助功能和输入监控权限后重启 Glide")}
			return
		}
		defer a.release(tap)
		source := a.portSource(0, tap, 0)
		if source == 0 {
			ready <- started{err: fmt.Errorf("无法创建鼠标事件监听")}
			return
		}
		defer a.release(source)
		loop := a.runLoopCurrent()
		a.runLoopAdd(loop, source, a.mode)
		a.tapEnable(tap, true)
		ready <- started{runloop: loop}
		for ctx.Err() == nil {
			if a.runLoopRunMode(a.mode, 0.5, false) == 1 {
				break
			}
		}
		a.tapEnable(tap, false)
	}()
	r := <-ready
	if r.err != nil {
		cancel()
		return nil, r.err
	}
	stop := onceStop(func() { cancel(); a.runLoopStop(r.runloop) })
	go func() { <-ctx.Done(); stop() }()
	go watchSelections(ctx, signals, callback)
	return stop, nil
}
