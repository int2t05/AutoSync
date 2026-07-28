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
		Name: "saved", RepoDir: repo, RemoteURL: remote,
		Branch: "main", Interval: "1m", ConflictStrategy: "local_wins",
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

// TestEngine_Quit 验证 quit 命令回 bye 并退出。
func TestEngine_Quit(t *testing.T) {
	stdin, stdout := startEngine(t, writeTrayConfig(t, nil))
	readEvent(t, stdout) // ready
	sendCmd(t, stdin, engine.Command{Cmd: "quit"})
	if ev := readEvent(t, stdout); ev.Event != "bye" {
		t.Fatalf("event=%s, 期望 bye", ev.Event)
	}
}
