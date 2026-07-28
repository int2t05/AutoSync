// engine.go 实现 engine 子命令的 IPC 主循环：Go 引擎子进程经 stdin/stdout JSON 与 Swift 壳通信。
// 壳发命令（status/sync-now/pause/resume/config-list/config-save/quit），引擎回事件。
// 启动即开始 TaskScheduler 调度；ticker 触发的结果经 onResult 回调上报 sync-result（无 id）；
// 通知经 ipcNotifier 委托壳（UNUserNotificationCenter）。日志写文件，ready 事件回传 logPath 供壳诊断。
package engine

import (
	"bufio"
	"encoding/json"
	"io"
	stdsync "sync"
	"time"

	"autosync/internal/config"
	"autosync/internal/configstore"
	"autosync/internal/log"
	"autosync/internal/notify"
	"autosync/internal/state"
	"autosync/internal/sync"
	"autosync/internal/tasksched"
)

// Engine 持有调度器与 IPC 读写器，实现引擎子进程主循环。
type Engine struct {
	store *configstore.Store
	sched *tasksched.TaskScheduler
	r     io.Reader
	enc   *json.Encoder
	mu    stdsync.Mutex // 保护 stdout 写入（事件 + ipcNotifier 并发）
}

// New 创建引擎。内部构造 ipcNotifier（notify 事件经 writeEvent）与 onResult（ticker 结果上报）。
// logger 透传给调度器，引擎自身不持有。
func New(store *configstore.Store, logger *log.Logger, r io.Reader, w io.Writer) *Engine {
	e := &Engine{store: store, r: r, enc: json.NewEncoder(w)}
	ipcN := &ipcNotifier{e: e}
	onResult := func(task string, res sync.SyncResult) {
		e.writeSyncResult(0, task, res)
	}
	e.sched = tasksched.NewTaskScheduler(store.List(), logger, ipcN, onResult)
	return e
}

// Run 启动调度器并进入 stdin 命令循环，阻塞至 quit 或 stdin 关闭。返回退出码 0。
func (e *Engine) Run() int {
	e.writeEvent(Event{
		Event:   "ready",
		Version: "1.2.0",
		LogPath: config.LogFilePath(),
		DataDir: config.UserDataDir(),
		Tasks:   e.taskStatuses(),
	})
	e.sched.Start()
	defer e.sched.Stop()

	sc := bufio.NewScanner(e.r)
	for sc.Scan() {
		var cmd Command
		if err := json.Unmarshal(sc.Bytes(), &cmd); err != nil {
			e.writeEvent(Event{Event: "error", Message: "解析命令失败: " + err.Error()})
			continue
		}
		if e.handle(cmd) {
			break // quit
		}
	}
	return 0
}

// handle 分发单条命令，返回 true 表示 quit（应退出主循环）。
func (e *Engine) handle(cmd Command) bool {
	switch cmd.Cmd {
	case "status":
		e.writeEvent(Event{ID: cmd.ID, Event: "status", Tasks: e.taskStatuses()})
	case "sync-now":
		if res, err := e.sched.RunNow(cmd.Task); err != nil {
			e.cmdError(cmd.ID, err)
		} else {
			e.writeSyncResult(cmd.ID, cmd.Task, res)
		}
	case "pause":
		if err := e.sched.SetPaused(cmd.Task, true); err != nil {
			e.cmdError(cmd.ID, err)
		} else {
			e.writeEvent(Event{ID: cmd.ID, Event: "paused", Task: cmd.Task})
		}
	case "resume":
		if err := e.sched.SetPaused(cmd.Task, false); err != nil {
			e.cmdError(cmd.ID, err)
		} else {
			e.writeEvent(Event{ID: cmd.ID, Event: "resumed", Task: cmd.Task})
		}
	case "config-list":
		ts, dtos := e.taskSnapshot()
		e.writeEvent(Event{ID: cmd.ID, Event: "config-list", Tasks: ts, ConfigTasks: dtos})
	case "config-save":
		if err := e.saveConfig(cmd.Tasks); err != nil {
			e.cmdError(cmd.ID, err)
		} else {
			ts, dtos := e.taskSnapshot()
			e.writeEvent(Event{ID: cmd.ID, Event: "config-saved", Tasks: ts, ConfigTasks: dtos})
		}
	case "quit":
		e.writeEvent(Event{Event: "bye", Reason: "quit"})
		return true
	default:
		e.writeEvent(Event{ID: cmd.ID, Event: "error", Message: "未知命令: " + cmd.Cmd})
	}
	return false
}

