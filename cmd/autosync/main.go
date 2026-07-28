// main.go 是 AutoSync 的入口，负责 CLI 分发与依赖装配。
// sync 支持 --dry-run 只读预览与单实例锁；install/uninstall 通过注册表 Run 键开关开机自启。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"autosync/internal/autostart"
	"autosync/internal/config"
	"autosync/internal/configstore"
	"autosync/internal/gitignore"
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

// run 解析子命令并分发，返回退出码。无子命令（双击/裸跑）默认进入托盘守护。
func run(args []string) int {
	cmd, rest := parseCommand(args)
	switch cmd {
	case "tray":
		return runTray(rest)
	case "status":
		return runStatus(rest)
	case "install":
		return runInstall(rest)
	case "uninstall":
		return runUninstall()
	default:
		return runSync(rest)
	}
}

// parseCommand 识别子命令；无子命令或仅旗标时默认 tray（双击即托盘）。
func parseCommand(args []string) (cmd string, rest []string) {
	if len(args) > 0 {
		switch args[0] {
		case "sync", "status", "install", "uninstall", "tray":
			return args[0], args[1:]
		}
	}
	return "tray", args
}

// runSync 执行单次同步：加载配置 → 日志 → .gitignore → 加锁 → 同步 → 持久化状态 → 通知。
// --dry-run 时只输出同步计划，不联网、不写盘、不加锁。
func runSync(rest []string) int {
	fs := flag.NewFlagSet("autosync", flag.ContinueOnError)
	configPath := fs.String("config", "", "配置文件路径（默认为可执行文件同目录的 config.yaml）")
	dryRun := fs.Bool("dry-run", false, "只读预览同步计划，不实际执行")
	if err := fs.Parse(rest); err != nil {
		return 1
	}

	cfg, err := config.Load(resolveConfigPath(*configPath))
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ 配置加载失败: %v\n", err)
		return 1
	}

	if err := config.EnsureUserDataDirs(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		return 1
	}

	logger, err := log.New(config.LogFilePath(), cfg.ShowConsole)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ 日志初始化失败: %v\n", err)
		return 1
	}
	defer logger.Close()

	logger.Info(fmt.Sprintf("AutoSync 启动 | 目录=%s | 远程=%s | 分支=%s | 策略=%s | 间隔=%s",
		cfg.RepoDir, cfg.RemoteURL, cfg.Branch, cfg.ConflictStrategy, cfg.Interval))

	// 构造 git 操作器：execGit + 重试装饰器（网络操作指数退避）
	gitOp := gitop.NewRetryGit(
		gitop.NewExecGit(cfg.RepoDir, logger),
		cfg.RetryCount, cfg.RetryBaseDelayDur, logger,
	)
	syncer := sync.NewSyncer(cfg, gitOp, logger)

	// --dry-run：只读分析，不写盘不加锁
	if *dryRun {
		plan := syncer.DryRun()
		fmt.Println("AutoSync 同步计划（dry-run，不实际执行）")
		fmt.Println("────────────────────────────────")
		for i, step := range plan.Steps {
			fmt.Printf("%d. %s\n", i+1, step)
		}
		logger.Info("dry-run 完成")
		return 0
	}

	// 维护 .gitignore：仅追加缺失条目
	gitignorePath := filepath.Join(cfg.RepoDir, ".gitignore")
	if added, err := gitignore.Ensure(gitignorePath, cfg.Ignore); err != nil {
		logger.Warn(fmt.Sprintf("维护 .gitignore 失败: %v", err))
	} else if added > 0 {
		logger.Info(fmt.Sprintf("已向 .gitignore 追加 %d 条", added))
	}

	// 单实例锁：防止间隔内并发执行破坏仓库；被存活实例持有时静默跳过
	locker := lock.New(config.LockFilePath("default"))
	acquired, release := locker.Acquire()
	if !acquired {
		logger.Info("已有同步实例在运行，跳过本次")
		return 0
	}
	defer release()

	result := syncer.Run()

	// 持久化状态（供 status 命令读取）
	stateStore := state.New(config.StateFilePath("default"))
	if err := stateStore.Save(state.State{
		LastSyncAt:   time.Now(),
		LastOutcome:  result.Outcome.String(),
		LastMessage:  result.Message,
		BackupBranch: result.BackupBranch,
	}); err != nil {
		logger.Warn(fmt.Sprintf("写入状态文件失败: %v", err))
	}

	// 通知策略：成功静默，冲突/失败/初始化才通知
	decision := notify.PolicyFor(result)
	if decision.Notify {
		if err := notify.NewBeeepNotifier().Notify(decision.Title, decision.Body, decision.Severity); err != nil {
			logger.Warn(fmt.Sprintf("发送通知失败: %v", err))
		}
	}

	// 退出码：失败/中止 → 1，其余 → 0
	if result.Outcome == sync.OutcomeFailed || result.Outcome == sync.OutcomeConflictAborted {
		logger.Error(fmt.Sprintf("同步失败: %s", result.Message))
		return 1
	}
	logger.Info(fmt.Sprintf("同步完成: %s — %s", result.Outcome, result.Message))
	return 0
}

