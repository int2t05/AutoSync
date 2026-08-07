// engine_test.go 验证 engine 子命令的 IPC 协议：真实子进程 + stdin/stdout JSON + 真实 git 仓库。
// 复用 git_helper_test.go 与 tasksched_test.go 的夹具（makeWorkRepo/schedTask 等）。
package tests

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"autosync/internal/config"
	"autosync/internal/configstore"
	"autosync/internal/engine"
)

// engineBinOnce 保证只构建一次 autosync 二进制供 engine 子进程测试复用。
var (
	engineBinOnce sync.Once
	engineBinPath string
	engineBinErr  error
)

// engineBin 构建并返回 autosync 二进制路径；构建失败则 skip（如缺 go 工具链）。
func engineBin(t *testing.T) string {
	t.Helper()
	engineBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "autosync-engine-*")
		if err != nil {
			engineBinErr = err
			return
		}
		name := "autosync-test"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		engineBinPath = filepath.Join(dir, name)
		// go test 的 cwd 为 test/，仓库根是其父目录
		cmd := exec.Command("go", "build", "-o", engineBinPath, "./cmd/autosync")
		cmd.Dir = ".."
		if out, err := cmd.CombinedOutput(); err != nil {
			engineBinErr = fmt.Errorf("go build: %v\n%s", err, out)
		}
	})
	if engineBinErr != nil {
		t.Skipf("跳过（无法构建 engine 二进制）: %v", engineBinErr)
	}
	return engineBinPath
}

// startEngine 启动 engine 子进程，返回 stdin 写入器与 stdout 行读取器；测试结束自动清理。
func startEngine(t *testing.T, configPath string) (io.WriteCloser, *bufio.Reader) {
	t.Helper()
	bin := engineBin(t)
	cmd := exec.Command(bin, "engine", "--config", configPath)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		cmd.Wait()
	})
	return stdin, bufio.NewReader(stdout)
}

// sendCmd 向引擎 stdin 写一行 JSON 命令。
func sendCmd(t *testing.T, stdin io.Writer, cmd engine.Command) {
	t.Helper()
	b, err := json.Marshal(cmd)
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	if _, err := stdin.Write(b); err != nil {
		t.Fatal(err)
	}
}

// readEvent 读一行 JSON 事件。
func readEvent(t *testing.T, r *bufio.Reader) engine.Event {
	t.Helper()
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("读事件失败: %v", err)
	}
	var ev engine.Event
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatalf("解析事件 %q: %v", line, err)
	}
	return ev
}

// readEventMatch 读事件直到 match 返回 true，跳过无关事件（如 ticker 触发的无 id sync-result）。
func readEventMatch(t *testing.T, r *bufio.Reader, match func(engine.Event) bool) engine.Event {
	t.Helper()
	for {
		ev := readEvent(t, r)
		if match(ev) {
			return ev
		}
	}
}

