// daemon_test.go 验证 daemon 子命令：真实子进程启动 TaskScheduler + 单实例锁（真实 git，无 mock）。
// 复用 engine_test.go 的 engineBin/writeTrayConfig 与 git_helper_test.go/tasksched_test.go 夹具。
package tests

import (
	"os/exec"
	"testing"
	"time"

	"autosync/internal/configstore"
)

// startDaemon 启动 daemon 子进程；测试结束自动 kill 清理。
func startDaemon(t *testing.T, configPath string) *exec.Cmd {
	t.Helper()
	bin := engineBin(t)
	cmd := exec.Command(bin, "daemon", "--config", configPath)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		cmd.Wait()
	})
	return cmd
}

// TestDaemon_StartsAndSyncs 验证 daemon 启动后 TaskScheduler 立即触发首次同步并写状态文件。
func TestDaemon_StartsAndSyncs(t *testing.T) {
	repo := makeWorkRepo(t)
	remote := makeBareRemote(t)
	addRemote(t, repo, "origin", remote)
	pushToRemote(t, repo, "origin", "main")
	task := schedTask(t, "docs", repo, remote, "1m")
	presetGitignore(t, task, repo, remote)
	startDaemon(t, writeTrayConfig(t, []*configstore.Task{task}))
	if !waitStateFile(task, 3*time.Second) {
		t.Fatalf("daemon 启动后 3s 内未写状态文件")
	}
}

// TestDaemon_SingleInstance 验证 DaemonLock：第二实例应非零退出。
func TestDaemon_SingleInstance(t *testing.T) {
	repo := makeWorkRepo(t)
	remote := makeBareRemote(t)
	addRemote(t, repo, "origin", remote)
	pushToRemote(t, repo, "origin", "main")
	task := schedTask(t, "si", repo, remote, "1m")
	presetGitignore(t, task, repo, remote)
	cfgPath := writeTrayConfig(t, []*configstore.Task{task})
	startDaemon(t, cfgPath) // 首实例持 DaemonLock
	if !waitStateFile(task, 3*time.Second) {
		t.Fatalf("首实例未启动")
	}
	second := exec.Command(engineBin(t), "daemon", "--config", cfgPath)
	if err := second.Run(); err == nil {
		t.Errorf("第二实例应非零退出（单实例锁）")
	}
}
