// engine.go 实现 engine 子命令的 IPC 主循环：Go 引擎子进程经 stdin/stdout JSON 与 Swift 壳通信。
// 壳发命令（status/sync-now/pause/resume/config-list/config-save/quit），引擎回事件。
// 启动即开始 TaskScheduler 调度；ticker 触发的结果经 onResult 回调上报 sync-result（无 id）；
// 通知经 ipcNotifier 委托壳（UNUserNotificationCenter）。日志写文件，ready 事件回传 logPath 供壳诊断。
package engine

import (
	"bufio"
	"encoding/json"
	"io"
	"sync/atomic"
	"time"

	"autosync/internal/config"
	"autosync/internal/configstore"
	"autosync/internal/log"
	"autosync/internal/notify"
	"autosync/internal/state"
	"autosync/internal/sync"
	"autosync/internal/tasksched"
)

// Version 是 AutoSync 当前版本号，供 ready 事件上报与 release 对齐。
const Version = "1.2.0"

// Engine 持有调度器与 IPC 读写器，实现引擎子进程主循环。
type Engine struct {
	store   *configstore.Store
	sched   *tasksched.TaskScheduler
	logger  *log.Logger
	r       io.Reader
	evCh    chan []byte         // 事件队列（有界）：壳不读 stdout 时丢弃而非阻塞引擎循环
	flushCh chan chan struct{}  // 冲刷请求：写环排空队列后关闭完成通道
	dropN   atomic.Int64        // 丢弃计数（节流日志）
}

// New 创建引擎。内部构造 ipcNotifier（notify 事件经 writeEvent）与 onResult（ticker 结果上报）。
// 启动单写者 writeLoop 写 stdout：事件顺序由单写者保证，且引擎循环不被壳不读的 stdout 阻塞。
func New(store *configstore.Store, logger *log.Logger, r io.Reader, w io.Writer) *Engine {
	e := &Engine{store: store, logger: logger, r: r, evCh: make(chan []byte, 8), flushCh: make(chan chan struct{})}
	ipcN := &ipcNotifier{e: e}
	onResult := func(task string, res sync.SyncResult) {
		e.writeSyncResult(0, task, res)
	}
	e.sched = tasksched.NewTaskScheduler(store.List(), logger, ipcN, onResult)
	go e.writeLoop(w)
	return e
}

// Run 启动调度器并进入 stdin 命令循环，阻塞至 quit 或 stdin 关闭。返回退出码 0。
func (e *Engine) Run() int {
	e.writeEvent(Event{
		Event:   "ready",
		Version: Version,
		LogPath: config.LogFilePath(),
		DataDir: config.UserDataDir(),
		Tasks:   e.taskStatuses(),
	})
	e.sched.Start()

	sc := bufio.NewScanner(e.r)
	// 放宽单行上限（默认 64KB）：config-save 可能携带大型任务配置 JSON，超限会静默退出且无 bye
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
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
	e.sched.Stop()           // 停止 ticker（有界），其后不再产生新的 onResult 事件
	e.flush(2 * time.Second) // 冲刷 bye（壳不读 stdout 时写环阻塞，超时放弃）
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
		ts, dtos, err := e.saveConfig(cmd.Tasks)
		if err != nil {
			e.cmdError(cmd.ID, err)
		} else {
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

// saveConfig 把 IPC 任务 DTO 转为 configstore.Task，ReplaceAll + Save + 后台热重载调度器。
// 落盘失败时回滚内存态（调度器未 Reload，仍跑旧任务），壳收到 error 后保持配置窗口原状。
// Reload 异步执行（Stop 有界），故响应快照取自新任务列表而非 runners（后者尚未重建）。
func (e *Engine) saveConfig(dtos []*TaskDTO) ([]TaskStatus, []*TaskDTO, error) {
	tasks := make([]*configstore.Task, 0, len(dtos))
	for _, d := range dtos {
		tasks = append(tasks, dtoToTask(d))
	}
	old := e.store.List()
	if err := e.store.ReplaceAll(tasks); err != nil {
		return nil, nil, err
	}
	if err := e.store.Save(); err != nil {
		_ = e.store.ReplaceAll(old) // 回滚内存态；old 曾通过校验，回滚必成功
		return nil, nil, err
	}
	e.sched.Reload(tasks, nil)
	ts, dtos := snapshotOf(tasks)
	return ts, dtos, nil
}

// snapshotOf 从任务列表构造状态与配置投影（供 config-saved 即时响应）。
func snapshotOf(tasks []*configstore.Task) ([]TaskStatus, []*TaskDTO) {
	statuses := make([]TaskStatus, 0, len(tasks))
	dtos := make([]*TaskDTO, 0, len(tasks))
	for _, t := range tasks {
		statuses = append(statuses, buildStatusFromTask(t))
		dtos = append(dtos, taskToDTO(t))
	}
	return statuses, dtos
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

// buildStatusFromTask 从任务构造状态投影（读 state 文件），供配置保存后的即时响应。
func buildStatusFromTask(t *configstore.Task) TaskStatus {
	ts := TaskStatus{Name: t.Name, RepoDir: t.RepoDir, Interval: t.Interval}
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

// writeEvent 把事件 marshal 后非阻塞入队；队列满（壳不读 stdout）时丢弃并节流记日志。
// 引擎命令循环与 ticker 的 onResult/notify 均不被 stdout 阻塞（防管道写满双向死锁）。
func (e *Engine) writeEvent(ev Event) {
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	data = append(data, '\n')
	select {
	case e.evCh <- data:
	default:
		if n := e.dropN.Add(1); n == 1 || n%1000 == 0 {
			e.logger.Warnf("IPC 事件队列满，丢弃事件（壳未读 stdout？累计 %d 条）", n)
		}
	}
}

// writeLoop 单写者消费事件队列写 stdout，保证事件顺序且不阻塞引擎循环。
// 壳不读 stdout（管道写满）时 Write 阻塞的仅此一个 goroutine，队列填满后事件被丢弃。
func (e *Engine) writeLoop(w io.Writer) {
	for {
		select {
		case data := <-e.evCh:
			writeData(w, data, e.logger)
		case done := <-e.flushCh:
			// 排空队列（含先入队的 bye）后确认冲刷完成
		drain:
			for {
				select {
				case data := <-e.evCh:
					writeData(w, data, e.logger)
				default:
					close(done)
					break drain
				}
			}
		}
	}
}

// writeData 写单条事件到 w；w 支持 deadline 时设 2s 写超时（Write 阻塞时快速恢复）。
func writeData(w io.Writer, data []byte, logger *log.Logger) error {
	if f, ok := w.(interface{ SetWriteDeadline(time.Time) error }); ok {
		_ = f.SetWriteDeadline(time.Now().Add(2 * time.Second))
	}
	_, err := w.Write(data)
	if err != nil {
		logger.Warn("写 IPC 事件失败: " + err.Error())
	}
	return err
}

// flush 请求写环排空事件队列（退出前冲刷 bye），超时即放弃——壳不读 stdout 时写环被阻塞。
func (e *Engine) flush(timeout time.Duration) {
	done := make(chan struct{})
	select {
	case e.flushCh <- done:
		select {
		case <-done:
		case <-time.After(timeout):
		}
	case <-time.After(timeout):
	}
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
