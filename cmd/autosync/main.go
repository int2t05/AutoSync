// main.go 是 AutoSync 的入口，负责 CLI 分发与依赖装配。
// 当前（P2）：加载配置 → 初始化日志 → 维护 .gitignore → 构造 gitop/Syncer 执行单次同步 → 记录结果。
// 子命令分发、通知、调度、dry-run 在后续里程碑实现。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"autosync/internal/config"
	"autosync/internal/gitignore"
	"autosync/internal/gitop"
	"autosync/internal/log"
	"autosync/internal/sync"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// run 执行主流程，返回退出码。拆分出来便于集中错误处理与未来测试。
func run(args []string) int {
	fs := flag.NewFlagSet("autosync", flag.ContinueOnError)
	configPath := fs.String("config", "", "配置文件路径（默认为可执行文件同目录的 config.yaml）")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	path := resolveConfigPath(*configPath)

	// 加载并校验配置：失败立即退出（US-001）
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ 配置加载失败: %v\n", err)
		return 1
	}

	// 初始化日志：失败立即退出
	logger, err := log.New(cfg.ResolveLogFile(), cfg.ShowConsole)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ 日志初始化失败: %v\n", err)
		return 1
	}
	defer logger.Close()

	logger.Info(fmt.Sprintf("AutoSync 启动 | 目录=%s | 远程=%s | 分支=%s | 策略=%s | 间隔=%s",
		cfg.RepoDir, cfg.RemoteURL, cfg.Branch, cfg.ConflictStrategy, cfg.Interval))

	// 维护 .gitignore：仅追加缺失条目，绝不覆盖已有内容（US-010）
	gitignorePath := filepath.Join(cfg.RepoDir, ".gitignore")
	if added, err := gitignore.Ensure(gitignorePath, cfg.Ignore); err != nil {
		logger.Warn(fmt.Sprintf("维护 .gitignore 失败: %v", err))
	} else if added > 0 {
		logger.Info(fmt.Sprintf("已向 .gitignore 追加 %d 条", added))
	}

	// 构造 git 操作器与同步器，执行单次同步
	gitOp := gitop.NewExecGit(cfg.RepoDir, logger)
	syncer := sync.NewSyncer(cfg, gitOp, logger)
	result := syncer.Run()

	if result.Outcome == sync.OutcomeFailed {
		logger.Error(fmt.Sprintf("同步失败: %s", result.Message))
		// TODO: P3 失败/冲突时发系统通知
		return 1
	}
	logger.Info(fmt.Sprintf("同步完成: %s — %s", result.Outcome, result.Message))
	// TODO: P3 按 Outcome 决定通知策略（成功静默、冲突/失败通知）
	// TODO: P4 支持子命令分发（sync/install/uninstall/status）与 dry-run
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
