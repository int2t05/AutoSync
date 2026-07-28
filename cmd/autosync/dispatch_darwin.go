// dispatch_darwin.go macOS 无参数默认进入 engine 子进程模式（通常由 Swift 壳拉起）。
//
//go:build darwin

package main

// defaultRun 平台分流：macOS 无参数默认 engine（IPC 子进程，供 Swift 壳通信）。
func defaultRun(rest []string) int { return runEngine(rest) }
