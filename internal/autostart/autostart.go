// autostart.go 开机自启的跨平台抽象与共享常量。
// Windows 写注册表 Run 键（autostart_windows.go）；Linux 写 systemd user service（autostart_linux.go）；
// macOS 为桩（autostart_darwin.go，由 Swift 壳 SMAppService 管理）。
// 命令构造 BuildRunCommand 按平台实现（各平台文件），供 install 打印与单测验证。
package autostart

import "errors"

// AppName 注册表值名 / systemd unit 名 / 自启标识（固定）。
const AppName = "AutoSync"

// ErrNotImplemented 表示当前平台未实现开机自启。
var ErrNotImplemented = errors.New("开机自启在当前平台未实现")
