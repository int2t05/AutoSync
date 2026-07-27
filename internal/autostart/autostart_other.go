// autostart_other.go 非 Windows 平台开机自启桩：返回未实现。
// V1.1 自启聚焦 Windows 注册表；macOS/Linux 由用户用系统调度器（launchd/cron）配置 CLI 一次性同步。
//
//go:build !windows

package autostart

// Enable 非 Windows 未实现。
func Enable(exePath, configPath string) error { return ErrNotImplemented }

// Disable 非 Windows 未实现。
func Disable() error { return ErrNotImplemented }

// IsEnabled 非 Windows 始终返回 false。
func IsEnabled() bool { return false }
