// gitop.go 定义 Git 操作的抽象接口与基于系统 git 的实现。
// 同步引擎依赖 GitOperator 接口而非具体实现（依赖倒置），便于装饰器扩展（重试、dry-run）。
// 所有 git 命令统一设置 GIT_TERMINAL_PROMPT=0 与 GIT_MERGE_AUTOEDIT=no，避免交互阻塞，
// 并统一经 exec() 执行：带超时终止挂起命令，杜绝裸 exec.Command 路径。
package gitop

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"autosync/internal/log"
)

// Relation 描述本地分支与远程分支的关系，用于决定推送还是合并。
type Relation int

const (
	RelUpToDate   Relation = iota // 本地与远程一致
	RelLocalAhead                 // 本地领先（远程是本地祖先），可直接推送
	RelRemoteAhead                // 远程领先（本地是远程祖先），需拉取
	RelDiverged                   // 真正分叉（双方各有独立提交），需 rebase
)

// String 返回 Relation 的标签，用于 dry-run 计划与日志。
func (r Relation) String() string {
	switch r {
	case RelUpToDate:
		return "UpToDate"
	case RelLocalAhead:
		return "LocalAhead"
	case RelRemoteAhead:
		return "RemoteAhead"
	case RelDiverged:
		return "Diverged"
	}
	return "Unknown"
}

// GitOperator 是同步引擎依赖的 git 操作抽象。
// 含主路径方法与冲突处理方法（force push / 备份分支 / reset）。
type GitOperator interface {
	IsRepo() bool
	Init(remote, remoteURL, branch string) error
	HasHead() (bool, error)
	CheckoutRemote(remote, branch string) error
	PushFirst(remote, branch string) error
	StageAll() error
	HasChanges() (bool, error)
	Commit(msg string) error
	Fetch(remote string) error
	RemoteBranchExists(remote, branch string) (bool, error)
	RelationTo(remote, branch string) (Relation, error)
	PullRebase(remote, branch string) error
	RebaseInProgress() (bool, error)
	RebaseAbort() error
	Push(remote, branch string) error
	// 冲突处理
	PushForce(remote, branch string) error                        // --force-with-lease
	CreateBackupBranch(remote, branch, backupName string) error   // 从 remote/<branch> 建备份分支
	PushBranch(remote, branchName string) error                   // 推送备份分支到远程
	DeleteRemoteBranch(remote, branchName string) error           // 删除远程分支
	DeleteLocalBranch(branchName string) error                    // 删除本地分支
	ListBackupBranches(remote string) ([]string, error)           // 列出 backup/remote-*（本地+远程去重）
	ResetHardToRemote(remote, branch string) error                // reset --hard + clean -fd
	DiffNameOnly(remote, branch string) ([]string, error)         // 本地 HEAD 与 remote/branch 的差异文件（Modified + Deleted）
}

// execGit 通过 shell out 调用系统 git 实现 GitOperator。
type execGit struct {
	repoDir string
	logger  *log.Logger
	timeout time.Duration // 单条 git 命令的完成上限：挂起（网络黑洞/SSH 认证卡住）时强制终止
}

// NewExecGit 创建基于系统 git 的操作器。
// timeout 为每条 git 命令的超时；本地命令毫秒级完成，网络命令挂起时由它兜底，
// 保证调用方（ticker goroutine / Stop / 退出路径）的等待有界。
func NewExecGit(repoDir string, logger *log.Logger, timeout time.Duration) GitOperator {
	return &execGit{repoDir: repoDir, logger: logger, timeout: timeout}
}

// run 执行 git 命令，返回去空白后的合并输出；失败时记日志并返回 error。
func (g *execGit) run(args ...string) (string, error) {
	out, err := g.exec(args...)
	s := strings.TrimSpace(out)
	if err != nil {
		g.logger.Error(fmt.Sprintf("git %s 失败: %v | %s", strings.Join(args, " "), err, truncate(s, 200)))
		return s, err
	}
	g.logger.Info(fmt.Sprintf("git %s → %s", strings.Join(args, " "), truncate(s, 100)))
	return s, nil
}

