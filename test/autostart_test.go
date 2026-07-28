// autostart_test.go 验证开机自启启动命令构造（纯函数，按平台期望）。
package tests

import (
	"runtime"
	"testing"

	"autosync/internal/autostart"
)

// TestBuildRunCommand_DefaultConfig 验证缺省配置的启动命令（Windows 后台托盘 / Linux daemon / macOS 空串）。
func TestBuildRunCommand_DefaultConfig(t *testing.T) {
	got := autostart.BuildRunCommand(`/opt/autosync/autosync`, "")
	var want string
	switch runtime.GOOS {
	case "windows":
		want = `"/opt/autosync/autosync" tray --background`
	case "darwin":
		want = ""
	default: // linux
		want = `/opt/autosync/autosync daemon`
	}
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestBuildRunCommand_WithConfig 验证显式配置追加 --config。
func TestBuildRunCommand_WithConfig(t *testing.T) {
	got := autostart.BuildRunCommand(`/opt/autosync/autosync`, `/etc/autosync.conf.yaml`)
	var want string
	switch runtime.GOOS {
	case "windows":
		want = `"/opt/autosync/autosync" tray --background --config "/etc/autosync.conf.yaml"`
	case "darwin":
		want = ""
	default: // linux
		want = `/opt/autosync/autosync daemon --config /etc/autosync.conf.yaml`
	}
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
