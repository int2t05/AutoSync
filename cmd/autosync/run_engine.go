// run_engine.go 实现 engine 子命令：macOS Swift 壳经 stdin/stdout JSON IPC 调用 Go 引擎。
// 装配用户数据目录 + 日志 + 多任务配置 → engine.Engine.Run()。
// 不获取守护级单实例锁：engine 模式下单实例由 Swift 壳（flock）负责，引擎只持各任务锁。
package main

import (
	"flag"
	"fmt"
	"os"

	"autosync/internal/config"
	"autosync/internal/configstore"
	"autosync/internal/engine"
	"autosync/internal/log"
)

// runEngine 启动 IPC 引擎子进程，供 Swift 壳经 stdin/stdout JSON 通信。
func runEngine(rest []string) int {
	fs := flag.NewFlagSet("autosync engine", flag.ContinueOnError)
	configPath := fs.String("config", "", "托盘配置文件路径（默认 autosync.conf.yaml）")
	if err := fs.Parse(rest); err != nil {
		return 1
	}

	// 先确保用户数据目录与日志：engine 失败可写日志，便于壳侧诊断
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
	logger.Info(fmt.Sprintf("AutoSync engine 启动 | 任务数=%d", len(store.List())))

	return engine.New(store, logger, os.Stdin, os.Stdout).Run()
}
