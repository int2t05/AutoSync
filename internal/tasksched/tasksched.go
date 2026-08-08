// tasksched.go 按任务间隔定时调度同步：每任务独立 ticker，复用 V1.0 Syncer 引擎。
// TaskRunner 加锁 → 维护 .gitignore → Syncer → 写状态 → 通知；TaskScheduler 管理多任务 ticker。
package tasksched

import (
	"fmt"
	"path/filepath"
	stdsync "sync"
	"sync/atomic"
	"time"

	"autosync/internal/configstore"
	"autosync/internal/gitignore"
	"autosync/internal/gitop"
	"autosync/internal/lock"
	"autosync/internal/log"
	"autosync/internal/notify"
	"autosync/internal/state"
	"autosync/internal/sync"
)

// TaskRunner 执行单个任务的同步全流程。
type TaskRunner struct {
	task     *configstore.Task
	logger   *log.Logger
	notifier notify.Notifier // 通知投递（beeep 或 IPC 委托），依赖倒置
	busy     atomic.Bool     // 同进程内是否正在同步：并发触发时跳过而非阻塞（防冗余再跑）
	pauseMu  stdsync.RWMutex
	paused   bool          // 暂停标志：暂停时 ticker 跳过触发
	gen      *atomic.Uint64 // 所属调度器代次计数器（Reload 后旧 runner 的 Run 跳过）
	born     uint64         // 本 runner 创建时代次
}

// NewTaskRunner 创建一次性任务执行器（CLI sync 用，无代次管理）。
// notifier 注入通知投递；从 state 文件恢复暂停标志（热重载/重启后保持）。
func NewTaskRunner(task *configstore.Task, logger *log.Logger, notifier notify.Notifier) *TaskRunner {
	return newTaskRunner(task, logger, notifier, nil, 0)
}

// newTaskRunner 创建任务执行器并绑定调度器代次（gen/born 由 TaskScheduler 注入）。
func newTaskRunner(task *configstore.Task, logger *log.Logger, notifier notify.Notifier, gen *atomic.Uint64, born uint64) *TaskRunner {
	r := &TaskRunner{task: task, logger: logger, notifier: notifier, gen: gen, born: born}
	if st, err := state.New(task.ResolveStateFile()).Load(); err == nil {
		r.paused = st.Paused
	}
	return r
}

// Task 返回执行器关联的任务（供托盘查询状态）。
func (r *TaskRunner) Task() *configstore.Task { return r.task }

// SetPaused 设置任务暂停/恢复并持久化到 state（热重载/重启后保持）。
// 暂停时定时 ticker 跳过触发（手动 RunNow 仍可强制执行）。
func (r *TaskRunner) SetPaused(p bool) {
	r.pauseMu.Lock()
	r.paused = p
	r.pauseMu.Unlock()
	if err := state.New(r.task.ResolveStateFile()).Update(func(st *state.State) { st.Paused = p }); err != nil {
		r.logger.Warnf("任务 %s 持久化暂停状态失败: %v", r.task.Name, err)
	}
}

// Paused 返回任务是否暂停。
func (r *TaskRunner) Paused() bool {
	r.pauseMu.RLock()
	defer r.pauseMu.RUnlock()
	return r.paused
}

// Run 执行一次同步：任务级锁 → .gitignore 维护 → Syncer → 写状态 → 通知。
// 同任务已有实例（本进程 busy / 跨进程锁）时跳过。返回同步结果。
func (r *TaskRunner) Run() sync.SyncResult {
	// 代次校验：Reload 后旧 runner 的 Run 直接跳过，防手动同步跑到旧配置
	if r.gen != nil && r.gen.Load() != r.born {
		r.logger.Infof("任务 %s 配置已变更，跳过本次", r.task.Name)
		return sync.SyncResult{Outcome: sync.OutcomeSkipped, Message: "配置已变更，跳过"}
	}
	// 同进程串行：并发触发（tick + 手动）时跳过本次，不阻塞等待后冗余再跑
	if !r.busy.CompareAndSwap(false, true) {
		r.logger.Infof("任务 %s 正在运行，跳过本次", r.task.Name)
		return sync.SyncResult{Outcome: sync.OutcomeSkipped, Message: "正在运行，跳过"}
	}
	defer r.busy.Store(false)

	// 任务级锁：跨进程防止同任务并发（如手动 CLI 与守护进程同时触发）
	acquired, release := lock.New(r.task.ResolveLockFile()).Acquire()
	if !acquired {
		// 已有实例持锁：跳过而非"无变更"——不改写上次同步状态、不通知
		r.logger.Infof("任务 %s 已有实例在运行，跳过", r.task.Name)
		return sync.SyncResult{Outcome: sync.OutcomeSkipped, Message: "已有实例在运行，跳过"}
	}
	defer release()

	// .gitignore 维护：仅追加缺失条目
	if _, err := gitignore.Ensure(filepath.Join(r.task.RepoDir, ".gitignore"), r.task.Ignore); err != nil {
		r.logger.Warnf("任务 %s 维护 .gitignore 失败: %v", r.task.Name, err)
	}

	// 构造 git 操作器（execGit + 重试装饰器）并执行同步状态机
	gitOp := gitop.NewRetryGit(
		gitop.NewExecGit(r.task.RepoDir, r.logger, r.task.GitTimeoutDur),
		*r.task.RetryCount, r.task.RetryBaseDelayDur, r.logger,
	)
	result := sync.NewSyncer(&r.task.Config, gitOp, r.logger).Run()

	// 持久化状态（供 status / 托盘读取）；Update 保留暂停标志，不覆盖
	if err := state.New(r.task.ResolveStateFile()).Update(func(st *state.State) {
		st.LastSyncAt = time.Now()
		st.LastOutcome = result.Outcome.String()
		st.LastMessage = result.Message
		st.BackupBranch = result.BackupBranch
	}); err != nil {
		r.logger.Warnf("任务 %s 写状态文件失败: %v", r.task.Name, err)
	}

	// 通知策略：成功静默，异常才通知（notifier 由调用方注入：beeep 或 IPC 委托壳）
	decision := notify.PolicyFor(result)
	if decision.Notify {
		if err := r.notifier.Notify(decision.Title, decision.Body, decision.Severity); err != nil {
			r.logger.Warnf("任务 %s 发送通知失败: %v", r.task.Name, err)
		}
	}
	return result
}

