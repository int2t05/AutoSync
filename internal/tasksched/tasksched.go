// tasksched.go 按任务间隔定时调度同步：每任务独立 ticker，复用 V1.0 Syncer 引擎。
// TaskRunner 加锁 → 维护 .gitignore → Syncer → 写状态 → 通知；TaskScheduler 管理多任务 ticker。
package tasksched

import (
	"fmt"
	"path/filepath"
	stdsync "sync"
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
	task    *configstore.Task
	logger  *log.Logger
	mu      stdsync.Mutex // 串行化同一任务的并发触发（tick + 手动）
	pauseMu stdsync.Mutex
	paused  bool // 暂停标志：暂停时 ticker 跳过触发
}

// NewTaskRunner 创建任务执行器。
func NewTaskRunner(task *configstore.Task, logger *log.Logger) *TaskRunner {
	return &TaskRunner{task: task, logger: logger}
}

// Task 返回执行器关联的任务（供托盘查询状态）。
func (r *TaskRunner) Task() *configstore.Task { return r.task }

// SetPaused 设置任务暂停/恢复。暂停时定时 ticker 跳过触发（手动 RunNow 仍可强制执行）。
func (r *TaskRunner) SetPaused(p bool) {
	r.pauseMu.Lock()
	r.paused = p
	r.pauseMu.Unlock()
}

// Paused 返回任务是否暂停。
func (r *TaskRunner) Paused() bool {
	r.pauseMu.Lock()
	defer r.pauseMu.Unlock()
	return r.paused
}

// Run 执行一次同步：任务级锁 → .gitignore 维护 → Syncer → 写状态 → 通知。
// 同任务已有实例持锁时跳过。返回同步结果。
func (r *TaskRunner) Run() sync.SyncResult {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 任务级锁：跨进程防止同任务并发（如手动 CLI 与守护进程同时触发）
	acquired, release := lock.New(r.task.ResolveLockFile()).Acquire()
	if !acquired {
		r.logger.Info(fmt.Sprintf("任务 %s 已有实例在运行，跳过", r.task.Name))
		return sync.SyncResult{Outcome: sync.OutcomeNoChanges, Message: "已有实例在运行，跳过"}
	}
	defer release()

	// .gitignore 维护：仅追加缺失条目
	if _, err := gitignore.Ensure(filepath.Join(r.task.RepoDir, ".gitignore"), r.task.Ignore); err != nil {
		r.logger.Warn(fmt.Sprintf("任务 %s 维护 .gitignore 失败: %v", r.task.Name, err))
	}

	// 构造 git 操作器（execGit + 重试装饰器）并执行同步状态机
	gitOp := gitop.NewRetryGit(
		gitop.NewExecGit(r.task.RepoDir, r.logger),
		r.task.RetryCount, r.task.RetryBaseDelayDur, r.logger,
	)
	result := sync.NewSyncer(&r.task.Config, gitOp, r.logger).Run()

	// 持久化状态（供 status / 托盘读取）
	if err := state.New(r.task.ResolveStateFile()).Save(state.State{
		LastSyncAt:   time.Now(),
		LastOutcome:  result.Outcome.String(),
		LastMessage:  result.Message,
		BackupBranch: result.BackupBranch,
	}); err != nil {
		r.logger.Warn(fmt.Sprintf("任务 %s 写状态文件失败: %v", r.task.Name, err))
	}

	// 通知策略：成功静默，异常才通知
	decision := notify.PolicyFor(result)
	if decision.Notify {
		if err := notify.NewBeeepNotifier().Notify(decision.Title, decision.Body, decision.Severity); err != nil {
			r.logger.Warn(fmt.Sprintf("任务 %s 发送通知失败: %v", r.task.Name, err))
		}
	}
	return result
}

// TaskScheduler 按各任务 interval 启动独立 ticker 定时触发 TaskRunner。
type TaskScheduler struct {
	runners []*TaskRunner
	logger  *log.Logger
	tickers []*time.Ticker
	stop    chan struct{}
	wg      stdsync.WaitGroup
	mu      stdsync.Mutex
	running bool
}

// NewTaskScheduler 为每个任务构造一个 TaskRunner。
func NewTaskScheduler(tasks []*configstore.Task, logger *log.Logger) *TaskScheduler {
	runners := make([]*TaskRunner, 0, len(tasks))
	for _, t := range tasks {
		runners = append(runners, NewTaskRunner(t, logger))
	}
	return &TaskScheduler{runners: runners, logger: logger}
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
				r.Run() // 启动时立即执行一次
			}
			for {
				select {
				case <-t.C:
					if !r.Paused() {
						r.Run()
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
func (s *TaskScheduler) runnerByName(name string) *TaskRunner {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.runners {
		if r.task.Name == name {
			return r
		}
	}
	return nil
}

// Reload 用新任务列表重建执行器并重启 ticker（配置变更后热重载）。
func (s *TaskScheduler) Reload(tasks []*configstore.Task) {
	s.Stop()
	runners := make([]*TaskRunner, 0, len(tasks))
	for _, t := range tasks {
		runners = append(runners, NewTaskRunner(t, s.logger))
	}
	s.mu.Lock()
	s.runners = runners
	s.mu.Unlock()
	s.Start()
}
