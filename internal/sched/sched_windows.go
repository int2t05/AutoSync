// sched_windows.go 用 schtasks 实现 Scheduler（注册/移除 Windows 定时任务）。
//go:build windows

package sched

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// schtasksScheduler 通过 schtasks 命令管理 Windows 定时任务。
type schtasksScheduler struct{}

// NewScheduler 创建当前平台的调度器（Windows：schtasks）。
func NewScheduler() Scheduler {
	return &schtasksScheduler{}
}

// Install 注册定时任务。/F 强制覆盖已存在的同名任务。
func (s *schtasksScheduler) Install(binPath, configPath string, interval time.Duration) error {
	out, err := exec.Command("schtasks", BuildInstallArgs(binPath, configPath, interval)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks 注册失败: %w | %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Uninstall 移除定时任务。
func (s *schtasksScheduler) Uninstall() error {
	out, err := exec.Command("schtasks", BuildUninstallArgs()...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks 移除失败: %w | %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
