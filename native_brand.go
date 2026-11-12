package main

import (
	"embed"
	"runtime"

	"github.com/egoist/mygo/ui"
)

//go:embed assets/brand/logo.png assets/brand/tray-template.png
var brandAssets embed.FS

var brandLogo = func() ui.ImageSource {
	data, _ := brandAssets.ReadFile("assets/brand/logo.png")
	image, err := ui.DecodeImage(data)
	if err != nil {
		panic("无法读取内置 Glide 品牌图标：" + err.Error())
	}
	return image
}()

// pnBrandLogo 在界面中显示带圆角的原始品牌图案，尺寸使用逻辑像素。
func pnBrandLogo(c *ui.Context, size float32) ui.Element {
	return ui.Image(c, brandLogo).Size(size, size).Radius(size * .22).Clip().Label("Glide 标志")
}

// trayIcon 为 macOS 提供 Retina 单色模板，为 Windows 提供彩色圆角图案。
func trayIcon() []byte {
	path := "assets/brand/logo.png"
	if trayIconIsTemplate() {
		path = "assets/brand/tray-template.png"
	}
	data, _ := brandAssets.ReadFile(path)
	return data
}

// trayIconIsTemplate 只在 macOS 使用自动适配菜单栏明暗背景的模板图。
func trayIconIsTemplate() bool { return runtime.GOOS == "darwin" }
