// gitop_timeout_test.go 验证 git 命令超时：挂起命令被强制终止（真实 git 仓库 + hook，无 mock）。
// 超时是卡死根因的兜底——fetch/push 挂起时 ticker goroutine、Stop、退出路径的等待必须仍有界。
package tests

import (
	"context"
	"errors"
	"testing"
	"time"

	"autosync/internal/gitop"
)

// TestGitOp_CommandTimeout 验证挂起的 git 命令在超时后被终止，返回 context.DeadlineExceeded。
// 用 pre-push hook（sleep 30）让真实 git push 挂起，不依赖任何 mock。
// 注：Git for Windows 无 pre-fetch hook（模板与机制均缺失），故用核心自带的 pre-push。
func TestGitOp_CommandTimeout(t *testing.T) {
	repo := makeWorkRepo(t)
	remote := makeBareRemote(t)
	addRemote(t, repo, "origin", remote)
	pushToRemote(t, repo, "origin", "main") // 初始推送先完成，此时 hook 未安装

	writeHook(t, repo, "pre-push", "#!/bin/sh\nsleep 30\n")

	git := gitop.NewExecGit(repo, schedLogger(t), 300*time.Millisecond)
	start := time.Now()
	err := git.Push("origin", "main")

	if err == nil {
		t.Fatal("挂起的 push 应超时报错")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("应返回 context.DeadlineExceeded, got: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("超时未及时生效，耗时 %v（期望 300ms 超时后迅速返回）", elapsed)
	}
}
