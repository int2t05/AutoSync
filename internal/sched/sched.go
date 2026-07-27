// sched.go 定义定时任务调度抽象与 schtasks 参数构造。
// 参数构造为纯函数（可单测，跨平台）；实际 schtasks 执行在 sched_windows.go，
// 非 Windows 平台返回 ErrNotImplemented（见 sched_other.go）。
package sched

import (
	"errors"
	"strconv"
	"time"
)

// ErrNotImplemented 表示当前平台未实现调度自安装。
var ErrNotImplemented = errors.New("调度自安装在当前平台未实现")

// TaskName 是注册的系统任务名（固定）。
const TaskName = "AutoSync"

// Scheduler 是定时任务调度抽象。
type Scheduler interface {
	// Install 注册按 interval 触发 `<binPath> sync --config <configPath>` 的定时任务。
	Install(binPath, configPath string, interval time.Duration) error
	// Uninstall 移除定时任务。
	Uninstall() error
}

// BuildInstallArgs 构造 schtasks /Create 参数（纯函数，可测）。
// interval < 1 分钟时钳制为 1（schtasks 最小粒度）。
func BuildInstallArgs(binPath, configPath string, interval time.Duration) []string {
	minutes := int(interval.Minutes())
	if minutes < 1 {
		minutes = 1
	}
	tr := "\"" + binPath + "\" sync --config \"" + configPath + "\""
	return []string{"/Create", "/TN", TaskName, "/TR", tr, "/SC", "MINUTE", "/MO", strconv.Itoa(minutes), "/F"}
}

// BuildUninstallArgs 构造 schtasks /Delete 参数（纯函数，可测）。
func BuildUninstallArgs() []string {
	return []string{"/Delete", "/TN", TaskName, "/F"}
}
