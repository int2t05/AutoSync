// autostart_test.go 验证开机自启启动命令构造（纯函数，按平台期望，覆盖含空格路径的引号行为）。
package tests

import (
	"runtime"
	"testing"

	"autosync/internal/autostart"
)

// TestBuildRunCommand_DefaultConfig 验证缺省配置的启动命令（Windows 后台托盘 / Linux daemon / macOS 空串）。
func TestBuildRunCommand_DefaultConfig(t *testing.T) {
	var exe, want string
	switch runtime.GOOS {
	case "windows":
		exe = `C:\Program Files\AutoSync\autosync.exe`
		want = `"C:\Program Files\AutoSync\autosync.exe" tray --background`
	case "darwin":
		exe = `/opt/autosync/autosync`
		want = ""
	default: // linux
		exe = `/opt/autosync/autosync`
		want = `"/opt/autosync/autosync" daemon`
	}
	if got := autostart.BuildRunCommand(exe, ""); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestBuildRunCommand_WithConfig 验证显式配置追加 --config（路径含空格时引号包裹）。
func TestBuildRunCommand_WithConfig(t *testing.T) {
	var exe, cfg, want string
	switch runtime.GOOS {
	case "windows":
		exe = `C:\Program Files\AutoSync\autosync.exe`
		cfg = `D:\My Config\autosync.conf.yaml`
		want = `"C:\Program Files\AutoSync\autosync.exe" tray --background --config "D:\My Config\autosync.conf.yaml"`
	case "darwin":
		exe = `/opt/autosync/autosync`
		cfg = `/etc/autosync.conf.yaml`
		want = ""
	default: // linux
		exe = `/opt/autosync/autosync`
		cfg = `/etc/autosync.conf.yaml`
		want = `"/opt/autosync/autosync" daemon --config "/etc/autosync.conf.yaml"`
	}
	if got := autostart.BuildRunCommand(exe, cfg); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestAutostartAppName 验证自启标识固定为 AutoSync。
func TestAutostartAppName(t *testing.T) {
	if autostart.AppName != "AutoSync" {
		t.Fatalf("AppName=%q, want AutoSync", autostart.AppName)
	}
}
