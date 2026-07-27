// tasksched_test.go 验证任务调度：TaskRunner 复用 Syncer、TaskScheduler 定时触发与手动同步（真实 git，无 mock）。
// 测试用 UpToDate/Pushed 场景（成功静默，不触发系统通知）。
package tests

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"autosync/internal/config"
	"autosync/internal/configstore"
	"autosync/internal/gitignore"
	"autosync/internal/log"
	"autosync/internal/sync"
	"autosync/internal/tasksched"
)

// schedTask 构造已 Normalize 的调度任务。
func schedTask(t *testing.T, name, repoDir, remoteURL, interval string) *configstore.Task {
	t.Helper()
	task := &configstore.Task{Name: name, Config: config.Config{RepoDir: repoDir, RemoteURL: remoteURL, Interval: interval}}
	if err := task.Normalize(); err != nil {
		t.Fatalf("Normalize 任务 %s: %v", name, err)
	}
	return task
}

// schedLogger 创建无文件无控制台日志器。
func schedLogger(t *testing.T) *log.Logger {
	t.Helper()
	l, err := log.New("", false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(l.Close)
	return l
}

// waitStateFile 轮询等待任务状态文件出现（最多 d），返回是否出现。
func waitStateFile(task *configstore.Task, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(task.ResolveStateFile()); err == nil {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// presetGitignore 预先用任务 ignore 条目生成并提交 .gitignore，使 TaskRunner 的 Ensure 成为空操作。
// 这样 UpToDate 仓库 Run 后才仍是 NoChanges（否则 Ensure 新建 .gitignore → 变更 → Pushed）。
func presetGitignore(t *testing.T, task *configstore.Task, repo, remote string) {
	t.Helper()
	gitignore.Ensure(filepath.Join(repo, ".gitignore"), task.Ignore)
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-m", "gitignore")
	runGit(t, repo, "push", "origin", "main")
}

// TestTaskRunner_NoChanges 验证 UpToDate 仓库 → NoChanges，状态文件写入。
func TestTaskRunner_NoChanges(t *testing.T) {
	repo := makeWorkRepo(t)
	remote := makeBareRemote(t)
	addRemote(t, repo, "origin", remote)
	pushToRemote(t, repo, "origin", "main")

	task := schedTask(t, "nc", repo, remote, "1m")
	presetGitignore(t, task, repo, remote)
	result := tasksched.NewTaskRunner(task, schedLogger(t)).Run()
	if result.Outcome != sync.OutcomeNoChanges {
		t.Fatalf("Outcome=%s, 期望 NoChanges", result.Outcome)
	}
	if _, err := os.Stat(task.ResolveStateFile()); err != nil {
		t.Errorf("状态文件未写入: %v", err)
	}
}

// TestTaskRunner_Pushed 验证本地变更 → Pushed，远程收到更新。
func TestTaskRunner_Pushed(t *testing.T) {
	repo := makeWorkRepo(t)
	remote := makeBareRemote(t)
	addRemote(t, repo, "origin", remote)
	pushToRemote(t, repo, "origin", "main")
	writeFile(t, repo, "new.txt", "x")

	task := schedTask(t, "push", repo, remote, "1m")
	result := tasksched.NewTaskRunner(task, schedLogger(t)).Run()
	if result.Outcome != sync.OutcomePushed {
		t.Fatalf("Outcome=%s, 期望 Pushed", result.Outcome)
	}
	c := cloneRemote(t, remote)
	if !fileExists(t, filepath.Join(c, "new.txt")) {
		t.Errorf("远程未收到 new.txt")
	}
}

// TestTaskScheduler_StartStop 验证 Start 后定时触发写入状态文件，Stop 干净退出。
func TestTaskScheduler_StartStop(t *testing.T) {
	repo := makeWorkRepo(t)
	remote := makeBareRemote(t)
	addRemote(t, repo, "origin", remote)
	pushToRemote(t, repo, "origin", "main")

	task := schedTask(t, "tick", repo, remote, "50ms")
	presetGitignore(t, task, repo, remote)
	s := tasksched.NewTaskScheduler([]*configstore.Task{task}, schedLogger(t))
	s.Start()
	defer s.Stop()

	if !waitStateFile(task, 3*time.Second) {
		t.Fatalf("Start 后 3s 内未写入状态文件")
	}
}

// TestTaskScheduler_RunNow 验证手动同步立即触发，不存在任务返回错误。
func TestTaskScheduler_RunNow(t *testing.T) {
	repo := makeWorkRepo(t)
	remote := makeBareRemote(t)
	addRemote(t, repo, "origin", remote)
	pushToRemote(t, repo, "origin", "main")

	task := schedTask(t, "manual", repo, remote, "1m")
	presetGitignore(t, task, repo, remote)
	s := tasksched.NewTaskScheduler([]*configstore.Task{task}, schedLogger(t))
	result, err := s.RunNow("manual")
	if err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	if result.Outcome != sync.OutcomeNoChanges {
		t.Fatalf("Outcome=%s, 期望 NoChanges", result.Outcome)
	}
	if _, err := s.RunNow("nope"); err == nil {
		t.Errorf("不存在任务应返回错误")
	}
}

// TestTaskRunner_Pause 验证暂停标志的读写。
func TestTaskRunner_Pause(t *testing.T) {
	d := makeTempDir(t, "autosync-repo-*")
	task := schedTask(t, "unitpause", d, "u", "1m")
	r := tasksched.NewTaskRunner(task, schedLogger(t))
	if r.Paused() {
		t.Error("默认应非暂停")
	}
	r.SetPaused(true)
	if !r.Paused() {
		t.Error("SetPaused(true) 后应暂停")
	}
	r.SetPaused(false)
	if r.Paused() {
		t.Error("SetPaused(false) 后应非暂停")
	}
}

// TestTaskScheduler_PauseBlocks 验证暂停时 ticker 不触发（无状态文件），恢复后触发。
func TestTaskScheduler_PauseBlocks(t *testing.T) {
	repo := makeWorkRepo(t)
	remote := makeBareRemote(t)
	addRemote(t, repo, "origin", remote)
	pushToRemote(t, repo, "origin", "main")
	task := schedTask(t, "pause", repo, remote, "50ms")
	presetGitignore(t, task, repo, remote)

	s := tasksched.NewTaskScheduler([]*configstore.Task{task}, schedLogger(t))
	s.Runners()[0].SetPaused(true) // 启动前暂停
	s.Start()
	defer s.Stop()

	time.Sleep(300 * time.Millisecond) // 暂停期间不应写状态文件
	if _, err := os.Stat(task.ResolveStateFile()); err == nil {
		t.Errorf("暂停期间不应写状态文件")
	}
	s.SetPaused("pause", false) // 恢复
	if !waitStateFile(task, 2*time.Second) {
		t.Errorf("恢复后应写状态文件")
	}
}

// TestTaskScheduler_Reload 验证热重载：Reload 加入新任务后新任务被调度。
func TestTaskScheduler_Reload(t *testing.T) {
	repo1 := makeWorkRepo(t)
	remote1 := makeBareRemote(t)
	addRemote(t, repo1, "origin", remote1)
	pushToRemote(t, repo1, "origin", "main")
	task1 := schedTask(t, "r1", repo1, remote1, "1m")
	presetGitignore(t, task1, repo1, remote1)

	s := tasksched.NewTaskScheduler([]*configstore.Task{task1}, schedLogger(t))
	s.Start()
	defer s.Stop()
	if !waitStateFile(task1, 2*time.Second) {
		t.Fatalf("task1 初始未运行")
	}

	repo2 := makeWorkRepo(t)
	remote2 := makeBareRemote(t)
	addRemote(t, repo2, "origin", remote2)
	pushToRemote(t, repo2, "origin", "main")
	task2 := schedTask(t, "r2", repo2, remote2, "1m")
	presetGitignore(t, task2, repo2, remote2)

	s.Reload([]*configstore.Task{task1, task2})
	if !waitStateFile(task2, 3*time.Second) {
		t.Errorf("Reload 后 task2 未运行")
	}
}