// runTray 启动托盘守护：先建用户目录与日志 → 加载多任务配置 → 守护级单实例锁 → 调度器 → 托盘应用。
// 无 traygui 标签构建时托盘为桩，Run 返回未启用错误。
func runTray(rest []string) int {
	fs := flag.NewFlagSet("autosync", flag.ContinueOnError)
	configPath := fs.String("config", "", "配置文件路径（默认 ~/.autosync/autosync.conf.yaml）")
	if err := fs.Parse(rest); err != nil {
		return 1
	}

	// 先确保用户数据目录与日志：后续配置/锁失败可写日志，便于静默版诊断
	if err := config.EnsureUserDataDirs(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		return 1
	}
	logger, err := log.New(config.LogFilePath(), false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ 日志初始化失败: %v\n", err)
		return 1
	}
	defer logger.Close()

	store, err := configstore.Load(resolveTrayConfigPath(*configPath))
	if err != nil {
		logger.Error(fmt.Sprintf("配置加载失败: %v", err))
		fmt.Fprintf(os.Stderr, "❌ 配置加载失败: %v\n", err)
		return 1
	}
	logger.Info(fmt.Sprintf("AutoSync 托盘启动 | 任务数=%d", len(store.List())))

	// 守护级单实例锁：防止多个托盘实例
	daemonLock := lock.New(config.DaemonLockPath())
	acquired, release := daemonLock.Acquire()
	if !acquired {
		logger.Info("已有 AutoSync 实例在运行，退出")
		fmt.Fprintln(os.Stderr, "❌ 已有 AutoSync 实例在运行")
		return 1
	}
	defer release()

	sched := tasksched.NewTaskScheduler(store.List(), logger)
	if err := tray.NewTrayApp(sched, store, logger).Run(); err != nil {
		logger.Error(fmt.Sprintf("托盘退出: %v", err))
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

// runInstall 设置开机自启：注册表 Run 键写入托盘守护启动命令，登录即自启。
// --config 可指定托盘配置路径（相对路径转绝对，避免登录时工作目录不同），缺省由托盘自行解析。
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

// runUninstall 移除开机自启注册表项。
func runUninstall() int {
	if err := autostart.Disable(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ 移除开机自启失败: %v\n", err)
		return 1
	}
	fmt.Println("✅ 已移除开机自启")
	return 0
}

// runStatus 读取并展示上次同步状态。用宽松加载，允许 repo_dir 暂时不可用。
func runStatus(rest []string) int {
	fs := flag.NewFlagSet("autosync status", flag.ContinueOnError)
	configPath := fs.String("config", "", "配置文件路径")
	if err := fs.Parse(rest); err != nil {
		return 1
	}

	cfg, err := config.LoadLenient(resolveConfigPath(*configPath))
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ 配置加载失败: %v\n", err)
		return 1
	}

	st, err := state.New(config.StateFilePath("default")).Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ 读取状态失败: %v\n", err)
		return 1
	}

	fmt.Println("AutoSync 状态")
	fmt.Println("────────────────────────────────")
	fmt.Printf("仓库目录: %s\n", cfg.RepoDir)
	fmt.Printf("远程:     %s (%s)\n", cfg.RemoteURL, cfg.Branch)
	fmt.Printf("策略:     %s | 间隔: %s\n", cfg.ConflictStrategy, cfg.Interval)
	fmt.Println("────────────────────────────────")
	if st.LastSyncAt.IsZero() {
		fmt.Println("尚未同步过")
		return 0
	}
	fmt.Printf("上次同步: %s\n", st.LastSyncAt.Format("2006-01-02 15:04:05"))
	fmt.Printf("结果:     %s\n", st.LastOutcome)
	fmt.Printf("摘要:     %s\n", st.LastMessage)
	if st.BackupBranch != "" {
		fmt.Printf("备份分支: %s\n", st.BackupBranch)
	}
	return 0
}

// resolveConfigPath 解析 CLI 配置路径：--config 优先，否则用 ~/.autosync/config.yaml。
func resolveConfigPath(explicit string) string {
	if explicit != "" {
		return explicit
	}
	return config.CLIConfigPath()
}
