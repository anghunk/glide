//go:build windows

package selection

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

var (
	user32                   = syscall.NewLazyDLL("user32.dll")
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	ole32                    = syscall.NewLazyDLL("ole32.dll")
	oleaut32                 = syscall.NewLazyDLL("oleaut32.dll")
	getForegroundWindow      = user32.NewProc("GetForegroundWindow")
	getWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")
	getCursorPos             = user32.NewProc("GetCursorPos")
	setWindowsHookEx         = user32.NewProc("SetWindowsHookExW")
	unhookWindowsHookEx      = user32.NewProc("UnhookWindowsHookEx")
	callNextHookEx           = user32.NewProc("CallNextHookEx")
	getMessage               = user32.NewProc("GetMessageW")
	peekMessage              = user32.NewProc("PeekMessageW")
	postThreadMessage        = user32.NewProc("PostThreadMessageW")
	getDoubleClickTime       = user32.NewProc("GetDoubleClickTime")
	getCurrentThreadID       = kernel32.NewProc("GetCurrentThreadId")
	getModuleHandle          = kernel32.NewProc("GetModuleHandleW")
	coInitializeEx           = ole32.NewProc("CoInitializeEx")
	coUninitialize           = ole32.NewProc("CoUninitialize")
	coCreateInstance         = ole32.NewProc("CoCreateInstance")
	sysStringLen             = oleaut32.NewProc("SysStringLen")
	sysFreeString            = oleaut32.NewProc("SysFreeString")
)

type guid struct {
	Data1        uint32
	Data2, Data3 uint16
	Data4        [8]byte
}

var (
	clsidAutomation  = guid{0xff48dba4, 0x60ef, 0x4201, [8]byte{0xaa, 0x87, 0x54, 0x10, 0x3e, 0xef, 0x59, 0x4e}}
	iidAutomation    = guid{0x30cbe57d, 0xd9d0, 0x452a, [8]byte{0xab, 0x13, 0x7a, 0xc5, 0xac, 0x48, 0x25, 0xee}}
	clsidAutomation8 = guid{0xe22ad333, 0xb25f, 0x460c, [8]byte{0x83, 0xd0, 0x05, 0x81, 0x10, 0x73, 0x95, 0xc9}}
	iidAutomation2   = guid{0x34723aff, 0x0c9d, 0x49d0, [8]byte{0x98, 0x96, 0x7a, 0xb5, 0x2d, 0xf8, 0xcd, 0x8a}}
	iidTextPattern   = guid{0x32eba289, 0x3583, 0x42c9, [8]byte{0x9c, 0x59, 0x3b, 0x6d, 0x9a, 0x1e, 0x9b, 0x6a}}
)

type winPoint struct{ X, Y int32 }

// Status 返回系统 UI Automation 能力；高权限窗口与自绘控件仍可能拒绝选区读取。
func Status() Capability {
	return Capability{Supported: true, AutomaticSupported: true, PermissionGranted: true,
		Message: "支持 UI Automation 取词；部分应用需开启辅助功能支持。高权限窗口或图片文字可使用复制翻译。"}
}

// OpenPermissionSettings 在 Windows 上无需申请全局辅助功能授权。
func OpenPermissionSettings() error { return nil }

// comObject 保留系统返回的原生指针类型，避免把 COM 地址当作 Go 整数解引用。
// 不同接口只访问其实际拥有的方法索引；最大接口 IUIAutomationElement 有 85 项。
type comObject struct{ VTable *[85]uintptr }

// comCall 按 Windows SDK 的接口虚表调用 COM 方法；参数中的指针需在调用期间保持存活。
//
//go:uintptrescapes
func comCall(object *comObject, method int, args ...uintptr) uintptr {
	address := object.VTable[method]
	arguments := append([]uintptr{uintptr(unsafe.Pointer(object))}, args...)
	result, _, _ := syscall.SyscallN(address, arguments...)
	return result
}

// releaseCOM 释放一个已取得引用的 COM 对象。
func releaseCOM(object *comObject) {
	if object != nil {
		comCall(object, 2)
	}
}

