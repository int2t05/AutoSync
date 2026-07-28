// genicon 把 internal/assets/icon.svg 光栅化为 internal/assets/icon.png（供 Fyne 嵌入与 go-winres exe 图标）。
// 用 Fyne 同款 oksvg+rasterx 渲染，保证 PNG 与 Fyne 运行时图标一致。
// 用法：go run ./cmd/genicon
package main

import (
	"fmt"
	"image"
	"image/png"
	"os"

	"github.com/fyne-io/oksvg"
	"github.com/srwiley/rasterx"
)

func main() {
	const inPath, outPath = "internal/assets/icon.svg", "internal/assets/icon.png"
	const size = 256

	icon, err := oksvg.ReadIcon(inPath)
	if err != nil {
		fail(fmt.Sprintf("解析 %s: %v", inPath, err))
	}
	icon.SetTarget(0, 0, size, size)

	img := image.NewRGBA(image.Rect(0, 0, size, size))
	sc := rasterx.NewScannerGV(size, size, img, img.Bounds())
	rast := rasterx.NewDasher(size, size, sc)
	icon.Draw(rast, 1.0)

	out, err := os.Create(outPath)
	if err != nil {
		fail(fmt.Sprintf("创建 %s: %v", outPath, err))
	}
	defer out.Close()
	if err := png.Encode(out, img); err != nil {
		fail(fmt.Sprintf("编码 PNG: %v", err))
	}
	fmt.Println("已生成", outPath)
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}
