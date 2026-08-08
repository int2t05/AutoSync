// paths.go 解析 AutoSync 的用户数据目录与各类 byproduct 路径。
// 统一用用户主目录下 ~/.autosync/（Win→C:\Users\<user>\.autosync，macOS/Linux→~/.autosync），
// 跨平台一致、用户易访问、exe 位置独立。
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// dataDirName 是用户主目录下的数据目录名。
const dataDirName = ".autosync"

// UserDataDir 返回用户数据目录的绝对路径（纯计算，不创建目录）。
// 优先用 AUTOSYNC_DATA_DIR 环境变量覆盖（自定义数据目录 / 测试隔离）；
// 否则用 os.UserHomeDir() + .autosync（~/.autosync，跨平台统一）；
// os.UserHomeDir 不可用时回退相对 ".autosync"。
func UserDataDir() string {
	if env := os.Getenv("AUTOSYNC_DATA_DIR"); env != "" {
		return env
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, dataDirName)
	}
	return dataDirName
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

// LogFilePath 返回日志文件路径 ~/.autosync/logs/autosync.log（守护与 CLI 共享，追加写）。
func LogFilePath() string {
	return filepath.Join(UserDataDir(), "logs", "autosync.log")
}

// StateFilePath 返回任务状态文件路径 ~/.autosync/state/autosync.state-<name>.json。
// name 经 SafeName 规整，杜绝含路径分隔符 / .. 的名称越界写。
func StateFilePath(name string) string {
	return filepath.Join(UserDataDir(), "state", fmt.Sprintf("autosync.state-%s.json", SafeName(name)))
}

// LockFilePath 返回任务锁文件路径 ~/.autosync/locks/autosync.lock-<name>。
func LockFilePath(name string) string {
	return filepath.Join(UserDataDir(), "locks", fmt.Sprintf("autosync.lock-%s", SafeName(name)))
}

// unsafeNameRe 匹配文件名不安全字符。
var unsafeNameRe = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// SafeName 把任务名转为文件名安全形式：非字母数字/下划线/连字符替换为 _，去首尾 ._，空串回退 default。
// 为 byproduct 路径的单一安全边界（判重/查找/路径解析统一使用），防越界写。
func SafeName(name string) string {
	s := unsafeNameRe.ReplaceAllString(name, "_")
	s = strings.Trim(s, "._")
	if s == "" {
		s = "default"
	}
	return s
}

// DaemonLockPath 返回守护单实例锁路径 ~/.autosync/locks/autosync.daemon.lock。
func DaemonLockPath() string {
	return filepath.Join(UserDataDir(), "locks", "autosync.daemon.lock")
}

// TrayConfigPath 返回多任务配置路径 ~/.autosync/autosync.conf.yaml。
func TrayConfigPath() string {
	return filepath.Join(UserDataDir(), "autosync.conf.yaml")
}
