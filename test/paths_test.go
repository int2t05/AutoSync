// paths_test.go 验证用户数据目录与 byproduct 路径解析（纯函数，跨平台）。
package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"autosync/internal/config"
)

// TestUserDataDir_Override 验证 AUTOSYNC_DATA_DIR 覆盖默认 home 目录。
func TestUserDataDir_Override(t *testing.T) {
	t.Setenv("AUTOSYNC_DATA_DIR", "/custom/path")
	if got := config.UserDataDir(); got != "/custom/path" {
		t.Fatalf("UserDataDir=%q, 期望 /custom/path", got)
	}
}

// TestUserDataDir_Default 验证未设覆盖时用各平台原生配置目录 + AutoSync。
func TestUserDataDir_Default(t *testing.T) {
	t.Setenv("AUTOSYNC_DATA_DIR", "")
	cfg, err := os.UserConfigDir()
	if err != nil || cfg == "" {
		t.Skip("无法获取用户配置目录")
	}
	want := filepath.Join(cfg, "AutoSync")
	if got := config.UserDataDir(); got != want {
		t.Fatalf("UserDataDir=%q, 期望 %q", got, want)
	}
}

// TestStateLockFilePath 验证 state/lock 路径含正确子目录与文件名。
func TestStateLockFilePath(t *testing.T) {
	t.Setenv("AUTOSYNC_DATA_DIR", "/data")
	s := config.StateFilePath("alpha")
	if !strings.HasSuffix(s, filepath.Join("state", "autosync.state-alpha.json")) {
		t.Errorf("StateFilePath=%q", s)
	}
	l := config.LockFilePath("beta")
	if !strings.HasSuffix(l, filepath.Join("locks", "autosync.lock-beta")) {
		t.Errorf("LockFilePath=%q", l)
	}
	if g := config.LogFilePath(); !strings.HasSuffix(g, filepath.Join("logs", "autosync.log")) {
		t.Errorf("LogFilePath=%q", g)
	}
	if g := config.DaemonLockPath(); !strings.HasSuffix(g, filepath.Join("locks", "autosync.daemon.lock")) {
		t.Errorf("DaemonLockPath=%q", g)
	}
	if g := config.CLIConfigPath(); !strings.HasSuffix(g, "config.yaml") {
		t.Errorf("CLIConfigPath=%q", g)
	}
	if g := config.TrayConfigPath(); !strings.HasSuffix(g, "autosync.conf.yaml") {
		t.Errorf("TrayConfigPath=%q", g)
	}
}

// TestEnsureUserDataDirs 验证创建 logs/state/locks 子目录。
func TestEnsureUserDataDirs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AUTOSYNC_DATA_DIR", dir)
	if err := config.EnsureUserDataDirs(); err != nil {
		t.Fatalf("EnsureUserDataDirs: %v", err)
	}
	for _, sub := range []string{"logs", "state", "locks"} {
		if _, err := os.Stat(filepath.Join(dir, sub)); err != nil {
			t.Errorf("子目录 %s 未创建: %v", sub, err)
		}
	}
}
