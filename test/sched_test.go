// sched_test.go 验证 schtasks 参数构造（纯函数，跨平台可测，不实际调用 schtasks）。
package tests

import (
	"strings"
	"testing"
	"time"

	"autosync/internal/sched"
)

// TestBuildInstallArgs 验证安装参数包含必要片段且命令行拼接正确。
func TestBuildInstallArgs(t *testing.T) {
	args := sched.BuildInstallArgs(`C:\autosync\autosync.exe`, `C:\autosync\config.yaml`, 1*time.Minute)
	joined := strings.Join(args, " ")

	for _, want := range []string{"/Create", "/TN " + sched.TaskName, "/SC MINUTE", "/MO 1", "/F"} {
		if !strings.Contains(joined, want) {
			t.Errorf("缺少参数片段 %q\n完整: %s", want, joined)
		}
	}
	if !strings.Contains(joined, `autosync.exe" sync --config "C:\autosync\config.yaml`) {
		t.Errorf("/TR 命令行构造不正确: %s", joined)
	}
}

// TestBuildInstallArgs_ClampMinutes 验证间隔小于 1 分钟时钳制为 /MO 1（schtasks 最小粒度）。
func TestBuildInstallArgs_ClampMinutes(t *testing.T) {
	args := sched.BuildInstallArgs("autosync", "config.yaml", 30*time.Second)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "/MO 1") {
		t.Errorf("小于 1 分钟应钳制为 /MO 1: %s", joined)
	}
}

// TestBuildInstallArgs_LargerInterval 验证较大间隔按分钟数填入 /MO。
func TestBuildInstallArgs_LargerInterval(t *testing.T) {
	args := sched.BuildInstallArgs("autosync", "config.yaml", 5*time.Minute)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "/MO 5") {
		t.Errorf("5 分钟应填 /MO 5: %s", joined)
	}
}

// TestBuildUninstallArgs 验证卸载参数包含必要片段。
func TestBuildUninstallArgs(t *testing.T) {
	args := sched.BuildUninstallArgs()
	joined := strings.Join(args, " ")
	for _, want := range []string{"/Delete", "/TN " + sched.TaskName, "/F"} {
		if !strings.Contains(joined, want) {
			t.Errorf("缺少参数片段 %q\n完整: %s", want, joined)
		}
	}
}
