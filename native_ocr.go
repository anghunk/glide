package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
	"glide/internal/ocr"
	"glide/internal/translation"
)

// ocrRunning 返回识别忙碌状态，避免重复截图和识别。
func (a *GlideService) ocrRunning() bool { a.mu.RLock(); defer a.mu.RUnlock(); return a.ocrBusy }

// startOCR 从图片或框选截图提取文字，完成后复用多渠道浮窗翻译。
func (a *GlideService) startOCR(screenshot bool) {
	a.mu.Lock()
	if a.ocrBusy || a.quitting {
		a.mu.Unlock()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	a.ocrBusy, a.ocrCancel = true, cancel
	a.mu.Unlock()
	a.notifySettings()
	go func() {
		defer func() { cancel(); a.mu.Lock(); a.ocrBusy, a.ocrCancel = false, nil; a.mu.Unlock(); a.notifySettings() }()
		text, err := a.readOCR(ctx, screenshot)
		if errors.Is(err, ocr.ErrCanceled) || errors.Is(err, context.Canceled) {
			return
		}
		a.mu.RLock()
		quitting := a.quitting
		a.mu.RUnlock()
		if quitting {
			return
		}
		if err != nil {
			a.showPanelError(err.Error())
			return
		}
		a.showTranslation(text)
	}()
}

// readOCR 保持本应用窗口可见状态，截图或选择图片后识别文字；临时截图始终在识别后删除。
func (a *GlideService) readOCR(ctx context.Context, screenshot bool) (string, error) {
	a.mu.RLock()
	provider, err := ocr.ActiveConfig(a.settings.OCRProviders)
	ctx = translation.WithProxy(ctx, a.settings.UseSystemProxy, a.settings.ProxyAddress)
	a.mu.RUnlock()
	if err != nil {
		return "", errors.New(tr(err.Error()))
	}
	if err := ocr.ValidateProvider(provider); err != nil {
		return "", err
	}
	var path string
	if screenshot {
		dir, err := os.MkdirTemp("", "glide-ocr-*")
		if err != nil {
			return "", err
		}
		defer os.RemoveAll(dir)
		path = filepath.Join(dir, "capture.png")
		if err := ocr.Capture(ctx, path); err != nil {
			return "", err
		}
	} else {
		parent := a.mainWindow
		if !parent.IsVisible() {
			parent = nil
		}
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: parent, Title: tr("选择图片并识别翻译"), ButtonLabel: tr("打开"), Filters: []mygo.FileFilter{{Name: tr("图片"), Extensions: []string{"png", "jpg", "jpeg", "bmp", "tif", "tiff"}}}})
		if err != nil {
			return "", err
		}
		if len(paths) == 0 {
			return "", ocr.ErrCanceled
		}
		path = paths[0]
	}
	a.mu.Lock()
	if a.quitting || ctx.Err() != nil {
		a.mu.Unlock()
		return "", ctx.Err()
	}
	if a.panelCancel != nil {
		a.panelCancel()
		a.panelCancel = nil
	}
	a.panelGeneration++
	generation := a.panelGeneration
	a.panel = PanelState{Loading: true, Pinned: a.panel.Pinned}
	a.mu.Unlock()
	a.positionPanel()
	if !screenshot && a.mainWindow.IsVisible() {
		a.panelWindow.Show()
	}
	a.notifyPanel()
	text, err := ocr.RecognizeWithProvider(ctx, provider, path)
	a.mu.Lock()
	stale := generation != a.panelGeneration
	if stale && a.panelCancel == nil && a.panel.Response == nil {
		a.panel.Loading = false
	}
	a.mu.Unlock()
	if stale {
		a.notifyPanel()
		return "", ocr.ErrCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "", errors.New(tr("截图或文字识别超时，请缩小范围后重试"))
	}
	return text, err
}
