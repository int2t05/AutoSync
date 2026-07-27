// main.go 是 AutoSync 的入口，负责 CLI 分发与依赖装配。
// P4：sync 支持 --dry-run 只读预览与单实例锁；install/uninstall 通过 schtasks 自安装调度。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"autosync/internal/config"
	"autosync/internal/gitignore"
	"autosync/internal/gitop"
	"autosync/internal/lock"
	"autosync/internal/log"
	"autosync/internal/notify"
	"autosync/internal/sched"
	"autosync/internal/state"
	"autosync/internal/sync"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// run 解析子命令并分发，返回退出码。
func run(args []string) int {
	cmd, rest := parseCommand(args)
	switch cmd {
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

// parseCommand 识别子命令；无子命令时默认 sync 并保留全部参数（含 --config）。
func parseCommand(args []string) (cmd string, rest []string) {
	if len(args) > 0 {
		switch args[0] {
		case "sync", "status", "install", "uninstall":
			return args[0], args[1:]
		}
	}
	return "sync", args
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

	logger, err := log.New(cfg.ResolveLogFile(), cfg.ShowConsole)
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

	// 维护 .gitignore：仅追加缺失条目（US-010）
	gitignorePath := filepath.Join(cfg.RepoDir, ".gitignore")
	if added, err := gitignore.Ensure(gitignorePath, cfg.Ignore); err != nil {
		logger.Warn(fmt.Sprintf("维护 .gitignore 失败: %v", err))
	} else if added > 0 {
		logger.Info(fmt.Sprintf("已向 .gitignore 追加 %d 条", added))
	}

	// 单实例锁：防止间隔内并发执行破坏仓库；被存活实例持有时静默跳过
	locker := lock.New(cfg.ResolveLockFile())
	acquired, release := locker.Acquire()
	if !acquired {
		logger.Info("已有同步实例在运行，跳过本次")
		return 0
	}
	defer release()

	result := syncer.Run()

	// 持久化状态（供 status 命令读取）
	stateStore := state.New(cfg.ResolveStateFile())
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

// runInstall 注册系统定时任务，按配置间隔触发 `autosync sync --config <配置>`。
// 用宽松加载：允许在仓库目录尚未初始化时安装调度。
func runInstall(rest []string) int {
	fs := flag.NewFlagSet("autosync install", flag.ContinueOnError)
	configPath := fs.String("config", "", "配置文件路径")
	if err := fs.Parse(rest); err != nil {
		return 1
	}

	cfg, err := config.LoadLenient(resolveConfigPath(*configPath))
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ 配置加载失败: %v\n", err)
		return 1
	}

	binPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ 获取可执行文件路径失败: %v\n", err)
		return 1
	}
	resolvedConfig := resolveConfigPath(*configPath)

	if err := sched.NewScheduler().Install(binPath, resolvedConfig, cfg.IntervalDur); err != nil {
		fmt.Fprintf(os.Stderr, "❌ 安装调度失败: %v\n", err)
		return 1
	}
	fmt.Printf("✅ 已安装定时任务 %s：每 %s 执行一次\n", sched.TaskName, cfg.Interval)
	fmt.Printf("   命令: \"%s\" sync --config \"%s\"\n", binPath, resolvedConfig)
	return 0
}

// runUninstall 移除系统定时任务。
func runUninstall() int {
	if err := sched.NewScheduler().Uninstall(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ 移除调度失败: %v\n", err)
		return 1
	}
	fmt.Printf("✅ 已移除定时任务 %s\n", sched.TaskName)
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

	st, err := state.New(cfg.ResolveStateFile()).Load()
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

// resolveConfigPath 解析配置文件路径：--config 优先，否则用可执行文件同目录的 config.yaml。
// 获取二进制路径失败时退化为工作目录下的 config.yaml。
func resolveConfigPath(explicit string) string {
	if explicit != "" {
		return explicit
	}
	exePath, err := os.Executable()
	if err != nil {
		return "config.yaml"
	}
	return filepath.Join(filepath.Dir(exePath), "config.yaml")
}
