// assets.go 内嵌 AutoSync 图标资源。
// 图标源为同目录 icon.svg，由 cmd/genicon 光栅化为 icon.png 供嵌入。
package assets

import _ "embed"

//go:embed icon.png
var iconPNG []byte

// IconPNG 返回嵌入的图标 PNG 字节（256x256），供 Fyne 资源化与渲染。
func IconPNG() []byte { return iconPNG }
