// Command brand-assets 从项目原稿生成品牌资源；原始 logo.png 始终保持不变。
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"

	"golang.org/x/image/draw"
)

// main 在项目根目录运行，生成圆角界面图、平台应用图标及高密度托盘图。
func main() {
	if err := generate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// generate 使用同一份原稿派生尺寸，保留蓝色和内部白色文字的原始像素关系。
func generate() error {
	f, err := os.Open("logo.png")
	if err != nil {
		return err
	}
	source, err := png.Decode(f)
	_ = f.Close()
	if err != nil {
		return err
	}
	if err := os.MkdirAll("assets/brand", 0755); err != nil {
		return err
	}
	if err := os.MkdirAll("resources", 0755); err != nil {
		return err
	}
	logo := rounded(source, 1024, 0, 220)
	if err := writePNG("assets/brand/logo.png", logo); err != nil {
		return err
	}
	if err := writePNG("resources/icon.png", logo); err != nil {
		return err
	}
	// 旧版 ICNS 独立保留透明安全边距，与 Finder / Dock 中的视觉尺寸保持协调。
	mac := rounded(source, 1024, 100, 184)
	if err := writePNG("assets/brand/icon-macos.png", mac); err != nil {
		return err
	}
	if err := writeICNS("assets/brand/icon-macos.icns", mac); err != nil {
		return err
	}
	// macOS 模板保留蓝色气泡轮廓与文字镂空，白色背景不进入模板的 alpha。
	mask := image.NewNRGBA(source.Bounds())
	bounds := image.Rectangle{}
	for y := source.Bounds().Min.Y; y < source.Bounds().Max.Y; y++ {
		for x := source.Bounds().Min.X; x < source.Bounds().Max.X; x++ {
			c := color.NRGBAModel.Convert(source.At(x, y)).(color.NRGBA)
			alpha := uint8(math.Round(math.Max(0, math.Min(1, (float64(c.B)-float64(c.R)-6)/216)) * float64(c.A)))
			mask.SetNRGBA(x, y, color.NRGBA{A: alpha})
			if alpha > 12 {
				bounds = bounds.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	if bounds.Empty() {
		return fmt.Errorf("logo.png 中没有可识别的蓝色品牌图案")
	}
	// 40×36px 由 MyGo 显示为 20×18pt，图案充满高度以匹配系统菜单栏图标。
	tray := image.NewNRGBA(image.Rect(0, 0, 40, 36))
	ratio := math.Min(40/float64(bounds.Dx()), 36/float64(bounds.Dy()))
	w, h := int(math.Round(float64(bounds.Dx())*ratio)), int(math.Round(float64(bounds.Dy())*ratio))
	dst := image.Rect((40-w)/2, (36-h)/2, (40-w)/2+w, (36-h)/2+h)
	draw.CatmullRom.Scale(tray, dst, mask, bounds, draw.Src, nil)
	return writePNG("assets/brand/tray-template.png", tray)
}

// rounded 将原稿等比缩放为圆角方形；padding 和 radius 均以目标像素计。
func rounded(source image.Image, size, padding, radius int) *image.NRGBA {
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	rect := image.Rect(padding, padding, size-padding, size-padding)
	draw.CatmullRom.Scale(dst, rect, source, source.Bounds(), draw.Src, nil)
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			cx := math.Max(float64(rect.Min.X+radius), math.Min(float64(x)+.5, float64(rect.Max.X-radius)))
			cy := math.Max(float64(rect.Min.Y+radius), math.Min(float64(y)+.5, float64(rect.Max.Y-radius)))
			distance := math.Hypot(float64(x)+.5-cx, float64(y)+.5-cy)
			coverage := math.Max(0, math.Min(1, float64(radius)+.5-distance))
			c := dst.NRGBAAt(x, y)
			c.A = uint8(math.Round(float64(c.A) * coverage))
			dst.SetNRGBA(x, y, c)
		}
	}
	return dst
}

// writePNG 写出无损资源，调用方负责选择平台所需的像素尺寸。
func writePNG(path string, img image.Image) error {
	f, err := os.Create(filepath.Clean(path))
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// writeICNS 提供普通与 Retina 槽位；小尺寸由兼容 PNG 的 ic11/ic12 承载。
func writeICNS(path string, source image.Image) error {
	var body bytes.Buffer
	for _, item := range []struct {
		kind string
		size int
	}{{"ic11", 32}, {"ic12", 64}, {"ic07", 128}, {"ic13", 256}, {"ic08", 256}, {"ic14", 512}, {"ic09", 512}, {"ic10", 1024}} {
		img := image.NewNRGBA(image.Rect(0, 0, item.size, item.size))
		draw.CatmullRom.Scale(img, img.Bounds(), source, source.Bounds(), draw.Src, nil)
		var data bytes.Buffer
		if err := png.Encode(&data, img); err != nil {
			return err
		}
		body.WriteString(item.kind)
		_ = binary.Write(&body, binary.BigEndian, uint32(data.Len()+8))
		body.Write(data.Bytes())
	}
	var out bytes.Buffer
	out.WriteString("icns")
	_ = binary.Write(&out, binary.BigEndian, uint32(body.Len()+8))
	out.Write(body.Bytes())
	return os.WriteFile(path, out.Bytes(), 0644)
}
