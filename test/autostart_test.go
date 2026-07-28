// autostart_test.go 验证开机自启启动命令构造（纯函数，跨平台可测）。
package tests

import (
	"testing"

	"autosync/internal/autostart"
)

// TestBuildRunCommand_DefaultConfig 验证缺省配置以后台模式启动托盘守护。
func TestBuildRunCommand_DefaultConfig(t *testing.T) {
	got := autostart.BuildRunCommand(`C:\AutoSync\AutoSync.exe`, "")
	want := `"C:\AutoSync\AutoSync.exe" tray --background`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestBuildRunCommand_WithConfig 验证显式配置追加 --config。
func TestBuildRunCommand_WithConfig(t *testing.T) {
	got := autostart.BuildRunCommand(`/opt/autosync/autosync`, `/etc/autosync.conf.yaml`)
	want := `"/opt/autosync/autosync" tray --background --config "/etc/autosync.conf.yaml"`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestAutostartAppName 验证自启标识固定为 AutoSync。
func TestAutostartAppName(t *testing.T) {
	if autostart.AppName != "AutoSync" {
		t.Fatalf("AppName=%q, want AutoSync", autostart.AppName)
	}
}
