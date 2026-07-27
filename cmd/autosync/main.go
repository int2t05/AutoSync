// main.go 是 AutoSync 的入口，负责 CLI 分发与依赖装配。
// P1 阶段：解析配置路径、加载并校验配置、初始化日志、维护 .gitignore，
// 验证基础设施可用；同步逻辑与子命令分发在后续里程碑实现。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"autosync/internal/config"
	"autosync/internal/gitignore"
	"autosync/internal/log"
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

	// 加载并校验配置：失败立即退出，符合 US-001
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

	// TODO: 分发子命令（sync / install / uninstall / status）— P2/P4
	// TODO: 执行同步状态机（init / commit / fetch / rebase / push / 冲突处理）— P2/P3
	logger.Info("AutoSync P1 基础设施就绪（同步逻辑待 P2 实现）")
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
