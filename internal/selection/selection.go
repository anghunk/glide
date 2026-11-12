// Package selection 读取外部应用暴露的选区，并按需监听鼠标划词。
package selection

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

var (
	ErrNoSelection = errors.New("未读取到选中文字；请先选中文字，或复制后使用剪贴板翻译")
	ErrPermission  = errors.New("请先在系统设置中授予 Glide 辅助功能权限")
	ErrUnsupported = errors.New("当前系统暂不支持跨应用取词，请使用剪贴板翻译")
)

// Point 是原生桌面坐标；Windows 使用物理像素，macOS 使用点。
type Point struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// Selection 是外部应用提供的当前文本选区，不包含剪贴板内容。
type Selection struct {
	Text  string `json:"text"`
	Point Point  `json:"point"`
}

// Capability 描述平台能力与授权状态，不能保证所有第三方控件都暴露选区。
type Capability struct {
	Supported          bool   `json:"supported"`
	AutomaticSupported bool   `json:"automaticSupported"`
	PermissionGranted  bool   `json:"permissionGranted"`
	Message            string `json:"message"`
}

// readSlot 防止一个无响应的第三方辅助功能提供程序累积无限原生调用。
var readSlot = make(chan struct{}, 1)

// Read 读取前台应用选区；上下文取消后立即返回，原生调用会在后台释放资源。
// 调用方应在显示自己的窗口前读取，避免前台焦点改变。
func Read(ctx context.Context) (Selection, error) {
	select {
	case readSlot <- struct{}{}:
	case <-ctx.Done():
		return Selection{}, ctx.Err()
	}
	type response struct {
		selection Selection
		err       error
	}
	result := make(chan response, 1)
	go func() {
		defer func() { <-readSlot }()
		s, err := readNative()
		s.Text = strings.TrimSpace(s.Text)
		if err == nil && s.Text == "" {
			err = ErrNoSelection
		}
		result <- response{s, err}
	}()
	select {
	case r := <-result:
		return r.selection, r.err
	case <-ctx.Done():
		return Selection{}, ctx.Err()
	}
}

// watchSelections 将鼠标松开信号合并后读取；不在原生鼠标回调中访问外部应用。
func watchSelections(ctx context.Context, signals <-chan Point, callback func(Selection)) {
	var timer *time.Timer
	var timerC <-chan time.Time
	var point Point
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case point = <-signals:
			if timer == nil {
				timer = time.NewTimer(130 * time.Millisecond)
			} else {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(130 * time.Millisecond)
			}
			timerC = timer.C
		case <-timerC:
			timerC = nil
			readCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
			s, err := Read(readCtx)
			cancel()
			if err == nil && ctx.Err() == nil {
				s.Point = point
				callback(s)
			}
		}
	}
}

// onceStop 保证切换自动划词开关与退出同时发生时，只释放一次原生监听。
func onceStop(stop func()) func() {
	var once sync.Once
	return func() { once.Do(stop) }
}

// dragged 判断移动是否足以形成划词，忽略普通点击的手部抖动。
func dragged(a, b Point) bool {
	dx, dy := a.X-b.X, a.Y-b.Y
	return dx*dx+dy*dy >= 16
}
