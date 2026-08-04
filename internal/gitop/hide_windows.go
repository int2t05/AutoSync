// hide_windows.go Windows 下隐藏 git 子进程的 console 窗口，避免同步时弹黑窗。
// 托盘/守护进程为 GUI 进程（-H windowsgui），其 fork 的子进程默认仍可能弹窗，需显式隐藏。
//
//go:build windows

package gitop

import (
	"os/exec"
	"syscall"
)

// CREATE_NO_WINDOW 阻止子进程创建控制台窗口（0x08000000）。
const CREATE_NO_WINDOW = 0x08000000

// applyHideWindow 配置 cmd 在 Windows 下不弹控制台窗口。
func applyHideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: CREATE_NO_WINDOW,
	}
}