// exec 执行 git 命令并返回合并输出（stdout+stderr），带超时与隐藏窗口，不记日志。
// 全部 git 命令的唯一执行入口。超时双保险：
//   - CommandContext 杀掉直接子进程；
//   - 输出管道读端设 deadline——孙子进程（hook/ssh 等继承写端）持管道时，
//     仅杀直接进程仍会让读端无限等 EOF，deadline 保证返回。
// 超时时返回 context.DeadlineExceeded（可 errors.Is 判定）。
func (g *execGit) exec(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.repoDir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_MERGE_AUTOEDIT=no")
	applyHideWindow(cmd) // Windows 下隐藏 git 子进程窗口，避免同步时弹黑窗

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}

	// 并发读两路输出（顺序读会因另一路管道填满而死锁），分入独立 buffer 避免并发写。
	// 超时兜底：CommandContext 只杀直接子进程；hook/ssh 等孙子进程继承管道写端时，
	// 读端仍会等 EOF 无限阻塞（Windows 管道不支持 SetReadDeadline），故定时强制关闭读端。
	var stdoutBuf, stderrBuf bytes.Buffer
	done := make(chan struct{}, 2)
	drain := func(r io.ReadCloser, buf *bytes.Buffer) {
		defer r.Close()
		defer func() { done <- struct{}{} }()
		_, _ = io.Copy(buf, r)
	}
	go drain(stdout, &stdoutBuf)
	go drain(stderr, &stderrBuf)
	timeout := time.AfterFunc(g.timeout, func() {
		stdout.Close()
		stderr.Close()
	})
	defer timeout.Stop()
	<-done
	<-done

	err = cmd.Wait()
	out := stdoutBuf.String() + stderrBuf.String()
	if ctx.Err() != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), ctx.Err())
	}
	if err != nil {
		return out, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// IsRepo 通过 .git 存在判断是否已是 git 仓库。
func (g *execGit) IsRepo() bool {
	_, err := os.Stat(filepath.Join(g.repoDir, ".git"))
	return err == nil
}

// Init 初始化仓库：init + remote add + 首次提交 + push -u。
// 使用 --allow-empty 以支持空文件夹首次同步。
func (g *execGit) Init(remote, remoteURL, branch string) error {
	steps := [][]string{
		{"init", "-b", branch},
		{"remote", "add", remote, remoteURL},
		{"add", "-A"},
		{"commit", "--allow-empty", "-m", "init: first sync"},
		{"push", "-u", remote, branch},
	}
	for _, args := range steps {
		if _, err := g.run(args...); err != nil {
			return err
		}
	}
	return nil
}

// HasHead 判断仓库是否有 HEAD 提交；无 HEAD（手动 git init 的空仓库，unborn 分支）返回 false。
func (g *execGit) HasHead() (bool, error) {
	if _, err := g.exec("rev-parse", "--verify", "HEAD"); err != nil {
		return false, nil // unborn HEAD，非错误
	}
	return true, nil
}

// CheckoutRemote 基于远程分支创建本地分支并检出（unborn 仓库拉取远程历史落地）。
func (g *execGit) CheckoutRemote(remote, branch string) error {
	_, err := g.run("checkout", "-B", branch, remote+"/"+branch)
	return err
}

// PushFirst 在空仓库（无 HEAD）上完成首次提交并推送：commit --allow-empty + push -u。
func (g *execGit) PushFirst(remote, branch string) error {
	if _, err := g.run("commit", "--allow-empty", "-m", "init: first sync"); err != nil {
		return err
	}
	_, err := g.run("push", "-u", remote, branch)
	return err
}

// StageAll 暂存全部变更（git add -A）。
func (g *execGit) StageAll() error {
	_, err := g.run("add", "-A")
	return err
}

// HasChanges 通过 git status --porcelain 判断是否有未提交变更。
func (g *execGit) HasChanges() (bool, error) {
	out, err := g.run("status", "--porcelain")
	if err != nil {
		return false, err
	}
	return out != "", nil
}

// Commit 提交已暂存变更。
func (g *execGit) Commit(msg string) error {
	_, err := g.run("commit", "-m", msg)
	return err
}

// Fetch 拉取远程引用（不合并）。
// --prune 删除远端已不存在的跟踪分支，避免陈旧引用导致假 UpToDate / 假"远程分支存在"。
func (g *execGit) Fetch(remote string) error {
	_, err := g.run("fetch", "--prune", remote)
	return err
}

// RemoteBranchExists 判断远程跟踪分支是否存在；不存在返回 false 而非 error。
func (g *execGit) RemoteBranchExists(remote, branch string) (bool, error) {
	_, err := g.exec("rev-parse", "--verify", remote+"/"+branch)
	if err != nil {
		return false, nil // 引用不存在，非错误
	}
	return true, nil
}

// RelationTo 用 rev-parse 与 merge-base 判定本地与远程的关系。
// 用四态关系替代布尔"是否分叉"：布尔判定无法区分"远程领先"，
// 会导致远程领先时误走 push（非快进失败）。四态可正确路由。
func (g *execGit) RelationTo(remote, branch string) (Relation, error) {
	localHead, err := g.run("rev-parse", "HEAD")
	if err != nil {
		return 0, err
	}
	remoteHead, err := g.run("rev-parse", remote+"/"+branch)
	if err != nil {
		return 0, err
	}
	if localHead == remoteHead {
		return RelUpToDate, nil
	}
	mb, err := g.run("merge-base", localHead, remoteHead)
	if err != nil {
		// 无共同祖先（远程被 force-push 重写 / 换仓库）：
		// 绝不静默回退 Diverged——那会让 local_wins 借 rebase/force push 覆盖无关远程。
		return 0, fmt.Errorf("merge-base 失败（本地与远程无共同祖先，可能远程被重写或换仓库）: %w", err)
	}
	switch {
	case mb == localHead:
		return RelRemoteAhead, nil
	case mb == remoteHead:
		return RelLocalAhead, nil
	default:
		return RelDiverged, nil
	}
}

// PullRebase 以 rebase 方式拉取并合并远程变更。
// 适用于 RemoteAhead（快进）与 Diverged（重放本地提交）两种情况。
func (g *execGit) PullRebase(remote, branch string) error {
	_, err := g.run("pull", "--rebase", remote, branch)
	return err
}

// RebaseInProgress 判断仓库是否处于 rebase 进行中（存在 rebase-merge/rebase-apply 目录）。
// 供同步器区分 pull --rebase 失败是"真冲突"还是"网络/钩子等其他原因"：
// 只有真冲突才中止 rebase 并交给冲突策略，其余失败须显式报错而非 reset / force push。
func (g *execGit) RebaseInProgress() (bool, error) {
	for _, d := range []string{"rebase-merge", "rebase-apply"} {
		if _, err := os.Stat(filepath.Join(g.repoDir, ".git", d)); err == nil {
			return true, nil
		}
	}
	return false, nil
}

// RebaseAbort 中止进行中的 rebase，恢复到 rebase 前状态。
// 可能没有进行中的 rebase（如 pull 因非冲突原因失败），此时报错可忽略。
func (g *execGit) RebaseAbort() error {
	_, _ = g.exec("rebase", "--abort") // 忽略输出与错误
	return nil
}

// Push 推送本地分支到远程。
func (g *execGit) Push(remote, branch string) error {
	_, err := g.run("push", remote, branch)
	return err
}

// PushForce 以 --force-with-lease 强制推送本地分支。
// 比 --force 安全：若 fetch 后远程被他人改写，lease 检查失败会拒绝推送。
func (g *execGit) PushForce(remote, branch string) error {
	_, err := g.run("push", "--force-with-lease", remote, branch)
	return err
}

// CreateBackupBranch 从远程分支创建本地备份分支（指向远程当前提交），用于 local_wins 备份。
func (g *execGit) CreateBackupBranch(remote, branch, backupName string) error {
	_, err := g.run("branch", backupName, remote+"/"+branch)
	return err
}

// PushBranch 推送指定分支到远程（用于推送备份分支）。
func (g *execGit) PushBranch(remote, branchName string) error {
	_, err := g.run("push", remote, branchName)
	return err
}

// DeleteRemoteBranch 删除远程分支。
func (g *execGit) DeleteRemoteBranch(remote, branchName string) error {
	_, err := g.run("push", remote, "--delete", branchName)
	return err
}

// DeleteLocalBranch 删除本地分支（-D 强制，因备份分支通常未被合并）。
func (g *execGit) DeleteLocalBranch(branchName string) error {
	_, err := g.run("branch", "-D", branchName)
	return err
}

// ListBackupBranches 列出 backup/remote-* 备份分支（本地+远程去重，去掉远程前缀）。
func (g *execGit) ListBackupBranches(remote string) ([]string, error) {
	localOut, err := g.run("for-each-ref", "--format=%(refname:short)", "refs/heads/backup/remote-*")
	if err != nil {
		return nil, err
	}
	remoteOut, err := g.run("for-each-ref", "--format=%(refname:short)", "refs/remotes/"+remote+"/backup/remote-*")
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool)
	for _, line := range strings.Split(localOut, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			set[line] = true
		}
	}
	prefix := remote + "/"
	for _, line := range strings.Split(remoteOut, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			set[strings.TrimPrefix(line, prefix)] = true
		}
	}
	result := make([]string, 0, len(set))
	for b := range set {
		result = append(result, b)
	}
	return result, nil
}

// ResetHardToRemote 重置本地到远程版本并清理未跟踪文件（remote_wins 策略）。
func (g *execGit) ResetHardToRemote(remote, branch string) error {
	if _, err := g.run("reset", "--hard", remote+"/"+branch); err != nil {
		return err
	}
	_, err := g.run("clean", "-fd")
	return err
}

// DiffNameOnly 列出本地 HEAD 与 remote/branch 的差异文件（Added + Modified + Deleted）。
// 供 conflict_files 策略读取需保留为副本的本地文件：Added 是本地独有文件，
// 不含它将随 reset --hard + clean -fd 被静默删除。
func (g *execGit) DiffNameOnly(remote, branch string) ([]string, error) {
	out, err := g.run("diff", "--name-only", "--diff-filter=AMD", "HEAD", remote+"/"+branch)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// truncate 截断字符串到最大长度，便于日志输出。
func truncate(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}
