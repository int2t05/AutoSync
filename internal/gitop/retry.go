// retry.go 对网络类 git 操作做指数退避重试，避免瞬时网络抖动导致同步失败。
// Retry 是纯控制流函数（可单测，测试用 baseDelay=0 跳过实际等待）；
// retryGit 装饰器嵌入 GitOperator，仅覆盖网络方法，其余直接委托。
package gitop

import (
	"fmt"
	"time"

	"autosync/internal/log"
)

// Retry 调用 fn 至多 count 次，指数退避（baseDelay, 2*baseDelay, ...）。
// 首次成功立即返回；全部失败返回最后一次错误。非网络失败由调用方决定是否重试。
func Retry(fn func() error, count int, baseDelay time.Duration, logger *log.Logger) error {
	if count < 1 {
		count = 1
	}
	var err error
	delay := baseDelay
	for i := 0; i < count; i++ {
		if err = fn(); err == nil {
			return nil
		}
		if i < count-1 {
			logger.Warn(fmt.Sprintf("第 %d 次失败，%v 后重试: %v", i+1, delay, err))
			time.Sleep(delay)
			delay *= 2
		}
	}
	return err
}

// retryGit 包装 GitOperator，对网络类操作重试，其余方法通过嵌入直接委托。
type retryGit struct {
	GitOperator
	count     int
	baseDelay time.Duration
	logger    *log.Logger
}

// NewRetryGit 创建重试装饰器，包裹 inner。
func NewRetryGit(inner GitOperator, count int, baseDelay time.Duration, logger *log.Logger) GitOperator {
	return &retryGit{GitOperator: inner, count: count, baseDelay: baseDelay, logger: logger}
}

// retry 对 fn 做 count 次重试。
func (r *retryGit) retry(fn func() error) error {
	return Retry(fn, r.count, r.baseDelay, r.logger)
}

// Fetch 拉取远程引用（网络）——重试。
func (r *retryGit) Fetch(remote string) error {
	return r.retry(func() error { return r.GitOperator.Fetch(remote) })
}

// Push 推送（网络）——重试。
func (r *retryGit) Push(remote, branch string) error {
	return r.retry(func() error { return r.GitOperator.Push(remote, branch) })
}

// PushForce 强制推送（网络）——重试。
func (r *retryGit) PushForce(remote, branch string) error {
	return r.retry(func() error { return r.GitOperator.PushForce(remote, branch) })
}

// PushBranch 推送备份分支（网络）——重试。
func (r *retryGit) PushBranch(remote, branchName string) error {
	return r.retry(func() error { return r.GitOperator.PushBranch(remote, branchName) })
}

// DeleteRemoteBranch 删除远程分支（网络）——重试。
func (r *retryGit) DeleteRemoteBranch(remote, branchName string) error {
	return r.retry(func() error { return r.GitOperator.DeleteRemoteBranch(remote, branchName) })
}
