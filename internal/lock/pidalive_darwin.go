// pidalive_darwin.go 提供 darwin 平台的进程存活判定与启动时间读取。
//go:build darwin

package lock

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// pidAlive 判断进程是否存活（darwin：kill -0）。EPERM 表示进程存在但无权限，视为存活。
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// processStartTime 返回进程启动时间（darwin：sysctl kern.boottime + kern.proc.pid.<pid>.p_starttime）。
func processStartTime(pid int) time.Time {
	boot, err := parseTimeval(sysctl("kern.boottime"))
	if err != nil {
		return time.Time{}
	}
	start, err := parseTimeval(sysctl("kern.proc.pid." + strconv.Itoa(pid) + ".p_starttime"))
	if err != nil {
		return time.Time{}
	}
	return time.Unix(0, int64(boot)).Add(start)
}

// sysctl 执行 sysctl -n <key>，返回去空白输出。
func sysctl(key string) string {
	out, err := exec.Command("sysctl", "-n", key).CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// parseTimeval 解析 "sec usec" 形式的 timeval 输出为时长。
func parseTimeval(s string) (time.Duration, error) {
	fields := strings.Fields(s)
	if len(fields) < 2 {
		return 0, fmt.Errorf("无法解析 timeval: %q", s)
	}
	sec, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0, err
	}
	usec, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return 0, err
	}
	return time.Duration(sec)*time.Second + time.Duration(usec)*time.Microsecond, nil
}
