//go:build windows

package ocr

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unicode/utf16"
)

//go:embed windows.ps1
var windowsScript string

// runWindows 使用系统 PowerShell 和 WinRT，参数通过环境变量传入而非拼接脚本。
func runWindows(ctx context.Context, mode, path string) (string, error) {
	units := utf16.Encode([]rune(windowsScript))
	encoded := make([]byte, len(units)*2)
	for i, unit := range units {
		binary.LittleEndian.PutUint16(encoded[i*2:], unit)
	}
	command := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-STA", "-EncodedCommand", base64.StdEncoding.EncodeToString(encoded))
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	command.Env = append(os.Environ(), "GLIDE_OCR_MODE="+mode, "GLIDE_OCR_PATH="+path)
	output, err := command.Output()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 2 {
			return "", ErrCanceled
		}
		return "", fmt.Errorf("Windows OCR 失败，请确认系统已安装所需语言的 OCR 功能，并使用支持的图片格式")
	}
	return strings.TrimPrefix(string(output), "\ufeff"), nil
}

// Capture 打开本机框选工具，取消时不触碰剪贴板。
func Capture(ctx context.Context, path string) error {
	_, err := runWindows(ctx, "capture", path)
	return err
}

// recognizeNative 使用 Windows 的用户语言 OCR 引擎提取文字。
func recognizeNative(ctx context.Context, path string) (string, error) {
	return runWindows(ctx, "recognize", path)
}
