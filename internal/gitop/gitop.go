// gitop.go 定义 Git 操作的抽象接口与基于系统 git 的实现。
// 同步引擎依赖 GitOperator 接口而非具体实现（依赖倒置），便于 P4 的 dry-run 装饰器扩展。
// 所有 git 命令统一设置 GIT_TERMINAL_PROMPT=0 与 GIT_MERGE_AUTOEDIT=no，避免交互阻塞。
package gitop

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
// P2 仅含主路径方法；冲突处理（force push / 备份分支 / reset）方法在 P3 扩展。
type GitOperator interface {
	IsRepo() bool
	Init(remote, remoteURL, branch string) error
	StageAll() error
	HasChanges() (bool, error)
	Commit(msg string) error
	Fetch(remote string) error
	RemoteBranchExists(remote, branch string) (bool, error)
	RelationTo(remote, branch string) (Relation, error)
	PullRebase(remote, branch string) error
	RebaseAbort() error
	Push(remote, branch string) error
	// 冲突处理（P3）
	PushForce(remote, branch string) error                        // --force-with-lease
	CreateBackupBranch(remote, branch, backupName string) error   // 从 remote/<branch> 建备份分支
	PushBranch(remote, branchName string) error                   // 推送备份分支到远程
	DeleteRemoteBranch(remote, branchName string) error           // 删除远程分支
	DeleteLocalBranch(branchName string) error                    // 删除本地分支
	ListBackupBranches(remote string) ([]string, error)           // 列出 backup/remote-*（本地+远程去重）
	ResetHardToRemote(remote, branch string) error                // reset --hard + clean -fd
}

// execGit 通过 shell out 调用系统 git 实现 GitOperator。
type execGit struct {
	repoDir string
	logger  *log.Logger
}

// NewExecGit 创建基于系统 git 的操作器。
func NewExecGit(repoDir string, logger *log.Logger) GitOperator {
	return &execGit{repoDir: repoDir, logger: logger}
}

// run 执行 git 命令，返回去空白后的合并输出；失败时记日志并返回 error。
func (g *execGit) run(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = g.repoDir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_MERGE_AUTOEDIT=no")
	out, err := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		g.logger.Error(fmt.Sprintf("git %s 失败: %s | %s", strings.Join(args, " "), err, truncate(s, 200)))
		return s, fmt.Errorf("git %s: %s", strings.Join(args, " "), truncate(s, 200))
	}
	g.logger.Info(fmt.Sprintf("git %s → %s", strings.Join(args, " "), truncate(s, 100)))
	return s, nil
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
func (g *execGit) Fetch(remote string) error {
	_, err := g.run("fetch", remote)
	return err
}

// RemoteBranchExists 判断远程跟踪分支是否存在；不存在返回 false 而非 error。
func (g *execGit) RemoteBranchExists(remote, branch string) (bool, error) {
	cmd := exec.Command("git", "rev-parse", "--verify", remote+"/"+branch)
	cmd.Dir = g.repoDir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if err := cmd.Run(); err != nil {
		return false, nil // 引用不存在，非错误
	}
	return true, nil
}

// RelationTo 用 rev-parse 与 merge-base 判定本地与远程的关系。
// 这是 TECH.md 中 IsDiverged 的修正版：原布尔判定无法区分"远程领先"，
// 会导致远程领先时误走 push（非快进失败）。四态关系可正确路由。
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
		return RelDiverged, nil // 无共同祖先视为分叉
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

// RebaseAbort 中止进行中的 rebase，恢复到 rebase 前状态。
// 可能没有进行中的 rebase（如 pull 因非冲突原因失败），此时报错可忽略。
func (g *execGit) RebaseAbort() error {
	cmd := exec.Command("git", "rebase", "--abort")
	cmd.Dir = g.repoDir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_MERGE_AUTOEDIT=no")
	cmd.CombinedOutput() // 忽略输出与错误
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

// truncate 截断字符串到最大长度，便于日志输出。
func truncate(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}
