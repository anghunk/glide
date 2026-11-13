//go:build windows

package ocr

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"io"
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
		stage := "Windows OCR 识别失败"
		if mode == "capture" {
			stage = "Windows 截图失败"
		}
		detail := err.Error()
		if exit, ok := err.(*exec.ExitError); ok {
			if message := windowsErrorMessage(exit.Stderr); message != "" {
				detail = message
			}
		}
		return "", fmt.Errorf("%s：%s", stage, detail)
	}
	return strings.TrimPrefix(string(output), "\ufeff"), nil
}

// windowsErrorMessage 提取 PowerShell 标准错误；编码命令的错误通常包装在 CLIXML 中。
func windowsErrorMessage(data []byte) string {
	message := strings.TrimSpace(strings.TrimPrefix(string(data), "\ufeff"))
	if strings.HasPrefix(message, "#< CLIXML") {
		decoder := xml.NewDecoder(strings.NewReader(strings.TrimSpace(strings.TrimPrefix(message, "#< CLIXML"))))
		var lines []string
		for {
			token, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				break
			}
			start, ok := token.(xml.StartElement)
			if !ok || start.Name.Local != "S" {
				continue
			}
			for _, attr := range start.Attr {
				if attr.Name.Local == "S" && attr.Value == "Error" {
					var line string
					if decoder.DecodeElement(&line, &start) == nil {
						lines = append(lines, line)
					}
					break
				}
			}
		}
		if len(lines) > 0 {
			message = strings.Join(lines, "")
		}
	}
	// PowerShell 用转义表示换行，恢复后方便在错误浮窗中阅读。
	message = strings.NewReplacer("_x000D_", "\r", "_x000A_", "\n").Replace(message)
	return strings.TrimSpace(message)
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