// cmdError 写一条带 id 的 error 事件。
func (e *Engine) cmdError(id int, err error) {
	e.writeEvent(Event{ID: id, Event: "error", Message: err.Error()})
}

// writeSyncResult 写一条 sync-result 事件（手动同步带 id，ticker 触发传 0 省略）。
func (e *Engine) writeSyncResult(id int, task string, res sync.SyncResult) {
	e.writeEvent(Event{
		ID:           id,
		Event:        "sync-result",
		Task:         task,
		Outcome:      res.Outcome.String(),
		Message:      res.Message,
		BackupBranch: res.BackupBranch,
		At:           time.Now().Format(time.RFC3339),
	})
}

// saveConfig 把 IPC 任务 DTO 转为 configstore.Task，ReplaceAll + Save + 热重载调度器。
func (e *Engine) saveConfig(dtos []*TaskDTO) error {
	tasks := make([]*configstore.Task, 0, len(dtos))
	for _, d := range dtos {
		tasks = append(tasks, dtoToTask(d))
	}
	if err := e.store.ReplaceAll(tasks); err != nil {
		return err
	}
	if err := e.store.Save(); err != nil {
		return err
	}
	e.sched.Reload(tasks)
	return nil
}

// buildStatus 从单个执行器构造状态投影（含上次同步结果，读 state 文件）。
func buildStatus(r *tasksched.TaskRunner) TaskStatus {
	t := r.Task()
	ts := TaskStatus{Name: t.Name, RepoDir: t.RepoDir, Interval: t.Interval, Paused: r.Paused()}
	if st, err := state.New(t.ResolveStateFile()).Load(); err == nil && !st.LastSyncAt.IsZero() {
		ts.LastSyncAt = st.LastSyncAt.Format(time.RFC3339)
		ts.LastOutcome = st.LastOutcome
		ts.LastMessage = st.LastMessage
	}
	return ts
}

// taskStatuses 构造所有任务的状态投影（ready/status 事件）。
func (e *Engine) taskStatuses() []TaskStatus {
	runners := e.sched.Runners()
	out := make([]TaskStatus, 0, len(runners))
	for _, r := range runners {
		out = append(out, buildStatus(r))
	}
	return out
}

// taskSnapshot 单遍历构造状态投影与完整配置投影（config-list/config-saved 事件）。
func (e *Engine) taskSnapshot() ([]TaskStatus, []*TaskDTO) {
	runners := e.sched.Runners()
	statuses := make([]TaskStatus, 0, len(runners))
	dtos := make([]*TaskDTO, 0, len(runners))
	for _, r := range runners {
		statuses = append(statuses, buildStatus(r))
		dtos = append(dtos, taskToDTO(r.Task()))
	}
	return statuses, dtos
}

// writeEvent 加锁写一行 JSON 事件到 stdout（事件与 ipcNotifier 共享此锁，避免交错）。
func (e *Engine) writeEvent(ev Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	_ = e.enc.Encode(ev)
}

// ipcNotifier 实现 notify.Notifier，经 Engine.writeEvent 写 notify 事件，委托壳投递系统通知。
type ipcNotifier struct{ e *Engine }

// Notify 写 notify 事件 JSON，severity 经 Severity.String() 映射为 info/warn/error。
func (n *ipcNotifier) Notify(title, body string, severity notify.Severity) error {
	n.e.writeEvent(Event{
		Event:    "notify",
		Severity: severity.String(),
		Title:    title,
		Body:     body,
	})
	return nil
}

// dtoToTask 把 IPC 任务 DTO 转为 configstore.Task（内嵌 Config 直接拷贝，无字段镜像）。
func dtoToTask(d *TaskDTO) *configstore.Task {
	return &configstore.Task{Name: d.Name, Config: d.Config}
}

// taskToDTO 把 configstore.Task 转为 IPC 任务 DTO（内嵌 Config 直接拷贝）。
func taskToDTO(t *configstore.Task) *TaskDTO {
	return &TaskDTO{Name: t.Name, Config: t.Config}
}
