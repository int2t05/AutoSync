// tray_fyne.go 用 Fyne 实现托盘守护应用（Windows，需 CGO + -tags traygui）。
// 托盘菜单：各任务手动同步/暂停、打开配置、退出。配置窗口：任务列表 CRUD，保存后热重载。
//
//go:build windows && traygui

package tray

import (
	"fmt"
	"os"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"autosync/internal/autostart"
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
	win    fyne.Window
}

// NewTrayApp 创建托盘应用，构造 Fyne 应用实例。
func NewTrayApp(sched *tasksched.TaskScheduler, store *configstore.Store, logger *log.Logger) *TrayApp {
	return &TrayApp{app: app.NewWithID("autosync"), sched: sched, store: store, logger: logger}
}

// Run 启动调度器与托盘事件循环，阻塞至用户退出。空配置时自动弹出配置窗口。
func (a *TrayApp) Run() error {
	a.refreshMenu()
	if desk, ok := a.app.(desktop.App); ok {
		desk.SetSystemTrayIcon(TrayIcon())
	}
	if len(a.store.List()) == 0 {
		a.showConfig() // 首次运行无任务：自动弹出配置窗口
	}
	a.sched.Start()
	a.app.Run() // 阻塞至退出
	a.sched.Stop()
	return nil
}

// refreshMenu 重建托盘右键菜单。
func (a *TrayApp) refreshMenu() {
	if desk, ok := a.app.(desktop.App); ok {
		desk.SetSystemTrayMenu(a.buildMenu())
	}
}

// buildMenu 构造托盘菜单：各任务（手动同步/暂停）+ 配置 + 退出。
func (a *TrayApp) buildMenu() *fyne.Menu {
	var items []*fyne.MenuItem
	for _, r := range a.sched.Runners() {
		name := r.Task().Name
		r := r
		pauseLabel := "暂停"
		if r.Paused() {
			pauseLabel = "恢复"
		}
		taskItem := fyne.NewMenuItem(name, nil)
		taskItem.ChildMenu = fyne.NewMenu(name,
			fyne.NewMenuItem("手动同步", func() { a.runTask(name) }),
			fyne.NewMenuItem(pauseLabel, func() {
				r.SetPaused(!r.Paused())
				a.refreshMenu()
			}),
		)
		items = append(items, taskItem)
	}
	// 开机自启开关：根据当前状态切换注册表 Run 键
	autoLabel := "开机自启"
	if autostart.IsEnabled() {
		autoLabel = "✓ 开机自启"
	}
	autoItem := fyne.NewMenuItem(autoLabel, func() {
		exe, err := os.Executable()
		if err != nil {
			a.logger.Warn(fmt.Sprintf("获取可执行文件路径失败: %v", err))
			return
		}
		if autostart.IsEnabled() {
			if err := autostart.Disable(); err != nil {
				a.logger.Warn(fmt.Sprintf("关闭开机自启失败: %v", err))
			}
		} else {
			if err := autostart.Enable(exe, ""); err != nil {
				a.logger.Warn(fmt.Sprintf("设置开机自启失败: %v", err))
			}
		}
		a.refreshMenu()
	})
	items = append(items,
		fyne.NewMenuItemSeparator(),
		autoItem,
		fyne.NewMenuItem("配置...", func() { a.showConfig() }),
		fyne.NewMenuItem("退出", func() { a.app.Quit() }),
	)
	return fyne.NewMenu("AutoSync", items...)
}

// runTask 在后台手动同步指定任务（不阻塞 UI）。
func (a *TrayApp) runTask(name string) {
	go func() {
		result, err := a.sched.RunNow(name)
		if err != nil {
			a.logger.Warn(fmt.Sprintf("手动同步 %s 失败: %v", name, err))
			return
		}
		a.logger.Info(fmt.Sprintf("手动同步 %s: %s — %s", name, result.Outcome, result.Message))
	}()
}

// showConfig 打开配置窗口（已打开则前置）。
func (a *TrayApp) showConfig() {
	if a.win != nil {
		a.win.Show()
		return
	}
	a.win = a.app.NewWindow("AutoSync 配置")
	a.win.SetIcon(TrayIcon())
	a.win.SetContent(a.buildConfigContent())
	a.win.Resize(fyne.NewSize(640, 420))
	a.win.SetOnClosed(func() { a.win = nil })
	a.win.Show()
}

