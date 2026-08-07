// main.go 是 AutoSync 的入口，负责 CLI 分发与依赖装配。
// sync 支持 --dry-run 只读预览与单实例锁；install/uninstall 跨平台开关开机自启
//（Windows 注册表 Run 键 / Linux systemd user service / macOS 由壳 SMAppService 管理）。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"autosync/internal/autostart"
	"autosync/internal/config"
	"autosync/internal/configstore"
	"autosync/internal/gitop"
	"autosync/internal/lock"
	"autosync/internal/log"
	"autosync/internal/notify"
	"autosync/internal/state"
	"autosync/internal/sync"
	"autosync/internal/tasksched"
	"autosync/internal/tray"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// run 解析子命令并分发，返回退出码。无子命令时按平台分流（windows→tray / darwin→engine / linux→daemon）。
func run(args []string) int {
	cmd, rest := parseCommand(args)
	switch cmd {
	case "sync":
		return runSync(rest)
	case "status":
		return runStatus(rest)
	case "install":
		return runInstall(rest)
	case "uninstall":
		return runUninstall()
	case "tray":
		return runTray(rest)
	case "engine":
		return runEngine(rest)
	case "daemon":
		return runDaemon(rest)
	default:
		return defaultRun(rest) // 无子命令：平台分流
	}
}

// parseCommand 识别子命令；无子命令返回空串，由 run() 按平台分流。
func parseCommand(args []string) (cmd string, rest []string) {
	if len(args) > 0 {
		switch args[0] {
		case "sync", "status", "install", "uninstall", "tray", "engine", "daemon":
			return args[0], args[1:]
		}
	}
	return "", args
}

// runSync 执行单次同步：载入多任务配置 → 解析目标任务 → 复用 TaskRunner 编排（任务锁/.gitignore/Syncer/状态/通知）。
// 支持 `sync [task]`：指定任务名，或仅配置一个任务时省略；--dry-run 只读预览，不联网、不写盘、不加锁。
func runSync(rest []string) int {
	fs := flag.NewFlagSet("autosync", flag.ContinueOnError)
	configPath := fs.String("config", "", "多任务配置文件路径（默认 ~/.autosync/autosync.conf.yaml）")
	dryRun := fs.Bool("dry-run", false, "只读预览同步计划，不实际执行")
	if err := fs.Parse(rest); err != nil {
		return 1
	}
	taskName := fs.Arg(0)

	store, logger, cleanup, err := setupTrayEnv(*configPath, "sync")
	if err != nil {
		return 1
	}
	defer cleanup()

	task, err := resolveTask(store, taskName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		return 1
	}

	// --dry-run：只读分析，不联网、不写盘、不加锁
	if *dryRun {
		gitOp := gitop.NewRetryGit(
			gitop.NewExecGit(task.RepoDir, logger, task.GitTimeoutDur),
			task.RetryCount, task.RetryBaseDelayDur, logger,
		)
		plan := sync.NewSyncer(&task.Config, gitOp, logger).DryRun()
		fmt.Println("AutoSync 同步计划（dry-run，不实际执行）")
		fmt.Println("────────────────────────────────")
		for i, step := range plan.Steps {
			fmt.Printf("%d. %s\n", i+1, step)
		}
		logger.Info("dry-run 完成")
		return 0
	}

	// 复用 TaskRunner 编排：任务级锁 → .gitignore 维护 → Syncer → 状态 → 通知
	result := tasksched.NewTaskRunner(task, logger, notify.NewBeeepNotifier()).Run()
	if result.Outcome == sync.OutcomeFailed {
		fmt.Fprintf(os.Stderr, "❌ 同步失败: %s\n", result.Message)
		logger.Errorf("同步失败: %s", result.Message)
		return 1
	}
	fmt.Printf("同步完成: %s — %s\n", result.Outcome, result.Message)
	return 0
}

// resolveTask 按名或唯一任务解析目标任务。
// name 为空时要求配置中恰好一个任务；任务缺失或多任务未指名时返回带说明的 error。
func resolveTask(store *configstore.Store, name string) (*configstore.Task, error) {
	if name != "" {
		t := store.Get(name)
		if t == nil {
			return nil, fmt.Errorf("任务不存在: %q", name)
		}
		return t, nil
	}
	tasks := store.List()
	switch len(tasks) {
	case 0:
		return nil, fmt.Errorf("未配置任务，请先编辑 %s", config.TrayConfigPath())
	case 1:
		return tasks[0], nil
	default:
		return nil, fmt.Errorf("配置了多个任务，请指定任务名（autosync sync <任务名>）")
	}
}

// setupTrayEnv 装配托盘/engine 共享的运行时环境：~/.autosync/ + 日志 + 多任务配置。
// mode 用于启动日志（"托盘"/"engine"）。返回 store + logger + cleanup；失败返回 error。
func setupTrayEnv(configPath, mode string) (*configstore.Store, *log.Logger, func(), error) {
	if err := config.EnsureUserDataDirs(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		return nil, nil, nil, err
	}
	logger, err := log.New(config.LogFilePath(), false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ 日志初始化失败: %v\n", err)
		return nil, nil, nil, err
	}
	store, err := configstore.Load(resolveTrayConfigPath(configPath))
	if err != nil {
		logger.Errorf("配置加载失败: %v", err)
		fmt.Fprintf(os.Stderr, "❌ 配置加载失败: %v\n", err)
		logger.Close()
		return nil, nil, nil, err
	}
	logger.Infof("AutoSync %s 启动 | 任务数=%d", mode, len(store.List()))
	return store, logger, func() { logger.Close() }, nil
}

