// autostart_darwin.go macOS 开机自启桩：由 Swift 壳经 SMAppService 注册 Login Item 管理。
// Go 端不直接操作；install/uninstall 在 macOS 返回未实现（壳负责）。
//
//go:build darwin

package autostart

// Enable macOS 未实现（壳管 SMAppService）。
func Enable(exePath, configPath string) error { return ErrNotImplemented }

// Disable macOS 未实现。
func Disable() error { return ErrNotImplemented }

// IsEnabled macOS 始终返回 false（壳管）。
func IsEnabled() bool { return false }

// BuildRunCommand macOS 返回空串（自启由壳 SMAppService 管理，无命令行）。
func BuildRunCommand(exePath, configPath string) string { return "" }
