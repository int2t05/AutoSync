// autostart_linux.go Linux 开机自启桩：返回未实现（预留，未来可用 systemd/cron）。
//
//go:build !windows && !darwin

package autostart

// Enable Linux 未实现（预留）。
func Enable(exePath, configPath string) error { return ErrNotImplemented }

// Disable Linux 未实现（预留）。
func Disable() error { return ErrNotImplemented }

// IsEnabled Linux 始终返回 false。
func IsEnabled() bool { return false }
