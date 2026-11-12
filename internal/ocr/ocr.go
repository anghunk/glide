// Package ocr 使用操作系统离线识别图片，截图临时文件由调用方负责删除。
package ocr

import (
	"context"
	"errors"
	"fmt"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"strings"
	"unicode/utf8"
)

var ErrCanceled = errors.New("已取消截图")

// Recognize 校验图片大小后调用系统 OCR；不发送图片到网络。
func Recognize(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("无法打开图片：%w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if info.Size() > 32<<20 {
		return "", fmt.Errorf("图片不能超过 32 MB")
	}
	config, _, err := image.DecodeConfig(f)
	if err != nil {
		return "", fmt.Errorf("无法读取图片，请使用 PNG、JPEG、BMP 或 TIFF")
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 40000000 {
		return "", fmt.Errorf("图片尺寸过大，请裁剪后重试（最多 4000 万像素）")
	}
	text, err := recognizeNative(ctx, path)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("未识别到文字，请选择更清晰的图片或重新截图")
	}
	if utf8.RuneCountInString(text) > 10000 {
		return "", fmt.Errorf("识别文字超过 10,000 字，请缩小截图范围后重试")
	}
	return text, nil
}
