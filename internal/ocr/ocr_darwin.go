//go:build darwin

package ocr

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

var vision struct {
	once sync.Once
	err  error
}

// loadVision 加载系统框架，无需安装额外 OCR 引擎或开发工具。
func loadVision() error {
	vision.once.Do(func() {
		for _, name := range []string{"Foundation", "Vision"} {
			_, err := purego.Dlopen("/System/Library/Frameworks/"+name+".framework/"+name, purego.RTLD_NOW|purego.RTLD_GLOBAL)
			if err != nil {
				vision.err = fmt.Errorf("无法加载 macOS OCR：%w", err)
				return
			}
		}
	})
	return vision.err
}

// Capture 使用系统交互截图，Esc 取消；路径由调用方提供且不经过剪贴板。
func Capture(ctx context.Context, path string) error {
	command := exec.CommandContext(ctx, "/usr/sbin/screencapture", "-i", "-x", "-t", "png", path)
	err := command.Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if info, statErr := os.Stat(path); statErr == nil && info.Size() > 0 {
		return nil
	}
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
			app, loadErr := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
			if loadErr == nil {
				var allowed func() bool
				purego.RegisterLibFunc(&allowed, app, "CGPreflightScreenCaptureAccess")
				if allowed() {
					return ErrCanceled
				}
			}
		}
		return fmt.Errorf("截图失败；首次使用请在系统设置的“屏幕与系统音频录制”中允许 Glide 后重试")
	}
	return ErrCanceled
}

// recognizeNative 使用 Vision 准确模式提取文字并保留行分隔。
func recognizeNative(ctx context.Context, path string) (string, error) {
	if err := loadVision(); err != nil {
		return "", err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	send := func(id objc.ID, name string, args ...any) objc.ID { return id.Send(objc.RegisterName(name), args...) }
	pool := send(objc.ID(objc.GetClass("NSAutoreleasePool")), "new")
	defer send(pool, "drain")
	nsPath := send(objc.ID(objc.GetClass("NSString")), "stringWithUTF8String:", path)
	imageURL := send(objc.ID(objc.GetClass("NSURL")), "fileURLWithPath:", nsPath)
	options := send(objc.ID(objc.GetClass("NSDictionary")), "dictionary")
	handler := send(objc.ID(objc.GetClass("VNImageRequestHandler")), "alloc")
	handler = send(handler, "initWithURL:options:", imageURL, options)
	if handler == 0 {
		return "", fmt.Errorf("无法创建图片识别请求")
	}
	defer send(handler, "release")
	request := send(objc.ID(objc.GetClass("VNRecognizeTextRequest")), "new")
	if request == 0 {
		return "", fmt.Errorf("当前 macOS 不支持文字识别，请升级系统")
	}
	defer send(request, "release")
	send(request, "setRecognitionLevel:", uintptr(0)) // VNRequestTextRecognitionLevelAccurate
	send(request, "setUsesLanguageCorrection:", true)
	done := make(chan struct{})
	stopCancel := context.AfterFunc(ctx, func() { send(request, "cancel"); close(done) })
	defer func() {
		if !stopCancel() {
			<-done
		}
	}()
	if send(request, "respondsToSelector:", objc.RegisterName("setAutomaticallyDetectsLanguage:")) != 0 {
		send(request, "setAutomaticallyDetectsLanguage:", true)
	}
	// 优先识别中英文，同时保留系统的自动语种检测。
	languages := send(objc.ID(objc.GetClass("NSMutableArray")), "array")
	for _, language := range []string{"zh-Hans", "en-US"} {
		send(languages, "addObject:", send(objc.ID(objc.GetClass("NSString")), "stringWithUTF8String:", language))
	}
	send(request, "setRecognitionLanguages:", languages)
	requests := send(objc.ID(objc.GetClass("NSArray")), "arrayWithObject:", request)
	var nativeError objc.ID
	success := send(handler, "performRequests:error:", requests, &nativeError)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if success == 0 {
		message := "系统识别失败"
		if nativeError != 0 {
			message = objc.Send[string](send(nativeError, "localizedDescription"), objc.RegisterName("UTF8String"))
		}
		return "", fmt.Errorf("OCR 识别失败：%s", message)
	}
	results := send(request, "results")
	lines := []string{}
	for i, count := uintptr(0), uintptr(send(results, "count")); i < count; i++ {
		observation := send(results, "objectAtIndex:", i)
		candidates := send(observation, "topCandidates:", uintptr(1))
		if send(candidates, "count") == 0 {
			continue
		}
		text := send(send(candidates, "objectAtIndex:", uintptr(0)), "string")
		lines = append(lines, objc.Send[string](text, objc.RegisterName("UTF8String")))
	}
	return strings.Join(lines, "\n"), nil
}
