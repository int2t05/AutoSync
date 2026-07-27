// sync_test.go 验证同步状态机主路径（真实 git 仓库 + 裸远程，无 mock）。
// 覆盖 US-002/003/004/005/007：首次初始化、本地变更推送、无变更、远程分支不存在、
// 远程领先/分叉的 rebase 合并、以及 P2 的冲突失败路径。
package tests

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"autosync/internal/config"
	"autosync/internal/gitop"
	"autosync/internal/log"
	"autosync/internal/sync"
)

// newConfig 构造测试用 Config（仅填充同步所需字段）。
func newConfig(repoDir, remoteURL string) *config.Config {
	return &config.Config{
		RepoDir:          repoDir,
		RemoteURL:        remoteURL,
		Remote:           "origin",
		Branch:           "main",
		ConflictStrategy: "local_wins",
		BackupKeep:       10,
		CommitMsgFormat:  "auto sync: {{.Timestamp}}",
	}
}

// newSyncer 为测试构造 Syncer：真实 execGit + 静默日志（无文件、无控制台，避免污染输出）。
func newSyncer(t *testing.T, cfg *config.Config) *sync.Syncer {
	t.Helper()
	logger, err := log.New("", false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(logger.Close)
	gitOp := gitop.NewExecGit(cfg.RepoDir, logger)
	return sync.NewSyncer(cfg, gitOp, logger)
}

// TestSync_FirstRun_Init 验证空目录 + 裸远程 → 初始化并首次推送（US-002）。
func TestSync_FirstRun_Init(t *testing.T) {
	repo := makeTempDir(t, "autosync-empty-*") // 空目录，非 git 仓库
	remote := makeBareRemote(t)

	result := newSyncer(t, newConfig(repo, remote)).Run()

	if result.Outcome != sync.OutcomeInitDone {
		t.Fatalf("Outcome = %s, 期望 InitDone", result.Outcome)
	}
	if !isGitRepo(t, repo) {
		t.Errorf("仓库未初始化（无 .git）")
	}
	if !remoteHasBranch(t, remote, "main") {
		t.Errorf("远程未收到首次推送")
	}
}

// TestSync_LocalChanges_Pushed 验证本地有新变更 → 自动提交并推送（US-003/US-007）。
func TestSync_LocalChanges_Pushed(t *testing.T) {
	repo := makeWorkRepo(t)
	remote := makeBareRemote(t)
	addRemote(t, repo, "origin", remote)
	pushToRemote(t, repo, "origin", "main") // 本地与远程一致

	writeFile(t, repo, "a.txt", "new content") // 制造本地变更

	result := newSyncer(t, newConfig(repo, remote)).Run()

	if result.Outcome != sync.OutcomePushed {
		t.Fatalf("Outcome = %s, 期望 Pushed", result.Outcome)
	}
	c := cloneRemote(t, remote)
	if !fileContains(t, filepath.Join(c, "a.txt"), "new content") {
		t.Errorf("远程未收到新文件 a.txt")
	}
}

// TestSync_NoChanges 验证无本地变更且与远程一致 → NoChanges。
func TestSync_NoChanges(t *testing.T) {
	repo := makeWorkRepo(t)
	remote := makeBareRemote(t)
	addRemote(t, repo, "origin", remote)
	pushToRemote(t, repo, "origin", "main")

	result := newSyncer(t, newConfig(repo, remote)).Run()

	if result.Outcome != sync.OutcomeNoChanges {
		t.Fatalf("Outcome = %s, 期望 NoChanges", result.Outcome)
	}
}

// TestSync_RemoteBranchNotExist 验证远程分支不存在 → 直接推送（US-004）。
func TestSync_RemoteBranchNotExist(t *testing.T) {
	repo := makeWorkRepo(t) // 本地有提交
	remote := makeBareRemote(t)
	addRemote(t, repo, "origin", remote) // 不 push，远程无 main

	result := newSyncer(t, newConfig(repo, remote)).Run()

	if result.Outcome != sync.OutcomePushed {
		t.Fatalf("Outcome = %s, 期望 Pushed", result.Outcome)
	}
	if !remoteHasBranch(t, remote, "main") {
		t.Errorf("远程未创建 main 分支")
	}
}

// TestSync_RemoteAhead_AutoMerged 验证远程领先（其他设备推送了新提交）→ rebase 拉取合并（US-005）。
func TestSync_RemoteAhead_AutoMerged(t *testing.T) {
	repo := makeWorkRepo(t)
	remote := makeBareRemote(t)
	addRemote(t, repo, "origin", remote)
	pushToRemote(t, repo, "origin", "main")

	pushAuxCommitToRemote(t, remote, "from_other.txt", "other") // 远程领先

	result := newSyncer(t, newConfig(repo, remote)).Run()

	if result.Outcome != sync.OutcomeAutoMerged {
		t.Fatalf("Outcome = %s, 期望 AutoMerged", result.Outcome)
	}
	if !fileExists(t, filepath.Join(repo, "from_other.txt")) {
		t.Errorf("本地未拉取到远程新文件 from_other.txt")
	}
}

// TestSync_Diverged_AutoMerged 验证双方各有独立提交（不同文件）→ rebase 成功合并（US-005）。
func TestSync_Diverged_AutoMerged(t *testing.T) {
	repo := makeWorkRepo(t)
	remote := makeBareRemote(t)
	addRemote(t, repo, "origin", remote)
	pushToRemote(t, repo, "origin", "main")

	commitFile(t, repo, "local.txt", "local")            // 本地新提交（不推送）
	pushAuxCommitToRemote(t, remote, "remote.txt", "remote") // 远程新提交（不同文件）

	result := newSyncer(t, newConfig(repo, remote)).Run()

	if result.Outcome != sync.OutcomeAutoMerged {
		t.Fatalf("Outcome = %s, 期望 AutoMerged", result.Outcome)
	}
	if !fileExists(t, filepath.Join(repo, "local.txt")) {
		t.Errorf("本地提交 local.txt 丢失")
	}
	if !fileExists(t, filepath.Join(repo, "remote.txt")) {
		t.Errorf("未合并远程 remote.txt")
	}
	c := cloneRemote(t, remote)
	if !fileExists(t, filepath.Join(c, "local.txt")) || !fileExists(t, filepath.Join(c, "remote.txt")) {
		t.Errorf("远程未包含合并后的全部文件")
	}
}

// TestSync_Conflict_LocalWins 验证 local_wins：备份远程旧版本 + --force-with-lease 推送本地（US-006）。
func TestSync_Conflict_LocalWins(t *testing.T) {
	repo := makeWorkRepo(t)
	remote := makeBareRemote(t)
	addRemote(t, repo, "origin", remote)
	pushToRemote(t, repo, "origin", "main")
	commitFile(t, repo, "same.txt", "LOCAL")
	pushAuxCommitToRemote(t, remote, "same.txt", "REMOTE")

	result := newSyncer(t, newConfig(repo, remote)).Run()

	if result.Outcome != sync.OutcomeConflictResolved {
		t.Fatalf("Outcome = %s, 期望 ConflictResolved", result.Outcome)
	}
	if result.BackupBranch == "" {
		t.Fatal("未返回备份分支名")
	}
	// 本地胜出：same.txt 仍为 LOCAL
	if !fileContains(t, filepath.Join(repo, "same.txt"), "LOCAL") {
		t.Errorf("local_wins 后本地 same.txt 应为 LOCAL")
	}
	// 远程被本地覆盖：远程 same.txt 应为 LOCAL
	c := cloneRemote(t, remote)
	if !fileContains(t, filepath.Join(c, "same.txt"), "LOCAL") {
		t.Errorf("远程应被本地覆盖，same.txt 应为 LOCAL")
	}
	// 备份分支存在并保存远程旧版本（REMOTE）
	if !remoteHasBranch(t, remote, result.BackupBranch) {
		t.Errorf("备份分支 %s 未推送到远程", result.BackupBranch)
	}
	bc := cloneRemote(t, remote)
	runGit(t, bc, "checkout", result.BackupBranch)
	if !fileContains(t, filepath.Join(bc, "same.txt"), "REMOTE") {
		t.Errorf("备份分支应保存远程旧版本（same.txt=REMOTE）")
	}
}

// TestSync_Conflict_RemoteWins 验证 remote_wins：放弃本地，重置到远程（US-006）。
func TestSync_Conflict_RemoteWins(t *testing.T) {
	repo := makeWorkRepo(t)
	remote := makeBareRemote(t)
	addRemote(t, repo, "origin", remote)
	pushToRemote(t, repo, "origin", "main")
	commitFile(t, repo, "same.txt", "LOCAL")
	pushAuxCommitToRemote(t, remote, "same.txt", "REMOTE")

	cfg := newConfig(repo, remote)
	cfg.ConflictStrategy = "remote_wins"
	result := newSyncer(t, cfg).Run()

	if result.Outcome != sync.OutcomeConflictResolved {
		t.Fatalf("Outcome = %s, 期望 ConflictResolved", result.Outcome)
	}
	// 远程胜出：本地 same.txt 被重置为 REMOTE
	if !fileContains(t, filepath.Join(repo, "same.txt"), "REMOTE") {
		t.Errorf("remote_wins 后本地 same.txt 应为 REMOTE")
	}
}

// TestSync_Conflict_Abort 验证 abort：不做变更，本地保留、远程不变（US-006）。
func TestSync_Conflict_Abort(t *testing.T) {
	repo := makeWorkRepo(t)
	remote := makeBareRemote(t)
	addRemote(t, repo, "origin", remote)
	pushToRemote(t, repo, "origin", "main")
	commitFile(t, repo, "same.txt", "LOCAL")
	pushAuxCommitToRemote(t, remote, "same.txt", "REMOTE")

	cfg := newConfig(repo, remote)
	cfg.ConflictStrategy = "abort"
	result := newSyncer(t, cfg).Run()

	if result.Outcome != sync.OutcomeConflictAborted {
		t.Fatalf("Outcome = %s, 期望 ConflictAborted", result.Outcome)
	}
	if !fileContains(t, filepath.Join(repo, "same.txt"), "LOCAL") {
		t.Errorf("abort 后本地 same.txt 应仍为 LOCAL")
	}
	c := cloneRemote(t, remote)
	if !fileContains(t, filepath.Join(c, "same.txt"), "REMOTE") {
		t.Errorf("abort 后远程应仍为 REMOTE")
	}
	if inRebase(t, repo) {
		t.Errorf("abort 后不应处于 rebase 状态")
	}
}

// TestSync_BackupCleanup 验证 local_wins 后清理旧备份分支，保留最新 backup_keep 个（US-006）。
func TestSync_BackupCleanup(t *testing.T) {
	repo := makeWorkRepo(t)
	remote := makeBareRemote(t)
	addRemote(t, repo, "origin", remote)
	pushToRemote(t, repo, "origin", "main")

	// 预创建 12 个备份分支（时间戳均早于当前）
	for i := 1; i <= 12; i++ {
		name := fmt.Sprintf("backup/remote-20260101_0000%02d", i)
		runGit(t, repo, "branch", name, "origin/main")
		runGit(t, repo, "push", "origin", name)
	}

	// 制造冲突触发 local_wins（再建 1 个备份，共 13，清理后留 10）
	commitFile(t, repo, "same.txt", "LOCAL")
	pushAuxCommitToRemote(t, remote, "same.txt", "REMOTE")

	result := newSyncer(t, newConfig(repo, remote)).Run()
	if result.Outcome != sync.OutcomeConflictResolved {
		t.Fatalf("Outcome = %s, 期望 ConflictResolved", result.Outcome)
	}

	c := cloneRemote(t, remote)
	out := runGit(t, c, "branch", "-r")
	count := strings.Count(out, "backup/remote-")
	if count != 10 {
		t.Errorf("备份分支数 = %d, 期望 10（清理后）\n%s", count, out)
	}
	if strings.Contains(out, "backup/remote-20260101_000001") {
		t.Errorf("最早的备份 T01 应被清理")
	}
}
