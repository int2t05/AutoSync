// sync_test.go 验证同步状态机主路径（真实 git 仓库 + 裸远程，无 mock）。
// 覆盖：首次初始化、本地变更推送、无变更、远程分支不存在、
// 远程领先/分叉的 rebase 合并、冲突三策略与备份清理、dry-run 只读。
package tests

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
// git 超时给 1 分钟：本地测试仓库命令毫秒级完成，仅防挂起无限期阻塞测试。
func newSyncer(t *testing.T, cfg *config.Config) *sync.Syncer {
	t.Helper()
	logger, err := log.New("", false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(logger.Close)
	gitOp := gitop.NewExecGit(cfg.RepoDir, logger, time.Minute)
	return sync.NewSyncer(cfg, gitOp, logger)
}

// TestSync_FirstRun_Init 验证空目录 + 裸远程 → 初始化并首次推送。
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

// TestSync_LocalChanges_Pushed 验证本地有新变更 → 自动提交并推送。
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

// TestSync_RemoteBranchNotExist 验证远程分支不存在 → 直接推送。
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

// TestSync_RemoteAhead_AutoMerged 验证远程领先（其他设备推送了新提交）→ rebase 拉取合并。
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

// TestSync_Diverged_AutoMerged 验证双方各有独立提交（不同文件）→ rebase 成功合并。
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

// TestSync_Conflict_LocalWins 验证 local_wins：备份远程旧版本 + --force-with-lease 推送本地。
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

// TestSync_Conflict_RemoteWins 验证 remote_wins：放弃本地，重置到远程。
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

// TestSync_Conflict_Files 验证 conflict_files：本地版落副本，远程版生效，副本入 git 推送。
func TestSync_Conflict_Files(t *testing.T) {
	repo := makeWorkRepo(t)
	remote := makeBareRemote(t)
	addRemote(t, repo, "origin", remote)
	pushToRemote(t, repo, "origin", "main")
	commitFile(t, repo, "same.txt", "LOCAL")
	pushAuxCommitToRemote(t, remote, "same.txt", "REMOTE")

	cfg := newConfig(repo, remote)
	cfg.ConflictStrategy = "conflict_files"
	result := newSyncer(t, cfg).Run()

	if result.Outcome != sync.OutcomeConflictResolved {
		t.Fatalf("Outcome = %s, 期望 ConflictResolved", result.Outcome)
	}
	// 远程胜出：主文件 same.txt 为 REMOTE
	if !fileContains(t, filepath.Join(repo, "same.txt"), "REMOTE") {
		t.Errorf("conflict_files 后主文件 same.txt 应为 REMOTE")
	}
	// 副本存在且内容为 LOCAL
	matches, err := filepath.Glob(filepath.Join(repo, "same.sync-conflict-*.txt"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("应存在 1 个 same.sync-conflict-*.txt 副本, got %d (err=%v)", len(matches), err)
	}
	if !fileContains(t, matches[0], "LOCAL") {
		t.Errorf("副本内容应为 LOCAL")
	}
	// 副本入 git 推送：克隆远程验证
	c := cloneRemote(t, remote)
	if !fileContains(t, filepath.Join(c, "same.txt"), "REMOTE") {
		t.Errorf("远程主文件应为 REMOTE")
	}
	cMatches, _ := filepath.Glob(filepath.Join(c, "same.sync-conflict-*.txt"))
	if len(cMatches) != 1 || !fileContains(t, cMatches[0], "LOCAL") {
		t.Errorf("远程应含副本且内容为 LOCAL, got %d matches", len(cMatches))
	}
}

// TestSync_BackupCleanup 验证 local_wins 后清理旧备份分支，保留最新 backup_keep 个。
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

// TestSync_FetchPrune_RemoteBranchDeleted 验证远程分支被删除后同步能重建而非误报"已是最新"。
// 无 --prune 时 fetch 保留陈旧 origin/main 跟踪引用 → 误判 UpToDate；--prune 清除后应重新推送。
func TestSync_FetchPrune_RemoteBranchDeleted(t *testing.T) {
	repo := makeWorkRepo(t)
	remote := makeBareRemote(t)
	addRemote(t, repo, "origin", remote)
	pushToRemote(t, repo, "origin", "main")

	// 另一设备删除远程 main 分支
	runGit(t, remote, "update-ref", "-d", "refs/heads/main")

	result := newSyncer(t, newConfig(repo, remote)).Run()

	if result.Outcome != sync.OutcomePushed {
		t.Fatalf("Outcome = %s, 期望 Pushed（--prune 清除陈旧引用后重建分支）", result.Outcome)
	}
	if !remoteHasBranch(t, remote, "main") {
		t.Errorf("远程 main 分支应被重新创建")
	}
}

// TestSync_DryRun_ReadOnly 验证 dry-run 只读：报告计划但不提交、不推送。
// UpToDate 状态下制造未提交变更，断言 HEAD 与远程均未改变，且计划提示将提交。
func TestSync_DryRun_ReadOnly(t *testing.T) {
	repo := makeWorkRepo(t)
	remote := makeBareRemote(t)
	addRemote(t, repo, "origin", remote)
	pushToRemote(t, repo, "origin", "main") // 本地与远程一致

	beforeHEAD := headShort(t, repo)
	beforeRemote := remoteHeadCount(t, remote)
	writeFile(t, repo, "uncommitted.txt", "x") // 本地未提交变更

	plan := newSyncer(t, newConfig(repo, remote)).DryRun()

	// HEAD 未移动（dry-run 不提交）
	if after := headShort(t, repo); after != beforeHEAD {
		t.Errorf("dry-run 不应提交，HEAD 变化 %s → %s", beforeHEAD, after)
	}
	// 远程未变（dry-run 不推送）
	if after := remoteHeadCount(t, remote); after != beforeRemote {
		t.Errorf("dry-run 不应推送，远程提交数变化 %d → %d", beforeRemote, after)
	}
	// 计划应提示有变更待提交，且判定与远程一致无需推送
	joined := strings.Join(plan.Steps, "\n")
	if !strings.Contains(joined, "将提交本地变更") {
		t.Errorf("计划应提示将提交本地变更\n%s", joined)
	}
	if !strings.Contains(joined, "UpToDate") {
		t.Errorf("计划应判定为 UpToDate\n%s", joined)
	}
}
