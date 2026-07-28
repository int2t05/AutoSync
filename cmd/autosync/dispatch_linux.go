// dispatch_linux.go Linux 无参数默认进入 daemon 守护（前台多任务同步，对齐 Win 托盘/macOS 引擎的常驻语义）。
//
//go:build !windows && !darwin

package main

// defaultRun 平台分流：Linux 无参数默认守护（autosync daemon）。
func defaultRun(rest []string) int { return runDaemon(rest) }
