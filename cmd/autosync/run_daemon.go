// run_daemon.go 实现 daemon 子命令：Linux 主用法，前台多任务守护。
// 复用 setupTrayEnv + TaskScheduler + beeep；阻塞等 SIGINT/SIGTERM 优雅退出。
// 与 runTray 的区别：不启 GUI（无托盘），靠信号退出；同样获取 DaemonLock 单实例。
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"autosync/internal/config"
	"autosync/internal/lock"
	"autosync/internal/notify"
	"autosync/internal/tasksched"
)

// runDaemon 启动多任务守护：装配环境 → 守护级单实例锁 → 调度器 → 阻塞等信号。
// systemd user service 的 ExecStart 指向本子命令；Ctrl+C 或 `systemctl --user stop` 发 SIGTERM 优雅退出。
func runDaemon(rest []string) int {
	fs := flag.NewFlagSet("autosync daemon", flag.ContinueOnError)
	configPath := fs.String("config", "", "多任务配置文件路径（默认 ~/.autosync/autosync.conf.yaml）")
	if err := fs.Parse(rest); err != nil {
		return 1
	}

	store, logger, cleanup, err := setupTrayEnv(*configPath, "守护")
	if err != nil {
		return 1
	}
	defer cleanup()

	// 守护级单实例锁：防止多个 daemon 实例
	daemonLock := lock.New(config.DaemonLockPath())
	acquired, release := daemonLock.Acquire()
	if !acquired {
		logger.Info("已有 AutoSync 实例在运行，退出")
		fmt.Fprintln(os.Stderr, "❌ 已有 AutoSync 实例在运行")
		return 1
	}
	defer release()

	sched := tasksched.NewTaskScheduler(store.List(), logger, notify.NewBeeepNotifier(), nil)
	sched.Start()
	defer sched.Stop()

	logger.Info("AutoSync 守护运行中，等待退出信号（SIGINT/SIGTERM）")
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh
	logger.Info("收到退出信号，停止守护")
	// 第二次信号强制退出：优雅停止被挂起的 git 命令阻塞时（网络黑洞），不无限等待
	go func() {
		<-sigCh
		logger.Info("收到第二次退出信号，强制退出")
		os.Exit(1)
	}()
	return 0
}
