// autostart.go 开机自启的跨平台抽象与共享常量。
// Windows 写注册表 Run 键（autostart_windows.go），非 Windows 为桩（autostart_other.go）。
// 命令构造为纯函数，供单测跨平台验证。
package autostart

import "errors"

// AppName 注册表值名 / 自启标识（固定）。
const AppName = "AutoSync"

// ErrNotImplemented 表示当前平台未实现开机自启。
var ErrNotImplemented = errors.New("开机自启在当前平台未实现")

// BuildRunCommand 构造开机自启的启动命令（纯函数，跨平台可测）。
// 以 --background 后台模式启动托盘守护（不弹配置窗口），避免每次登录弹窗；
// configPath 非空时追加 --config 指定配置路径，否则用默认配置。
func BuildRunCommand(exePath, configPath string) string {
	cmd := "\"" + exePath + "\" tray --background"
	if configPath != "" {
		cmd += " --config \"" + configPath + "\""
	}
	return cmd
}
