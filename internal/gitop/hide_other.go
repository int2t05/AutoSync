// hide_other.go 非 Windows 平台隐藏窗口的空实现（无控制台窗口概念）。
//
//go:build !windows

package gitop

import "os/exec"

// applyHideWindow 非 Windows 空操作。
func applyHideWindow(cmd *exec.Cmd) {}
