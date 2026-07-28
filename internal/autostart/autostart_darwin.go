// autostart_darwin.go macOS 开机自启桩：返回未实现。
// macOS 自启由 Swift 壳经 SMAppService 注册 Login Item 管理，Go 侧不直接实现。
//
//go:build darwin

package autostart

// Enable macOS 未实现（由 Swift 壳 SMAppService 管理）。
func Enable(exePath, configPath string) error { return ErrNotImplemented }

// Disable macOS 未实现（由 Swift 壳管理）。
func Disable() error { return ErrNotImplemented }

// IsEnabled macOS 始终返回 false（自启状态由壳持有）。
func IsEnabled() bool { return false }
