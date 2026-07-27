// pidalive_windows.go 在 Windows 平台用 tasklist 判断进程存活。
//go:build windows

package lock

import (
	"os/exec"
	"strconv"
	"strings"
)

// pidAlive 判断进程是否存活（Windows：tasklist 查询）。
// 查询失败时保守视为存活，避免误接管导致并发。
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	out, err := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/NH", "/FO", "CSV").CombinedOutput()
	if err != nil {
		return true
	}
	// 存活时 tasklist 输出 CSV 行（以 " 开头）；不存在时输出 INFO 信息
	return strings.HasPrefix(strings.TrimSpace(string(out)), "\"")
}
