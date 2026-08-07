// pidalive_windows.go 提供 Windows 平台的进程存活判定与启动时间读取。
//go:build windows

package lock

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// pidAlive 判断进程是否存活（Windows：tasklist 查询，3s 超时防探测命令自身卡死）。
// 超时视为存活（保守，避免误接管导致并发破坏）；tasklist 本身不可用（命令缺失等）视为死亡，
// 允许接管——否则持有者恒被误判存活导致任务永久跳过（PID 复用已由启动时间戳兜底）。
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/NH", "/FO", "CSV").CombinedOutput()
	if err != nil {
		return ctx.Err() == nil // 命令失败非超时：tasklist 不可用，视为死亡允许接管
	}
	// 存活时 tasklist 输出 CSV 行（以 " 开头）；不存在时输出 INFO 信息
	return strings.HasPrefix(strings.TrimSpace(string(out)), "\"")
}

// processStartTime 返回进程启动时间（Windows：OpenProcess + GetProcessTimes 读创建时间）。
func processStartTime(pid int) time.Time {
	h, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return time.Time{}
	}
	defer syscall.CloseHandle(h)
	var ct syscall.Filetime
	if err := syscall.GetProcessTimes(h, &ct, &syscall.Filetime{}, &syscall.Filetime{}, &syscall.Filetime{}); err != nil {
		return time.Time{}
	}
	return time.Unix(0, ct.Nanoseconds())
}
