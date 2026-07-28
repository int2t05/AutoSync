// dispatch_windows.go Windows 无参数默认进入托盘守护（Fyne 进程内）。
//
//go:build windows

package main

// defaultRun 平台分流：Windows 无参数默认托盘守护（双击即托盘）。
func defaultRun(rest []string) int { return runTray(rest) }