// runTray 启动托盘守护：装配环境 → 守护级单实例锁 → 调度器 → 托盘应用。
// --background 供开机自启：后台启动不弹配置窗口；双击裸跑则弹出窗口供交互。
// 无 traygui 标签构建时托盘为桩，Run 返回未启用错误。
func runTray(rest []string) int {
	fs := flag.NewFlagSet("autosync", flag.ContinueOnError)
	configPath := fs.String("config", "", "配置文件路径（默认 ~/.autosync/autosync.conf.yaml）")
	background := fs.Bool("background", false, "后台启动（不弹配置窗口，供开机自启）")
	if err := fs.Parse(rest); err != nil {
		return 1
	}

	store, logger, cleanup, err := setupTrayEnv(*configPath, "托盘")
	if err != nil {
		return 1
	}
	defer cleanup()

	// 守护级单实例锁：防止多个托盘实例
	daemonLock := lock.New(config.DaemonLockPath())
	acquired, release := daemonLock.Acquire()
	if !acquired {
		logger.Info("已有 AutoSync 实例在运行，退出")
		fmt.Fprintln(os.Stderr, "❌ 已有 AutoSync 实例在运行")
		return 1
	}
	defer release()

	sched := tasksched.NewTaskScheduler(store.List(), logger, notify.NewBeeepNotifier(), nil)
	if err := tray.NewTrayApp(sched, store, logger).Run(!*background); err != nil {
		logger.Errorf("托盘退出: %v", err)
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		return 1
	}
	return 0
}

// resolveTrayConfigPath 解析托盘配置路径：--config 优先，否则用 ~/.autosync/autosync.conf.yaml。
func resolveTrayConfigPath(explicit string) string {
	if explicit != "" {
		return explicit
	}
	return config.TrayConfigPath()
}

// runInstall 设置开机自启（Windows 注册表 Run 键 / Linux systemd user service / macOS 由壳管理）。
// --config 可指定配置路径（相对路径转绝对，避免登录时工作目录不同），缺省由守护自行解析。
func runInstall(rest []string) int {
	fs := flag.NewFlagSet("autosync install", flag.ContinueOnError)
	configPath := fs.String("config", "", "托盘配置文件路径（默认 autosync.conf.yaml）")
	if err := fs.Parse(rest); err != nil {
		return 1
	}

	binPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ 获取可执行文件路径失败: %v\n", err)
		return 1
	}

	// 仅显式 --config 时写入命令；相对路径转绝对，防止登录时工作目录不一致
	cfgArg := ""
	if *configPath != "" {
		if abs, err := filepath.Abs(*configPath); err == nil {
			cfgArg = abs
		} else {
			cfgArg = *configPath
		}
	}

	if err := autostart.Enable(binPath, cfgArg); err != nil {
		fmt.Fprintf(os.Stderr, "❌ 设置开机自启失败: %v\n", err)
		return 1
	}
	fmt.Printf("✅ 已设置开机自启：%s\n", autostart.BuildRunCommand(binPath, cfgArg))
	return 0
}

// runUninstall 移除开机自启（Windows 注册表项 / Linux systemd unit）。
func runUninstall() int {
	if err := autostart.Disable(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ 移除开机自启失败: %v\n", err)
		return 1
	}
	fmt.Println("✅ 已移除开机自启")
	return 0
}

// runStatus 读取并展示任务同步状态。用宽松加载，允许 repo_dir 暂时不可用。
// 支持 `status [task]`：指定任务名或展示全部任务。
func runStatus(rest []string) int {
	fs := flag.NewFlagSet("autosync status", flag.ContinueOnError)
	configPath := fs.String("config", "", "多任务配置文件路径（默认 ~/.autosync/autosync.conf.yaml）")
	if err := fs.Parse(rest); err != nil {
		return 1
	}
	taskName := fs.Arg(0)

	store, err := configstore.LoadLenient(resolveTrayConfigPath(*configPath))
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ 配置加载失败: %v\n", err)
		return 1
	}
	tasks := store.List()
	if taskName != "" {
		t := store.Get(taskName)
		if t == nil {
			fmt.Fprintf(os.Stderr, "❌ 任务不存在: %q\n", taskName)
			return 1
		}
		tasks = []*configstore.Task{t}
	}

	fmt.Println("AutoSync 状态")
	fmt.Println("────────────────────────────────")
	for _, t := range tasks {
		fmt.Printf("任务:     %s\n", t.Name)
		fmt.Printf("仓库目录: %s\n", t.RepoDir)
		fmt.Printf("远程:     %s (%s)\n", t.RemoteURL, t.Branch)
		fmt.Printf("策略:     %s | 间隔: %s\n", t.ConflictStrategy, t.Interval)
		st, err := state.New(t.ResolveStateFile()).Load()
		if err != nil || st.LastSyncAt.IsZero() {
			fmt.Println("上次同步: 尚未同步过")
		} else {
			fmt.Printf("上次同步: %s\n", st.LastSyncAt.Format("2006-01-02 15:04:05"))
			fmt.Printf("结果:     %s\n", st.LastOutcome)
			fmt.Printf("摘要:     %s\n", st.LastMessage)
			if st.BackupBranch != "" {
				fmt.Printf("备份分支: %s\n", st.BackupBranch)
			}
		}
		fmt.Println("────────────────────────────────")
	}
	return 0
}
