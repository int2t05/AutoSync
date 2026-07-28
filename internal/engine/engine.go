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
	store  *configstore.Store
	logger *log.Logger
	sched  *tasksched.TaskScheduler
	r      io.Reader
	enc    *json.Encoder
	mu     stdsync.Mutex // 保护 stdout 写入（事件 + ipcNotifier 并发）
}

// New 创建引擎。内部构造 ipcNotifier（notify 事件经 writeEvent）与 onResult（ticker 结果上报）。
func New(store *configstore.Store, logger *log.Logger, r io.Reader, w io.Writer) *Engine {
	e := &Engine{store: store, logger: logger, r: r, enc: json.NewEncoder(w)}
	ipcN := &ipcNotifier{e: e}
	onResult := func(task string, res sync.SyncResult) {
		e.writeEvent(Event{
			Event:        "sync-result",
			Task:         task,
			Outcome:      res.Outcome.String(),
			Message:      res.Message,
			BackupBranch: res.BackupBranch,
			At:           time.Now().Format(time.RFC3339),
		})
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
		res, err := e.sched.RunNow(cmd.Task)
		if err != nil {
			e.writeEvent(Event{ID: cmd.ID, Event: "error", Message: err.Error()})
			break
		}
		e.writeEvent(Event{
			ID: cmd.ID, Event: "sync-result", Task: cmd.Task,
			Outcome: res.Outcome.String(), Message: res.Message,
			BackupBranch: res.BackupBranch, At: time.Now().Format(time.RFC3339),
		})
	case "pause":
		if err := e.sched.SetPaused(cmd.Task, true); err != nil {
			e.writeEvent(Event{ID: cmd.ID, Event: "error", Message: err.Error()})
			break
		}
		e.writeEvent(Event{ID: cmd.ID, Event: "paused", Task: cmd.Task})
	case "resume":
		if err := e.sched.SetPaused(cmd.Task, false); err != nil {
			e.writeEvent(Event{ID: cmd.ID, Event: "error", Message: err.Error()})
			break
		}
		e.writeEvent(Event{ID: cmd.ID, Event: "resumed", Task: cmd.Task})
	case "config-list":
		e.writeEvent(Event{ID: cmd.ID, Event: "config-list", Tasks: e.taskStatuses(), ConfigTasks: e.taskDTOs()})
	case "config-save":
		if err := e.saveConfig(cmd.Tasks); err != nil {
			e.writeEvent(Event{ID: cmd.ID, Event: "error", Message: err.Error()})
			break
		}
		e.writeEvent(Event{ID: cmd.ID, Event: "config-saved", Tasks: e.taskStatuses(), ConfigTasks: e.taskDTOs()})
	case "quit":
		e.writeEvent(Event{Event: "bye", Reason: "quit"})
		return true
	default:
		e.writeEvent(Event{ID: cmd.ID, Event: "error", Message: "未知命令: " + cmd.Cmd})
	}
	return false
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
	e.sched.Reload(e.store.List())
	return nil
}

// taskStatuses 构造当前所有任务的状态投影（含运行态与上次同步结果）。
func (e *Engine) taskStatuses() []TaskStatus {
	runners := e.sched.Runners()
	out := make([]TaskStatus, 0, len(runners))
	for _, r := range runners {
		ts := TaskStatus{
			Name: r.Task().Name, RepoDir: r.Task().RepoDir,
			Interval: r.Task().Interval, Paused: r.Paused(),
		}
		if st, err := state.New(r.Task().ResolveStateFile()).Load(); err == nil && !st.LastSyncAt.IsZero() {
			ts.LastSyncAt = st.LastSyncAt.Format(time.RFC3339)
			ts.LastOutcome = st.LastOutcome
			ts.LastMessage = st.LastMessage
		}
		out = append(out, ts)
	}
	return out
}

// writeEvent 加锁写一行 JSON 事件到 stdout（事件与 ipcNotifier 共享此锁，避免交错）。
func (e *Engine) writeEvent(ev Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	_ = e.enc.Encode(ev)
}

// ipcNotifier 实现 notify.Notifier，经 Engine.writeEvent 写 notify 事件，委托壳投递系统通知。
type ipcNotifier struct{ e *Engine }

// Notify 写 notify 事件 JSON，severity 映射为 info/warn/error。
func (n *ipcNotifier) Notify(title, body string, severity notify.Severity) error {
	n.e.writeEvent(Event{
		Event: "notify", Severity: severityString(int(severity)),
		Title: title, Body: body,
	})
	return nil
}

// dtoToTask 把 IPC 任务 DTO 转为 configstore.Task（字段一一对应）。
func dtoToTask(d *TaskDTO) *configstore.Task {
	return &configstore.Task{
		Name: d.Name,
		Config: config.Config{
			RepoDir:          d.RepoDir,
			RemoteURL:        d.RemoteURL,
			Remote:           d.Remote,
			Branch:           d.Branch,
			Interval:         d.Interval,
			ConflictStrategy: d.ConflictStrategy,
			BackupKeep:       d.BackupKeep,
			RetryCount:       d.RetryCount,
			RetryBaseDelay:   d.RetryBaseDelay,
			CommitMsgFormat:  d.CommitMsgFormat,
			ShowConsole:      d.ShowConsole,
			Ignore:           d.Ignore,
		},
	}
}

// taskDTOs 构造当前所有任务的完整配置投影（config-list/config-saved 事件）。
func (e *Engine) taskDTOs() []*TaskDTO {
	tasks := e.store.List()
	out := make([]*TaskDTO, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, taskToDTO(t))
	}
	return out
}

// taskToTask 把 configstore.Task 转为 IPC 任务 DTO（字段一一对应）。
func taskToDTO(t *configstore.Task) *TaskDTO {
	return &TaskDTO{
		Name:             t.Name,
		RepoDir:          t.RepoDir,
		RemoteURL:        t.RemoteURL,
		Remote:           t.Remote,
		Branch:           t.Branch,
		Interval:         t.Interval,
		ConflictStrategy: t.ConflictStrategy,
		BackupKeep:       t.BackupKeep,
		RetryCount:       t.RetryCount,
		RetryBaseDelay:   t.RetryBaseDelay,
		CommitMsgFormat:  t.CommitMsgFormat,
		ShowConsole:      t.ShowConsole,
		Ignore:           t.Ignore,
	}
}