// writeTrayConfig 把任务写入临时数据目录的 autosync.conf.yaml，返回路径并隔离 AUTOSYNC_DATA_DIR。
func writeTrayConfig(t *testing.T, tasks []*configstore.Task) string {
	t.Helper()
	t.Setenv("AUTOSYNC_DATA_DIR", t.TempDir())
	path := config.TrayConfigPath()
	store := configstore.NewStore(path)
	for _, task := range tasks {
		if err := store.Add(task); err != nil {
			t.Fatalf("Add 任务 %s: %v", task.Name, err)
		}
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestEngine_Ready 验证启动首事件为 ready，含 logPath/dataDir/tasks。
func TestEngine_Ready(t *testing.T) {
	stdin, stdout := startEngine(t, writeTrayConfig(t, nil))
	defer stdin.Close()
	ev := readEvent(t, stdout)
	if ev.Event != "ready" {
		t.Fatalf("event=%s, 期望 ready", ev.Event)
	}
	if ev.LogPath == "" {
		t.Error("ready 应含 logPath")
	}
	if ev.DataDir == "" {
		t.Error("ready 应含 dataDir")
	}
}

// TestEngine_Status 验证 status 命令返回任务列表。
func TestEngine_Status(t *testing.T) {
	repo := makeWorkRepo(t)
	remote := makeBareRemote(t)
	addRemote(t, repo, "origin", remote)
	pushToRemote(t, repo, "origin", "main")
	task := schedTask(t, "st", repo, remote, "1m")
	presetGitignore(t, task, repo, remote)
	stdin, stdout := startEngine(t, writeTrayConfig(t, []*configstore.Task{task}))
	defer stdin.Close()
	readEvent(t, stdout) // ready
	sendCmd(t, stdin, engine.Command{ID: 1, Cmd: "status"})
	ev := readEventMatch(t, stdout, func(e engine.Event) bool { return e.ID == 1 })
	if ev.Event != "status" || len(ev.Tasks) != 1 || ev.Tasks[0].Name != "st" {
		t.Fatalf("event=%+v", ev)
	}
}

// TestEngine_SyncNow 验证手动同步返回带 id 的 sync-result。
func TestEngine_SyncNow(t *testing.T) {
	repo := makeWorkRepo(t)
	remote := makeBareRemote(t)
	addRemote(t, repo, "origin", remote)
	pushToRemote(t, repo, "origin", "main")
	task := schedTask(t, "docs", repo, remote, "1m")
	presetGitignore(t, task, repo, remote)
	stdin, stdout := startEngine(t, writeTrayConfig(t, []*configstore.Task{task}))
	defer stdin.Close()
	readEvent(t, stdout) // ready
	sendCmd(t, stdin, engine.Command{ID: 2, Cmd: "sync-now", Task: "docs"})
	ev := readEventMatch(t, stdout, func(e engine.Event) bool { return e.ID == 2 })
	if ev.Event != "sync-result" || ev.Task != "docs" {
		t.Fatalf("event=%+v", ev)
	}
}

// TestEngine_PauseResume 验证暂停/恢复事件，暂停后 status 反映 paused。
func TestEngine_PauseResume(t *testing.T) {
	repo := makeWorkRepo(t)
	remote := makeBareRemote(t)
	addRemote(t, repo, "origin", remote)
	pushToRemote(t, repo, "origin", "main")
	task := schedTask(t, "pr", repo, remote, "1m")
	presetGitignore(t, task, repo, remote)
	stdin, stdout := startEngine(t, writeTrayConfig(t, []*configstore.Task{task}))
	defer stdin.Close()
	readEvent(t, stdout) // ready
	sendCmd(t, stdin, engine.Command{ID: 1, Cmd: "pause", Task: "pr"})
	if ev := readEventMatch(t, stdout, func(e engine.Event) bool { return e.ID == 1 }); ev.Event != "paused" {
		t.Fatalf("event=%+v", ev)
	}
	sendCmd(t, stdin, engine.Command{ID: 2, Cmd: "status"})
	ev := readEventMatch(t, stdout, func(e engine.Event) bool { return e.ID == 2 })
	if ev.Event != "status" || len(ev.Tasks) != 1 || !ev.Tasks[0].Paused {
		t.Fatalf("暂停后 status 应反映 paused: %+v", ev)
	}
}

// TestEngine_ConfigSave 验证 config-save 写入配置文件并回 config-saved。
func TestEngine_ConfigSave(t *testing.T) {
	repo := makeWorkRepo(t)
	remote := makeBareRemote(t)
	addRemote(t, repo, "origin", remote)
	pushToRemote(t, repo, "origin", "main")
	cfgPath := writeTrayConfig(t, nil) // 空配置启动
	stdin, stdout := startEngine(t, cfgPath)
	defer stdin.Close()
	readEvent(t, stdout) // ready
	dto := &engine.TaskDTO{
		Name:   "saved",
		Config: config.Config{RepoDir: repo, RemoteURL: remote, Branch: "main", Interval: "1m", ConflictStrategy: "local_wins"},
	}
	sendCmd(t, stdin, engine.Command{ID: 1, Cmd: "config-save", Tasks: []*engine.TaskDTO{dto}})
	ev := readEventMatch(t, stdout, func(e engine.Event) bool { return e.ID == 1 })
	if ev.Event != "config-saved" {
		t.Fatalf("event=%+v", ev)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "saved") {
		t.Errorf("配置文件未含新任务: %s", data)
	}
}

// TestEngine_ConfigSave_Fail_Rollback 验证 config-save 落盘失败时回 error 事件且内存态回滚
// （store 仍为旧任务）。用 in-process 引擎（os.Pipe 驱动）以便失败后直接检查 store 状态。
func TestEngine_ConfigSave_Fail_Rollback(t *testing.T) {
	repo1 := makeWorkRepo(t)
	remote1 := makeBareRemote(t)
	addRemote(t, repo1, "origin", remote1)
	pushToRemote(t, repo1, "origin", "main")
	keep := schedTask(t, "keep", repo1, remote1, "1m")
	presetGitignore(t, keep, repo1, remote1)

	// store 配置路径指向目录：Save 的 os.Rename 替换目录必失败 → 触发回滚
	cfgPath := filepath.Join(makeTempDir(t, "autosync-cfg-*"), "autosync.conf.yaml")
	if err := os.Mkdir(cfgPath, 0755); err != nil {
		t.Fatal(err)
	}
	store := configstore.NewStore(cfgPath)
	if err := store.Add(keep); err != nil {
		t.Fatalf("Add keep: %v", err)
	}

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdinW.Close()
	defer stdoutR.Close()
	defer stdoutW.Close()
	done := make(chan int, 1)
	go func() { done <- engine.New(store, schedLogger(t), stdinR, stdoutW).Run() }()

	// config-save 新增任务：ReplaceAll 通过、Save 失败 → error 事件
	repo2 := makeWorkRepo(t)
	remote2 := makeBareRemote(t)
	addRemote(t, repo2, "origin", remote2)
	pushToRemote(t, repo2, "origin", "main")
	dto := &engine.TaskDTO{
		Name:   "new",
		Config: config.Config{RepoDir: repo2, RemoteURL: remote2, Branch: "main", Interval: "1m", ConflictStrategy: "conflict_files"},
	}
	sendCmd(t, stdinW, engine.Command{ID: 1, Cmd: "config-save", Tasks: []*engine.TaskDTO{dto}})
	ev := readEventMatch(t, bufio.NewReader(stdoutR), func(e engine.Event) bool { return e.ID == 1 })
	if ev.Event != "error" {
		t.Fatalf("config-save 落盘失败应回 error 事件, got %+v", ev)
	}

	// 回滚后 store 仍为旧任务
	list := store.List()
	if len(list) != 1 || list[0].Name != "keep" {
		t.Errorf("回滚后任务列表应保持旧任务, got %+v", list)
	}

	// 收尾：退出引擎，等待 goroutine 结束
	sendCmd(t, stdinW, engine.Command{Cmd: "quit"})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("引擎退出超时")
	}
}

// TestEngine_Quit 验证 quit 命令回 bye 并退出。
func TestEngine_Quit(t *testing.T) {
	stdin, stdout := startEngine(t, writeTrayConfig(t, nil))
	readEvent(t, stdout) // ready
	sendCmd(t, stdin, engine.Command{Cmd: "quit"})
	if ev := readEvent(t, stdout); ev.Event != "bye" {
		t.Fatalf("event=%s, 期望 bye", ev.Event)
	}
}

// TestEngine_StdoutBlocked_Continues 验证壳不读 stdout（管道写满）时引擎仍能处理 quit 退出，
// 事件被丢弃而非阻塞命令循环（in-process，os.Pipe 驱动）。
func TestEngine_StdoutBlocked_Continues(t *testing.T) {
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdinW.Close()
	defer stdoutR.Close()
	defer stdoutW.Close()

	store := configstore.NewStore(filepath.Join(t.TempDir(), "autosync.conf.yaml"))
	done := make(chan int, 1)
	go func() { done <- engine.New(store, schedLogger(t), stdinR, stdoutW).Run() }()

	// 海量 status 命令（stdin 行均小、不触发 Scanner 上限）累计产生 > 64KB 的 stdout 事件：
	// 测试不读 stdoutR，管道写满后事件应被丢弃而非阻塞引擎；quit 仍须被处理使引擎退出。
	status, _ := json.Marshal(engine.Command{Cmd: "status"})
	for i := 0; i < 2000; i++ {
		if _, err := stdinW.Write(append(status, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	quit, _ := json.Marshal(engine.Command{Cmd: "quit"})
	if _, err := stdinW.Write(append(quit, '\n')); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("stdout 写满时引擎应仍能退出（事件丢弃而非阻塞）")
	}
}

// TestEngine_LargeConfigSave 验证 config-save 携带超 64KB 的单行配置 JSON 时不被 Scanner 上限截断，
// 引擎仍回 config-saved 而非静默退出（此前 Scanner 默认 64KB 上限致主循环退出且无 bye）。
func TestEngine_LargeConfigSave(t *testing.T) {
	repo := makeWorkRepo(t)
	remote := makeBareRemote(t)
	addRemote(t, repo, "origin", remote)
	pushToRemote(t, repo, "origin", "main")
	stdin, stdout := startEngine(t, writeTrayConfig(t, nil))
	defer stdin.Close()
	readEvent(t, stdout) // ready

	// 用超大 ignore 条目撑大 config-save 单行 JSON 超过 64KB
	big := strings.Repeat("x", 80*1024)
	dto := &engine.TaskDTO{
		Name:   "big",
		Config: config.Config{RepoDir: repo, RemoteURL: remote, Branch: "main", Interval: "1m", ConflictStrategy: "conflict_files", Ignore: []string{big}},
	}
	sendCmd(t, stdin, engine.Command{ID: 1, Cmd: "config-save", Tasks: []*engine.TaskDTO{dto}})
	ev := readEventMatch(t, stdout, func(e engine.Event) bool { return e.ID == 1 })
	if ev.Event != "config-saved" {
		t.Fatalf("大配置应回 config-saved, got %+v", ev)
	}
}
