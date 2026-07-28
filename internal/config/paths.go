// paths.go 解析 AutoSync 的用户数据目录与各类 byproduct 路径。
// 各平台用 os.UserConfigDir() 的原生路径（Win→%AppData%\AutoSync，
// macOS→~/Library/Application Support/AutoSync，Linux→~/.config/AutoSync），
// 使 exe 位置独立且符合各平台数据存放习惯。不兼容旧版 ~/.autosync/。
package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// userDataDirName 是用户数据目录名（位于各平台配置目录下）。
const userDataDirName = "AutoSync"

// UserDataDir 返回用户数据目录的绝对路径（纯计算，不创建目录）。
// 优先用 AUTOSYNC_DATA_DIR 环境变量覆盖（自定义数据目录 / 测试隔离）；
// 否则用 os.UserConfigDir() + AutoSync（各平台原生路径）；
// os.UserConfigDir 不可用时回退相对 "AutoSync"。
func UserDataDir() string {
	if env := os.Getenv("AUTOSYNC_DATA_DIR"); env != "" {
		return env
	}
	if cfg, err := os.UserConfigDir(); err == nil && cfg != "" {
		return filepath.Join(cfg, userDataDirName)
	}
	return userDataDirName
}

// EnsureUserDataDirs 创建 logs/state/locks 子目录，首次运行时调用。
func EnsureUserDataDirs() error {
	for _, sub := range []string{"logs", "state", "locks"} {
		if err := os.MkdirAll(filepath.Join(UserDataDir(), sub), 0755); err != nil {
			return fmt.Errorf("创建用户数据目录失败: %w", err)
		}
	}
	return nil
}

// LogFilePath 返回日志文件路径 <datadir>/logs/autosync.log（托盘守护与 CLI 共享，追加写）。
func LogFilePath() string {
	return filepath.Join(UserDataDir(), "logs", "autosync.log")
}

// StateFilePath 返回任务状态文件路径 <datadir>/state/autosync.state-<name>.json。
// name 应已安全化（由调用方负责，如 configstore.safeName）。
func StateFilePath(name string) string {
	return filepath.Join(UserDataDir(), "state", fmt.Sprintf("autosync.state-%s.json", name))
}

// LockFilePath 返回任务锁文件路径 <datadir>/locks/autosync.lock-<name>。
func LockFilePath(name string) string {
	return filepath.Join(UserDataDir(), "locks", fmt.Sprintf("autosync.lock-%s", name))
}

// DaemonLockPath 返回托盘守护单实例锁路径 <datadir>/locks/autosync.daemon.lock。
func DaemonLockPath() string {
	return filepath.Join(UserDataDir(), "locks", "autosync.daemon.lock")
}

// CLIConfigPath 返回 CLI 单任务配置路径 <datadir>/config.yaml。
func CLIConfigPath() string {
	return filepath.Join(UserDataDir(), "config.yaml")
}

// TrayConfigPath 返回托盘多任务配置路径 <datadir>/autosync.conf.yaml。
func TrayConfigPath() string {
	return filepath.Join(UserDataDir(), "autosync.conf.yaml")
}
