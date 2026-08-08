// gitop.go 定义 Git 操作的抽象接口与基于系统 git 的实现。
// 同步引擎依赖 GitOperator 接口而非具体实现（依赖倒置），便于装饰器扩展（重试、dry-run）。
// 所有 git 命令统一设置 GIT_TERMINAL_PROMPT=0 与 GIT_MERGE_AUTOEDIT=no，避免交互阻塞，
// 并统一经 exec() 执行：带超时终止挂起命令，杜绝裸 exec.Command 路径。
package gitop

import (
	"context"
	"fmt"
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
	// 配置一致性
	GetRemoteURL(remote string) (string, error)   // remote get-url：核对配置 remote_url 与仓库实际远程
	CurrentBranch() (string, error)               // branch --show-current：核对配置 branch 与本地当前分支
	// 冲突处理
	PushForce(remote, branch string) error                        // --force-with-lease
	CreateBackupBranch(remote, branch, backupName string) error   // 从 remote/<branch> 建备份分支
	PushBranch(remote, branchName string) error                   // 推送备份分支到远程
	DeleteRemoteBranch(remote, branchName string) error           // 删除远程分支
	DeleteLocalBranch(branchName string) error                    // 删除本地分支
	ListBackupBranches(remote string) ([]string, error)           // 列出 backup/remote-*（本地+远程去重）
	ResetHardToRemote(remote, branch string) error                // reset --hard + clean -fd
	DiffNameOnly(remote, branch string) ([]string, error)         // 本地 HEAD 与 remote/branch 中本地有内容的差异文件（Modified + Deleted，排除远程独有的 Added），供冲突副本保留
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
		g.logger.Errorf("git %s 失败: %v | %s", strings.Join(args, " "), err, truncate(s, 200))
		return s, err
	}
	g.logger.Infof("git %s → %s", strings.Join(args, " "), truncate(s, 100))
	return s, nil
}

// exec 执行 git 命令并返回合并输出（stdout+stderr），带超时与隐藏窗口，不记日志。
// 全部 git 命令的唯一执行入口。
// 输出重定向到临时文件而非管道：hook/ssh 等孙子进程继承管道写端时，仅杀直接进程
// 仍会让读端无限等 EOF（Windows 管道不支持 deadline，Close 解除阻塞也不可靠）；
// 文件则天然不阻塞 cmd.Wait。超时时 CommandContext 杀直接进程，Wait 有界返回。
// 返回 context.DeadlineExceeded（可 errors.Is 判定）。
func (g *execGit) exec(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.repoDir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_MERGE_AUTOEDIT=no")
	applyHideWindow(cmd) // Windows 下隐藏 git 子进程窗口，避免同步时弹黑窗

	f, err := os.CreateTemp("", "autosync-git-*.out")
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	outPath := f.Name()
	cmd.Stdout = f
	cmd.Stderr = f

	if err := cmd.Start(); err != nil {
		f.Close()
		removeOutputFile(outPath)
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	err = cmd.Wait() // 仅等进程句柄：超时由 CommandContext 终止
	f.Close()
	data, rerr := os.ReadFile(outPath)
	removeOutputFile(outPath)
	if ctx.Err() != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), ctx.Err())
	}
	if err != nil {
		return string(data), fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	if rerr != nil {
		// 命令成功但读输出失败（罕见）：显式报错，不吞读错误返回空数据
		return "", fmt.Errorf("git %s: 读取输出失败: %w", strings.Join(args, " "), rerr)
	}
	return string(data), nil
}

