// icon.go 提供托盘与窗口的自有图标资源（嵌入 PNG）。
//
//go:build windows && traygui

package tray

import (
	_ "image/png" // 注册 PNG 解码器，供 Fyne 解码嵌入资源

	"fyne.io/fyne/v2"

	"autosync/internal/assets"
)

// TrayIcon 返回 AutoSync 图标资源，供系统托盘与窗口使用。
func TrayIcon() fyne.Resource {
	return fyne.NewStaticResource("icon.png", assets.IconPNG())
}