func failed(result uintptr) bool { return int32(result) < 0 }

// foreground 返回前台窗口及其进程，以免对应用自身触发反馈循环。
func foreground() (uintptr, uint32) {
	window, _, _ := getForegroundWindow.Call()
	var pid uint32
	getWindowThreadProcessID.Call(window, uintptr(unsafe.Pointer(&pid)))
	return window, pid
}

func readNative() (Selection, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	window, pid := foreground()
	if window == 0 || pid == uint32(os.Getpid()) {
		return Selection{}, ErrNoSelection
	}
	hr, _, _ := coInitializeEx.Call(0, 0) // COINIT_MULTITHREADED
	if failed(hr) {
		return Selection{}, fmt.Errorf("初始化 Windows 取词失败（0x%08X）", uint32(hr))
	}
	defer coUninitialize.Call()
	var automation *comObject
	hr, _, _ = coCreateInstance.Call(uintptr(unsafe.Pointer(&clsidAutomation8)), 0, 1,
		uintptr(unsafe.Pointer(&iidAutomation2)), uintptr(unsafe.Pointer(&automation)))
	if !failed(hr) && automation != nil {
		// IUIAutomation2 的连接与事务超时，避免第三方窗口阻塞取词。
		comCall(automation, 61, 700)
		comCall(automation, 63, 700)
	} else {
		hr, _, _ = coCreateInstance.Call(uintptr(unsafe.Pointer(&clsidAutomation)), 0, 1,
			uintptr(unsafe.Pointer(&iidAutomation)), uintptr(unsafe.Pointer(&automation)))
	}
	if failed(hr) || automation == nil {
		return Selection{}, fmt.Errorf("Windows UI Automation 不可用（0x%08X）", uint32(hr))
	}
	defer releaseCOM(automation)
	var element *comObject
	if failed(comCall(automation, 8, uintptr(unsafe.Pointer(&element)))) || element == nil {
		return Selection{}, ErrNoSelection
	}
	var walker *comObject
	comCall(automation, 14, uintptr(unsafe.Pointer(&walker)))
	defer releaseCOM(walker)
	var text string
	// 文档容器可能持有选区而其子元素持有键盘焦点，只向上查找少量祖先。
	for depth := 0; depth < 6 && element != nil; depth++ {
		var elementPID int32
		comCall(element, 20, uintptr(unsafe.Pointer(&elementPID)))
		if elementPID == int32(os.Getpid()) {
			releaseCOM(element)
			return Selection{}, ErrNoSelection
		}
		text = selectedText(element)
		if strings.TrimSpace(text) != "" {
			releaseCOM(element)
			element = nil
			break
		}
		var parent *comObject
		if walker != nil {
			comCall(walker, 3, uintptr(unsafe.Pointer(element)), uintptr(unsafe.Pointer(&parent)))
		}
		releaseCOM(element)
		element = parent
	}
	releaseCOM(element)
	if current, _ := foreground(); current != window {
		return Selection{}, ErrNoSelection
	}
	if strings.TrimSpace(text) == "" {
		return Selection{}, ErrNoSelection
	}
	var p winPoint
	getCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	return Selection{Text: text, Point: Point{int(p.X), int(p.Y)}}, nil
}

