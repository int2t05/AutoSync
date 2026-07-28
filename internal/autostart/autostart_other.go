// autostart_other.go 非 Windows 开机自启桩：返回未实现。
// macOS 自启由 Swift 壳经 SMAppService 注册 Login Item 管理；Linux 预留（未来可用 systemd/cron）。
// 当前两平台行为一致，故共用一个桩；若未来 darwin 出现 Go 端实现或 linux 出现 systemd 支持再拆分。
//
//go:build !windows

package autostart

// Enable 非 Windows 未实现（macOS 由壳管 SMAppService，Linux 预留）。
func Enable(exePath, configPath string) error { return ErrNotImplemented }

// Disable 非 Windows 未实现。
func Disable() error { return ErrNotImplemented }

// IsEnabled 非 Windows 始终返回 false。
func IsEnabled() bool { return false }
