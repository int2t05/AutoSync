// tray_fyne.go 用 Fyne 实现托盘守护应用（Windows，需 CGO + -tags traygui）。
// M2 骨架：托盘图标 + 退出菜单 + 启动调度器；M3 补配置窗口与完整菜单。
//
//go:build windows && traygui

package tray

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"

	"autosync/internal/configstore"
	"autosync/internal/log"
	"autosync/internal/tasksched"
)

// TrayApp 托盘守护应用。
type TrayApp struct {
	app    fyne.App
	sched  *tasksched.TaskScheduler
	store  *configstore.Store
	logger *log.Logger
}

// NewTrayApp 创建托盘应用，构造 Fyne 应用实例。
func NewTrayApp(sched *tasksched.TaskScheduler, store *configstore.Store, logger *log.Logger) *TrayApp {
	return &TrayApp{app: app.NewWithID("autosync"), sched: sched, store: store, logger: logger}
}

// Run 启动调度器与托盘事件循环，阻塞至用户退出。
func (a *TrayApp) Run() error {
	// TODO: M3 补配置窗口、各任务手动同步/暂停、状态回显
	menu := fyne.NewMenu("AutoSync",
		fyne.NewMenuItem("退出", func() { a.app.Quit() }),
	)
	if desk, ok := a.app.(desktop.App); ok {
		desk.SetSystemTrayMenu(menu)
		desk.SetSystemTrayIcon(theme.FyneLogo())
	}
	a.sched.Start()
	a.app.Run() // 阻塞至退出
	a.sched.Stop()
	return nil
}
