// sched_other.go 在非 Windows 平台提供 Scheduler 桩，返回 ErrNotImplemented。
//go:build !windows

package sched

import "time"

// stubScheduler 非 Windows 平台的调度桩。
// TODO: 实现 launchd（macOS）/ cron（Linux）调度自安装
type stubScheduler struct{}

// NewScheduler 创建当前平台的调度器（非 Windows：桩）。
func NewScheduler() Scheduler {
	return &stubScheduler{}
}

func (s *stubScheduler) Install(binPath, configPath string, interval time.Duration) error {
	return ErrNotImplemented
}

func (s *stubScheduler) Uninstall() error {
	return ErrNotImplemented
}