// selectedText 通过 TextPattern.GetSelection 读取文本，不模拟按键或触碰剪贴板。
func selectedText(element *comObject) string {
	var isPassword int32
	if !failed(comCall(element, 35, uintptr(unsafe.Pointer(&isPassword)))) && isPassword != 0 {
		return ""
	}
	var pattern *comObject
	// GetCurrentPatternAs 可直接请求正确的 IUIAutomationTextPattern 接口。
	if failed(comCall(element, 14, 10014, uintptr(unsafe.Pointer(&iidTextPattern)), uintptr(unsafe.Pointer(&pattern)))) || pattern == nil {
		return ""
	}
	defer releaseCOM(pattern)
	var ranges *comObject
	if failed(comCall(pattern, 5, uintptr(unsafe.Pointer(&ranges)))) || ranges == nil {
		return ""
	}
	defer releaseCOM(ranges)
	var length int32
	if failed(comCall(ranges, 3, uintptr(unsafe.Pointer(&length)))) {
		return ""
	}
	if length <= 0 {
		return ""
	}
	parts := make([]string, 0, min(length, 8))
	for i := int32(0); i < length && i < 8; i++ {
		var textRange *comObject
		if failed(comCall(ranges, 4, uintptr(i), uintptr(unsafe.Pointer(&textRange)))) || textRange == nil {
			continue
		}
		var bstr *uint16
		hr := comCall(textRange, 12, 100000, uintptr(unsafe.Pointer(&bstr)))
		releaseCOM(textRange)
		if bstr == nil {
			continue
		}
		if !failed(hr) {
			length, _, _ := sysStringLen.Call(uintptr(unsafe.Pointer(bstr)))
			if length > 0 && length <= 100000 {
				parts = append(parts, string(utf16.Decode(unsafe.Slice(bstr, length))))
			}
		}
		sysFreeString.Call(uintptr(unsafe.Pointer(bstr)))
	}
	return strings.Join(parts, "\n")
}

type mouseHookData struct {
	Point     winPoint
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type winMessage struct {
	Window  uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Point   winPoint
	Private uint32
}

// StartWatching 安装只读鼠标钩子，在拖选或双击后取词，不监听键盘。
// 原生消息循环运行于独立线程；返回的 stop 幂等且非阻塞。
func StartWatching(parent context.Context, callback func(Selection)) (func(), error) {
	if callback == nil {
		return nil, fmt.Errorf("取词回调不能为空")
	}
	ctx, cancel := context.WithCancel(parent)
	type started struct {
		threadID uintptr
		err      error
	}
	ready := make(chan started, 1)
	signals := make(chan Point, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer cancel()
		threadID, _, _ := getCurrentThreadID.Call()
		var msg winMessage
		// 先创建线程消息队列，确保 stop 的 WM_QUIT 不会丢失。
		peekMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0, 0)
		doubleClickMillis, _, _ := getDoubleClickTime.Call()
		var down, lastUp Point
		var lastUpTime time.Time
		var pressed bool
		cb := syscall.NewCallback(func(code int32, wparam uintptr, data *mouseHookData) uintptr {
			if code >= 0 && data != nil {
				if data.Flags&1 == 0 { // 忽略程序注入的鼠标事件。
					point := Point{int(data.Point.X), int(data.Point.Y)}
					if wparam == 0x0201 {
						down = point
						pressed = true
					}
					if wparam == 0x0202 && pressed {
						now := time.Now()
						double := !lastUpTime.IsZero() && now.Sub(lastUpTime) <= time.Duration(doubleClickMillis)*time.Millisecond && !dragged(lastUp, point)
						if dragged(down, point) || double {
							select {
							case signals <- point:
							default:
							}
						}
						lastUp, lastUpTime, pressed = point, now, false
					}
				}
			}
			result, _, _ := callNextHookEx.Call(0, uintptr(code), wparam, uintptr(unsafe.Pointer(data)))
			return result
		})
		module, _, _ := getModuleHandle.Call(0)
		hook, _, err := setWindowsHookEx.Call(14, cb, module, 0) // WH_MOUSE_LL
		if hook == 0 {
			ready <- started{err: fmt.Errorf("无法启动自动划词：%w", err)}
			return
		}
		defer unhookWindowsHookEx.Call(hook)
		ready <- started{threadID: threadID}
		for {
			result, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
			if int32(result) <= 0 {
				break
			}
		}
	}()
	r := <-ready
	if r.err != nil {
		cancel()
		return nil, r.err
	}
	stop := onceStop(func() { cancel(); postThreadMessage.Call(r.threadID, 0x0012, 0, 0) })
	go func() { <-ctx.Done(); stop() }()
	go watchSelections(ctx, signals, callback)
	return stop, nil
}
