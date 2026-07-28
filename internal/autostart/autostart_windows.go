// autostart_windows.go Windows 开机自启：写 HKCU\Software\Microsoft\Windows\CurrentVersion\Run。
// 登录时由系统执行该键值，启动托盘守护进程。
//
//go:build windows

package autostart

import (
	"errors"

	"golang.org/x/sys/windows/registry"
)

// runKeyPath 当前用户登录自启的注册表路径。
const runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`

// Enable 写注册表 Run 键，设置开机自启。exePath 为二进制路径，configPath 为可选配置路径。
func Enable(exePath, configPath string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(AppName, BuildRunCommand(exePath, configPath))
}

// Disable 移除注册表 Run 键的自启项。值不存在视为成功（重复 uninstall 不报错）。
func Disable() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.DeleteValue(AppName); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

// IsEnabled 返回自启项是否已注册。
func IsEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(AppName)
	return err == nil
}
