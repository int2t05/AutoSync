// dispatch_linux.go Linux 无参数默认 CLI 一次性同步（预留，无 GUI 守护）。
//
//go:build !windows && !darwin

package main

// defaultRun 平台分流：Linux 无参数默认 CLI 同步（预留）。
func defaultRun(rest []string) int { return runSync(rest) }
