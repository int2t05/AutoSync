// pidalive_unix.go 在 Unix 平台用 kill -0 判断进程存活。
//go:build !windows

package lock

import "syscall"

// pidAlive 判断进程是否存活（Unix：kill -0）。EPERM 表示进程存在但无权限，视为存活。
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
