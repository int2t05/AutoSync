// paths.go 解析 AutoSync 的用户数据目录与各类 byproduct 路径。
// 所有 byproduct 统一存放于 ~/.autosync/（按 logs/state/locks 子目录管理），
// 使 exe 位置独立——可装进只读目录、可在任意位置打开。
package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// userDataDirName 是用户数据目录名（位于 home 下）。
const userDataDirName = ".autosync"

// UserDataDir 返回用户数据目录的绝对路径（纯计算，不创建目录）。
// 优先用 AUTOSYNC_DATA_DIR 环境变量覆盖（自定义数据目录 / 测试隔离）；
// 否则默认 ~/.autosync。os.UserHomeDir 失败时退化为相对路径 ".autosync"。
func UserDataDir() string {
	if env := os.Getenv("AUTOSYNC_DATA_DIR"); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return userDataDirName
	}
	return filepath.Join(home, userDataDirName)
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

// LogFilePath 返回日志文件路径 ~/.autosync/logs/autosync.log（托盘守护与 CLI 共享，追加写）。
func LogFilePath() string {
	return filepath.Join(UserDataDir(), "logs", "autosync.log")
}

// StateFilePath 返回任务状态文件路径 ~/.autosync/state/autosync.state-<name>.json。
// name 应已安全化（由调用方负责，如 configstore.safeName）。
func StateFilePath(name string) string {
	return filepath.Join(UserDataDir(), "state", fmt.Sprintf("autosync.state-%s.json", name))
}

// LockFilePath 返回任务锁文件路径 ~/.autosync/locks/autosync.lock-<name>。
func LockFilePath(name string) string {
	return filepath.Join(UserDataDir(), "locks", fmt.Sprintf("autosync.lock-%s", name))
}

// DaemonLockPath 返回托盘守护单实例锁路径 ~/.autosync/locks/autosync.daemon.lock。
func DaemonLockPath() string {
	return filepath.Join(UserDataDir(), "locks", "autosync.daemon.lock")
}

// CLIConfigPath 返回 CLI 单任务配置路径 ~/.autosync/config.yaml。
func CLIConfigPath() string {
	return filepath.Join(UserDataDir(), "config.yaml")
}

// TrayConfigPath 返回托盘多任务配置路径 ~/.autosync/autosync.conf.yaml。
func TrayConfigPath() string {
	return filepath.Join(UserDataDir(), "autosync.conf.yaml")
}
