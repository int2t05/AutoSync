// run_engine.go 实现 engine 子命令：macOS Swift 壳经 stdin/stdout JSON IPC 调用 Go 引擎。
// 装配运行时环境（setupTrayEnv）后启动 engine.Engine.Run()。
// 不获取守护级单实例锁：engine 模式下单实例由 Swift 壳（flock）负责，引擎只持各任务锁。
package main

import (
	"flag"
	"os"

	"autosync/internal/engine"
)

// runEngine 启动 IPC 引擎子进程，供 Swift 壳经 stdin/stdout JSON 通信。
func runEngine(rest []string) int {
	fs := flag.NewFlagSet("autosync engine", flag.ContinueOnError)
	configPath := fs.String("config", "", "托盘配置文件路径（默认 autosync.conf.yaml）")
	if err := fs.Parse(rest); err != nil {
		return 1
	}

	store, logger, cleanup, err := setupTrayEnv(*configPath, "engine")
	if err != nil {
		return 1
	}
	defer cleanup()

	// 不获取守护锁：engine 模式下单实例由 Swift 壳（flock）负责，引擎只持各任务锁
	return engine.New(store, logger, os.Stdin, os.Stdout).Run()
}