// buildConfigContent 构造配置窗口内容：任务列表 + 新增/编辑/删除按钮。
func (a *TrayApp) buildConfigContent() fyne.CanvasObject {
	list := widget.NewList(
		func() int { return len(a.store.List()) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			tasks := a.store.List()
			if i < len(tasks) {
				o.(*widget.Label).SetText(fmt.Sprintf("%s — %s [%s]", tasks[i].Name, tasks[i].RepoDir, tasks[i].Interval))
			}
		},
	)
	selected := -1
	list.OnSelected = func(id widget.ListItemID) { selected = int(id) }

	addBtn := widget.NewButton("新增", func() { a.editTask(nil, list, &selected) })
	editBtn := widget.NewButton("编辑", func() {
		tasks := a.store.List()
		if selected >= 0 && selected < len(tasks) {
			a.editTask(tasks[selected], list, &selected)
		}
	})
	delBtn := widget.NewButton("删除", func() {
		tasks := a.store.List()
		if selected < 0 || selected >= len(tasks) {
			return
		}
		name := tasks[selected].Name
		dialog.ShowConfirm("删除任务", "确认删除 "+name+" ?", func(ok bool) {
			if !ok {
				return
			}
			if err := a.store.Delete(name); err != nil {
				dialog.ShowError(err, a.win)
				return
			}
			a.persistAndReload()
			list.UnselectAll()
			selected = -1
			list.Refresh()
		}, a.win)
	})

	toolbar := container.NewHBox(addBtn, editBtn, delBtn)
	return container.NewBorder(toolbar, nil, nil, nil, list)
}

// editTask 弹出表单编辑任务（existing 为 nil 表示新增），保存后热重载并重置选中项。
func (a *TrayApp) editTask(existing *configstore.Task, list *widget.List, selected *int) {
	t := &configstore.Task{}
	if existing != nil {
		*t = *existing
	}
	nameEntry := widget.NewEntry()
	repoEntry := widget.NewEntry()
	urlEntry := widget.NewEntry()
	branchEntry := widget.NewEntry()
	intervalEntry := widget.NewEntry()
	strategySelect := widget.NewSelect([]string{"local_wins", "remote_wins", "abort"}, nil)
	if existing != nil {
		nameEntry.SetText(t.Name)
		repoEntry.SetText(t.RepoDir)
		urlEntry.SetText(t.RemoteURL)
		branchEntry.SetText(t.Branch)
		intervalEntry.SetText(t.Interval)
		strategySelect.SetSelected(t.ConflictStrategy)
	} else {
		branchEntry.SetText("main")
		intervalEntry.SetText("1m")
		strategySelect.SetSelected("local_wins")
	}
	items := []*widget.FormItem{
		{Text: "名称", Widget: nameEntry},
		{Text: "目录", Widget: repoEntry},
		{Text: "远程地址", Widget: urlEntry},
		{Text: "分支", Widget: branchEntry},
		{Text: "间隔", Widget: intervalEntry},
		{Text: "冲突策略", Widget: strategySelect},
	}
	// dialog.NewForm 自带确认/取消按钮并与表单验证集成，无需 widget.Form 的独立按钮。
	dlg := dialog.NewForm("任务", "保存", "取消", items, func(save bool) {
		if !save {
			return
		}
		t.Name = nameEntry.Text
		t.RepoDir = repoEntry.Text
		t.RemoteURL = urlEntry.Text
		t.Branch = branchEntry.Text
		t.Interval = intervalEntry.Text
		t.ConflictStrategy = strategySelect.Selected
		var err error
		if existing == nil {
			err = a.store.Add(t)
		} else {
			err = a.store.Update(existing.Name, t)
		}
		if err != nil {
			dialog.ShowError(err, a.win)
			return
		}
		a.persistAndReload()
		*selected = -1
		list.UnselectAll()
		list.Refresh()
	}, a.win)
	dlg.Resize(fyne.NewSize(500, 420))
	dlg.Show()
}

// persistAndReload 保存配置并热重载调度器与托盘菜单。
func (a *TrayApp) persistAndReload() {
	if err := a.store.Save(); err != nil {
		dialog.ShowError(err, a.win)
		return
	}
	a.sched.Reload(a.store.List())
	a.refreshMenu()
}