// removeOutputFile 删除 git 输出临时文件。
// 超时挂起时孙子进程（hook/ssh）可能仍持句柄，Windows 删除打开文件失败；此时异步延迟重试
// 删除（进程存续期内清理），避免永久泄漏系统 tmp。正常路径删除即成功，不产生额外开销。
func removeOutputFile(path string) {
	if err := os.Remove(path); err == nil || os.IsNotExist(err) {
		return
	}
	go func(p string) {
		for i := 0; i < 5; i++ { // 每分钟重试，最长 5 分钟；超过仍持句柄则放弃（罕见）
			time.Sleep(time.Minute)
			if os.Remove(p) == nil {
				return
			}
		}
	}(path)
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
		{"remote", "add", remote, "--", remoteURL}, // -- 终止选项解析，防 remoteURL 以 - 开头被当选项
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
// 仅 unborn（git 报 "Needed a single revision"）视为无提交；其他失败（仓库损坏等）返回 error，
// 避免把损坏仓库误当空仓库去对齐远程。
func (g *execGit) HasHead() (bool, error) {
	out, err := g.exec("rev-parse", "--verify", "HEAD")
	if err != nil {
		if strings.Contains(strings.ToLower(out), "needed a single revision") {
			return false, nil
		}
		return false, err
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

// GetRemoteURL 读取指定远程的实际 URL（git remote get-url）。
func (g *execGit) GetRemoteURL(remote string) (string, error) {
	return g.run("remote", "get-url", remote)
}

// CurrentBranch 返回当前分支名；分离头或无分支（空仓库 unborn）时返回 error。
func (g *execGit) CurrentBranch() (string, error) {
	out, err := g.run("branch", "--show-current")
	if err != nil {
		return "", err
	}
	if out == "" {
		return "", fmt.Errorf("当前处于分离头状态，未检出分支")
	}
	return out, nil
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
// 调用方仅在 RebaseInProgress 确认为真时调用；abort 失败会残留 rebase 状态，须上报而非吞错。
func (g *execGit) RebaseAbort() error {
	_, err := g.exec("rebase", "--abort")
	return err
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

// DiffNameOnly 列出本地 HEAD 与 remote/branch 的差异文件（Modified + Deleted）。
// 供 conflict_files 策略读取需保留为副本的本地文件：Modified 双端不同、Deleted 本地独有，
// 二者本地均存在可读；Added 是远程独有（本地无版本可保留），排除之。
// -z 以 NUL 分隔，文件名含任意字符（含非 ASCII）不被 core.quotePath 八进制转义。
func (g *execGit) DiffNameOnly(remote, branch string) ([]string, error) {
	out, err := g.run("diff", "--name-only", "-z", "--diff-filter=DM", "HEAD", remote+"/"+branch)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range strings.Split(out, "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

// NormalizeRemoteURL 把远程 URL 归一化为可比较的 host[:port]/path 形式。
// 用于核对配置 remote_url 与仓库实际远程是否一致：忽略尾部 .git、协议（ssh/https/file）、
// 用户信息（git@）与 scp 简写（git@host:path ↔ https://host/path）的差异。
// 带 scheme 的 URL 中的 host:port 是端口，保留原样；仅无 scheme 的 scp 简写才把冒号转路径分隔。
func NormalizeRemoteURL(url string) string {
	u := strings.TrimSpace(url)
	u = strings.TrimSuffix(u, "/")
	u = strings.TrimSuffix(u, ".git")
	u = strings.TrimSuffix(u, "/")
	hadScheme := false
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
		hadScheme = true
	}
	if i := strings.LastIndex(u, "@"); i >= 0 {
		u = u[i+1:]
	}
	// scp 简写 host:path → host/path；Windows 盘符（C:\）后的冒号跳过。
	// 有 scheme 时 host:port 是端口非路径分隔，不转换（否则 ssh://host:2222/path 被误拼成 host/2222/path）。
	if !hadScheme {
		if i := strings.Index(u, ":"); i >= 0 && !strings.HasPrefix(u[i+1:], "/") && !strings.HasPrefix(u[i+1:], "\\") {
			u = u[:i] + "/" + u[i+1:]
		}
	}
	if i := strings.Index(u, "/"); i >= 0 {
		return strings.ToLower(u[:i]) + u[i:]
	}
	return strings.ToLower(u)
}

// truncate 截断字符串到最大 rune 数，便于日志输出。
// 按 rune 边界截断（[]rune 切片），避免按字节切断多字节 UTF-8 序列产生乱码。
func truncate(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	r := []rune(s)
	if len(r) > maxLen {
		return string(r[:maxLen]) + "..."
	}
	return s
}
