// tray_stub.go 在未启用 traygui 标签或非 Windows 平台提供托盘桩。
// 默认构建（无 -tags traygui）用此桩，保持纯 Go、可交叉编译、无 CGO 依赖。
// 发布 Windows 托盘版：go build -tags traygui。
//
//go:build !(windows && traygui)

package tray

import (
	"errors"

	"autosync/internal/configstore"
	"autosync/internal/log"
	"autosync/internal/tasksched"
)

// ErrTrayDisabled 表示当前构建未启用托盘（需 -tags traygui 且 Windows）。
var ErrTrayDisabled = errors.New("托盘模式未启用（需 -tags traygui 构建）")

// TrayApp 托盘应用（桩）。
type TrayApp struct{}

// NewTrayApp 创建托盘应用桩。参数仅为与 Fyne 实现 API 一致。
func NewTrayApp(sched *tasksched.TaskScheduler, store *configstore.Store, logger *log.Logger) *TrayApp {
	return &TrayApp{}
}

// Run 托盘桩直接返回未启用错误。showWindow 参数仅为与 Fyne 实现 API 一致。
func (a *TrayApp) Run(showWindow bool) error {
	return ErrTrayDisabled
}