// TaskScheduler 按各任务 interval 启动独立 ticker 定时触发 TaskRunner。
type TaskScheduler struct {
	runners  []*TaskRunner
	logger   *log.Logger
	notifier notify.Notifier
	onResult func(string, sync.SyncResult)
	tickers  []*time.Ticker
	stop     chan struct{}
	wg       stdsync.WaitGroup
	mu       stdsync.Mutex
	running  bool
	reloadMu stdsync.Mutex // 串行化并发 Reload（后台重建互不交错）
	gen      atomic.Uint64 // Reload 递增，令旧 runner 的 Run 跳过
}

// NewTaskScheduler 为每个任务构造一个 TaskRunner，透传 notifier 与 onResult 给每个执行器。
func NewTaskScheduler(tasks []*configstore.Task, logger *log.Logger, notifier notify.Notifier, onResult func(string, sync.SyncResult)) *TaskScheduler {
	s := &TaskScheduler{logger: logger, notifier: notifier, onResult: onResult}
	s.gen.Store(1)
	for _, t := range tasks {
		s.runners = append(s.runners, newTaskRunner(t, logger, notifier, &s.gen, s.gen.Load()))
	}
	return s
}

// Runners 返回全部任务执行器的副本（供托盘菜单构造，避免与 Reload 并发读写）。
func (s *TaskScheduler) Runners() []*TaskRunner {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*TaskRunner, len(s.runners))
	copy(out, s.runners)
	return out
}

// Start 启动所有任务的 ticker，每个任务启动时立即执行一次。
func (s *TaskScheduler) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return
	}
	s.running = true
	s.stop = make(chan struct{})
	for _, r := range s.runners {
		r := r
		interval := r.task.IntervalDur
		if interval <= 0 {
			interval = time.Minute
		}
		t := time.NewTicker(interval)
		s.tickers = append(s.tickers, t)
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			if !r.Paused() {
				s.report(r) // 启动时立即执行一次
			}
			for {
				select {
				case <-t.C:
					if !r.Paused() {
						s.report(r)
					}
				case <-s.stop:
					return
				}
			}
		}()
	}
}

// Stop 停止所有 ticker 并等待 goroutine 退出。
func (s *TaskScheduler) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	close(s.stop)
	for _, t := range s.tickers {
		t.Stop()
	}
	s.tickers = nil
	s.mu.Unlock()
	s.wg.Wait()
}

// RunNow 立即执行指定任务（手动同步，忽略暂停），未找到返回错误。
// 锁内仅查找执行器，Run() 耗时较长须在锁外执行。
func (s *TaskScheduler) RunNow(name string) (sync.SyncResult, error) {
	r := s.runnerByName(name)
	if r == nil {
		return sync.SyncResult{}, fmt.Errorf("任务不存在: %q", name)
	}
	return r.Run(), nil
}

// SetPaused 设置指定任务暂停/恢复。
func (s *TaskScheduler) SetPaused(name string, paused bool) error {
	r := s.runnerByName(name)
	if r == nil {
		return fmt.Errorf("任务不存在: %q", name)
	}
	r.SetPaused(paused)
	return nil
}

// runnerByName 在锁内按名查找执行器，未找到返回 nil。
// 查找键与判重键统一（safeName 规范化）：任务名 "a b" 可经 "a b" 或 "a_b" 两种写法命中。
func (s *TaskScheduler) runnerByName(name string) *TaskRunner {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := configstore.NormalizeName(name)
	for _, r := range s.runners {
		if configstore.NormalizeName(r.task.Name) == key {
			return r
		}
	}
	return nil
}

// report 执行一次任务并经 onResult 回调上报结果（ticker 触发；手动 RunNow 不经此路径）。
func (s *TaskScheduler) report(r *TaskRunner) {
	res := r.Run()
	if s.onResult != nil {
		s.onResult(r.task.Name, res)
	}
}

// Reload 后台异步重建调度器：停止旧 ticker（Stop 有界，见 git 超时）→ 重建 runners → 重启 → onDone。
// 调用方（UI/IPC 线程）立即返回，不再冻结在 Stop 的等待上（此前 git 挂起时配置窗口冻结）。
// 并发 Reload 由 reloadMu 串行化；进程退出瞬间若 Reload 仍在进行，Start 复活 ticker 随进程消亡。
func (s *TaskScheduler) Reload(tasks []*configstore.Task, onDone func()) {
	go func() {
		s.reloadMu.Lock()
		defer s.reloadMu.Unlock()
		s.Stop()
		s.gen.Add(1) // 递增代次：进行中/排队的手动 Run 命中旧 runner 时跳过
		runners := make([]*TaskRunner, 0, len(tasks))
		for _, t := range tasks {
			runners = append(runners, newTaskRunner(t, s.logger, s.notifier, &s.gen, s.gen.Load()))
		}
		s.mu.Lock()
		s.runners = runners
		s.mu.Unlock()
		s.Start()
		if onDone != nil {
			onDone()
		}
	}()
}
