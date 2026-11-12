//go:build !darwin && !windows

package ocr

import (
	"context"
	"fmt"
)

// Capture 在不受支持的平台返回明确提示。
func Capture(context.Context, string) error {
	return fmt.Errorf("截图 OCR 暂仅支持 macOS 和 Windows")
}

// recognizeNative 在不受支持的平台不调用远程服务。
func recognizeNative(context.Context, string) (string, error) {
	return "", fmt.Errorf("图片 OCR 暂仅支持 macOS 和 Windows")
}
