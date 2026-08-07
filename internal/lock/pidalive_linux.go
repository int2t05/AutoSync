// pidalive_linux.go 提供 Linux 平台的进程存活判定与启动时间读取。
//go:build linux

package lock

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// pidAlive 判断进程是否存活（Linux：kill -0）。EPERM 表示进程存在但无权限，视为存活。
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// processStartTime 返回进程启动时间（Linux：/proc/<pid>/stat 第 22 字段 starttime + 系统 btime）。
func processStartTime(pid int) time.Time {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return time.Time{}
	}
	// 进程名可含空格/括号，从最后一个 ")" 后取字段
	rest := string(data)
	if i := strings.LastIndex(rest, ")"); i >= 0 {
		rest = rest[i+1:]
	}
	fields := strings.Fields(rest)
	// 去掉 "pid (comm) " 前缀后偏移 2：第 22 字段（starttime）对应 fields[19]
	if len(fields) < 20 {
		return time.Time{}
	}
	ticks, err := strconv.ParseInt(fields[19], 10, 64)
	if err != nil {
		return time.Time{}
	}
	// USER_HZ 通常为 100
	return bootTime().Add(time.Duration(ticks) * time.Second / 100)
}

// bootTime 读取 /proc/stat 的 btime（系统启动的 Unix 秒）。
func bootTime() time.Time {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "btime ") {
			sec, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "btime ")), 10, 64)
			if err != nil {
				return time.Time{}
			}
			return time.Unix(sec, 0)
		}
	}
	return time.Time{}
}
