// genicon 光栅化 SVG 图标为 PNG，供 Fyne 嵌入、Windows exe 图标、macOS .app 图标与菜单栏 template。
// - internal/assets/icon.svg → icon.png（256，彩色，Fyne + go-winres exe 图标）
// - internal/assets/icon.svg → macos AppIcon.appiconset/icon_1024.png（1024，Xcode single-size 生成 .icns）
// - internal/assets/icon-menubar.svg → macos MenubarIcon.imageset menubar_16.png(16) + menubar_32.png(32)（纯黑 template）
// 用 Fyne 同款 oksvg+rasterx 渲染，保证 PNG 与 Fyne 运行时图标一致。用法：go run ./cmd/genicon
package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"

	"github.com/fyne-io/oksvg"
	"github.com/srwiley/rasterx"
)

// renderSVG 读取 SVG 并光栅化为指定尺寸的 image.RGBA。
func renderSVG(inPath string, size int) (image.Image, error) {
	icon, err := oksvg.ReadIcon(inPath)
	if err != nil {
		return nil, fmt.Errorf("解析 %s: %w", inPath, err)
	}
	icon.SetTarget(0, 0, float64(size), float64(size))
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	sc := rasterx.NewScannerGV(size, size, img, img.Bounds())
	rast := rasterx.NewDasher(size, size, sc)
	icon.Draw(rast, 1.0)
	return img, nil
}

// writePNG 把图像写入 outPath，自动创建父目录。
func writePNG(outPath string, img image.Image) error {
	if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
		return fmt.Errorf("创建目录 %s: %w", filepath.Dir(outPath), err)
	}
	out, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("创建 %s: %w", outPath, err)
	}
	defer out.Close()
	if err := png.Encode(out, img); err != nil {
		return fmt.Errorf("编码 PNG %s: %w", outPath, err)
	}
	return nil
}

// renderPNG 一步完成 SVG→PNG（指定尺寸），失败时 fatal。
func renderPNG(inPath, outPath string, size int) {
	img, err := renderSVG(inPath, size)
	if err != nil {
		fail(err.Error())
	}
	if err := writePNG(outPath, img); err != nil {
		fail(err.Error())
	}
	fmt.Println("已生成", outPath)
}

func main() {
	const colorSrc = "internal/assets/icon.svg"
	const menubarSrc = "internal/assets/icon-menubar.svg"

	// Windows/Fyne + exe 图标（彩色 256）
	renderPNG(colorSrc, "internal/assets/icon.png", 256)
	// macOS app 图标源（彩色 1024，Xcode appiconset single-size 自动生成各尺寸 .icns）
	renderPNG(colorSrc, "macos/AutoSync/Assets.xcassets/AppIcon.appiconset/icon_1024.png", 1024)
	// macOS 菜单栏 template（纯黑 16@1x + 32@2x）
	renderPNG(menubarSrc, "macos/AutoSync/Assets.xcassets/MenubarIcon.imageset/menubar_16.png", 16)
	renderPNG(menubarSrc, "macos/AutoSync/Assets.xcassets/MenubarIcon.imageset/menubar_32.png", 32)
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}
