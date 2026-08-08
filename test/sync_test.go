// sync_test.go 验证同步状态机主路径（真实 git 仓库 + 裸远程，无 mock）。
// 覆盖：首次初始化、本地变更推送、无变更、远程分支不存在、
// 远程领先/分叉的 rebase 合并、冲突三策略与备份清理、dry-run 只读。
package tests

import (
	"fmt"
	"os"
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
		BackupKeep:       intPtr(10),
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

	writeFile(t, repo, "untracked.txt", "junk") // 未跟踪文件，验证 clean -fd 清除
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
	// clean -fd 清除未跟踪文件（untracked.txt 不在远程，重置后成为未跟踪并被清理）
	if fileExists(t, filepath.Join(repo, "untracked.txt")) {
		t.Errorf("remote_wins 应 clean -fd 清除未跟踪文件 untracked.txt")
	}
}

// TestSync_ConflictFiles_RemoteDeleted_Preserved 验证 conflict_files 的 Deleted(D) 分支：
// 远程删除的文件（gone.txt）本地仍有版本，冲突时须保留为副本——reset 后本地版不丢。
func TestSync_ConflictFiles_RemoteDeleted_Preserved(t *testing.T) {
	repo := makeWorkRepo(t)
	remote := makeBareRemote(t)
	addRemote(t, repo, "origin", remote)
	pushToRemote(t, repo, "origin", "main")
	commitFile(t, repo, "gone.txt", "v1")
	runGit(t, repo, "push", "origin", "main") // 远程先有 gone.txt
	commitFile(t, repo, "same.txt", "LOCAL")  // 本地新提交（不推送）

	// 远程删除 gone.txt 并改写 same.txt（从 clone 提交，隔离本地）
	c := cloneRemote(t, remote)
	runGit(t, c, "rm", "gone.txt")
	writeFile(t, c, "same.txt", "REMOTE")
	runGit(t, c, "add", "-A")
	runGit(t, c, "commit", "-m", "remote: delete gone, change same")
	runGit(t, c, "push", "origin", "main")

	cfg := newConfig(repo, remote)
	cfg.ConflictStrategy = "conflict_files"
	result := newSyncer(t, cfg).Run()

	if result.Outcome != sync.OutcomeConflictResolved {
		t.Fatalf("Outcome = %s, 期望 ConflictResolved", result.Outcome)
	}
	// 主文件 same.txt 为 REMOTE；远程已删除的 gone.txt 被重置后本地也不存在
	if !fileContains(t, filepath.Join(repo, "same.txt"), "REMOTE") {
		t.Errorf("主文件 same.txt 应为 REMOTE")
	}
	if fileExists(t, filepath.Join(repo, "gone.txt")) {
		t.Errorf("remote 已删除 gone.txt，重置后本地不应存在")
	}
	// 本地版保留为副本：gone 副本内容为 v1
	gMatches, err := filepath.Glob(filepath.Join(repo, "gone.sync-conflict-*.txt"))
	if err != nil || len(gMatches) != 1 {
		t.Fatalf("应存在 1 个 gone.sync-conflict-*.txt 副本, got %d (err=%v)", len(gMatches), err)
	}
	if !fileContains(t, gMatches[0], "v1") {
		t.Errorf("gone 副本内容应为 v1（本地版保留）")
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

// TestSync_PullFail_NotConflict_NoReset 验证非冲突的 pull 失败（pre-rebase 钩子拒绝）不被当 rebase 冲突处理：
// remote_wins 策略下不再 reset --hard 销毁本地已提交工作，而是显式 Failed。
func TestSync_PullFail_NotConflict_NoReset(t *testing.T) {
	repo := makeWorkRepo(t)
	remote := makeBareRemote(t)
	addRemote(t, repo, "origin", remote)
	pushToRemote(t, repo, "origin", "main")
	commitFile(t, repo, "local.txt", "local")               // 本地已提交
	pushAuxCommitToRemote(t, remote, "remote.txt", "remote") // 远程领先 → Diverged

	// pre-rebase 钩子直接拒绝：rebase 未开始即失败，属非冲突原因
	hooksDir := filepath.Join(repo, ".git", "hooks")
	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, hooksDir, "pre-rebase", "#!/bin/sh\nexit 1\n")

	cfg := newConfig(repo, remote)
	cfg.ConflictStrategy = "remote_wins"
	beforeHEAD := headShort(t, repo)
	result := newSyncer(t, cfg).Run()

	if result.Outcome != sync.OutcomeFailed {
		t.Fatalf("Outcome = %s, 期望 Failed（非冲突 pull 失败不得触发 remote_wins 销毁）", result.Outcome)
	}
	if after := headShort(t, repo); after != beforeHEAD {
		t.Errorf("HEAD 不应移动 %s → %s", beforeHEAD, after)
	}
	if !fileExists(t, filepath.Join(repo, "local.txt")) {
		t.Errorf("本地已提交文件 local.txt 不应被 reset --hard 销毁")
	}
}

// TestSync_MergeBaseFail_NoForcePush 验证无共同祖先（远程被换/重写）时绝不自动 rebase/force push：
// local_wins 策略下远程 main 保持原样，同步显式 Failed。
func TestSync_MergeBaseFail_NoForcePush(t *testing.T) {
	repo := makeWorkRepo(t)                                       // 本地独立根提交（README）
	remote := makeBareRemote(t)                                   // 空裸远程
	pushAuxCommitToRemote(t, remote, "remote.txt", "remote")       // 远程独立根提交，与本地无共同祖先
	addRemote(t, repo, "origin", remote)

	beforeRemote := runGit(t, remote, "rev-parse", "main")
	result := newSyncer(t, newConfig(repo, remote)).Run()

	if result.Outcome != sync.OutcomeFailed {
		t.Fatalf("Outcome = %s, 期望 Failed（无共同祖先不得自动合并/强推）", result.Outcome)
	}
	if after := runGit(t, remote, "rev-parse", "main"); after != beforeRemote {
		t.Errorf("远程 main 应保持不变 %s → %s", beforeRemote, after)
	}
}

// TestSync_ConflictFiles_LocalOnlyFile_Preserved 验证 conflict_files 保留本地独有文件为副本：
// 本地新增 local.txt + 远程改 same.txt 冲突 → local.txt 必须落 .sync-conflict 副本（此前被 reset+clean 静默删除）。
func TestSync_ConflictFiles_LocalOnlyFile_Preserved(t *testing.T) {
	repo := makeWorkRepo(t)
	remote := makeBareRemote(t)
	addRemote(t, repo, "origin", remote)
	pushToRemote(t, repo, "origin", "main")
	commitFile(t, repo, "local.txt", "local") // 本地独有文件（Added）
	commitFile(t, repo, "same.txt", "LOCAL")  // 与远程冲突的文件（Modified）
	pushAuxCommitToRemote(t, remote, "same.txt", "REMOTE")

	cfg := newConfig(repo, remote)
	cfg.ConflictStrategy = "conflict_files"
	result := newSyncer(t, cfg).Run()

	if result.Outcome != sync.OutcomeConflictResolved {
		t.Fatalf("Outcome = %s, 期望 ConflictResolved", result.Outcome)
	}
	if !fileContains(t, filepath.Join(repo, "same.txt"), "REMOTE") {
		t.Errorf("主文件 same.txt 应为 REMOTE")
	}
	// 本地独有文件落副本，内容保留
	matches, err := filepath.Glob(filepath.Join(repo, "local.sync-conflict-*.txt"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("应存在 1 个 local.sync-conflict-*.txt 副本, got %d (err=%v)", len(matches), err)
	}
	if !fileContains(t, matches[0], "local") {
		t.Errorf("本地独有文件副本内容应为 'local'")
	}
}

// TestSync_EmptyRepo_NoHead_PullRemote 验证空仓库（用户手动 git init、无提交）+ 远程已有分支：
// 拉取远程分支对齐，不再永久 Failed。
func TestSync_EmptyRepo_NoHead_PullRemote(t *testing.T) {
	dir := makeTempDir(t, "autosync-emptyhead-*")
	runGit(t, dir, "init", "-b", "main") // 空仓库，无提交（unborn HEAD）
	remote := makeBareRemote(t)
	pushAuxCommitToRemote(t, remote, "remote.txt", "remote") // 远程已有提交
	addRemote(t, dir, "origin", remote)

	result := newSyncer(t, newConfig(dir, remote)).Run()

	if result.Outcome == sync.OutcomeFailed {
		t.Fatalf("空仓库拉取远程不应 Failed: %s", result.Message)
	}
	if !fileExists(t, filepath.Join(dir, "remote.txt")) {
		t.Errorf("本地应已拉取远程文件 remote.txt")
	}
}

// TestSync_EmptyRepo_NoHead_PushFirst 验证空仓库 + 空远程：完成首次提交推送。
func TestSync_EmptyRepo_NoHead_PushFirst(t *testing.T) {
	dir := makeTempDir(t, "autosync-emptyhead-*")
	runGit(t, dir, "init", "-b", "main")
	remote := makeBareRemote(t) // 空远程
	addRemote(t, dir, "origin", remote)

	result := newSyncer(t, newConfig(dir, remote)).Run()

	if result.Outcome != sync.OutcomePushed {
		t.Fatalf("Outcome = %s, 期望 Pushed（空仓库首推）: %s", result.Outcome, result.Message)
	}
	if !remoteHasBranch(t, remote, "main") {
		t.Errorf("远程应创建 main 分支")
	}
}

// TestNormalizeRemoteURL 验证远程 URL 归一化：协议/用户信息/尾部 .git 差异视为等价。
func TestNormalizeRemoteURL(t *testing.T) {
	cases := []struct{ a, b string }{
		{"https://github.com/int2t05/File.git", "git@github.com:int2t05/File"},
		{"https://github.com/int2t05/File", "https://github.com/int2t05/File.git"},
		{"ssh://git@github.com/int2t05/File", "github.com:int2t05/File"},
		// 非默认端口：带 scheme 的 host:port 保留端口，协议/用户差异仍等价（修复前误拼成 host/2222/path）
		{"ssh://git@host:2222/path", "https://host:2222/path"},
	}
	for _, c := range cases {
		na, nb := gitop.NormalizeRemoteURL(c.a), gitop.NormalizeRemoteURL(c.b)
		if na != nb {
			t.Errorf("归一化不等: %q vs %q → %q vs %q", c.a, c.b, na, nb)
		}
	}
}

// TestSync_RemoteURLMismatch_Fail 验证配置 remote_url 与仓库实际远程不一致 → 显式 Failed（此前静默失效）。
func TestSync_RemoteURLMismatch_Fail(t *testing.T) {
	repo := makeWorkRepo(t)
	remote := makeBareRemote(t)
	addRemote(t, repo, "origin", remote)
	pushToRemote(t, repo, "origin", "main")

	cfg := newConfig(repo, remote)
	cfg.RemoteURL = remote + "/other" // 与实际远程不一致
	result := newSyncer(t, cfg).Run()

	if result.Outcome != sync.OutcomeFailed {
		t.Fatalf("Outcome = %s, 期望 Failed（远程地址不一致不得静默继续）", result.Outcome)
	}
}

// TestSync_RemoteURLEquivalent_Pass 验证等价 URL（尾部 .git 差异）不被误判为不一致。
func TestSync_RemoteURLEquivalent_Pass(t *testing.T) {
	base := makeTempDir(t, "autosync-eq-*")
	remoteDir := filepath.Join(base, "remote.git")
	runGit(t, base, "init", "--bare", "-b", "main", remoteDir)
	repo := makeWorkRepo(t)
	addRemote(t, repo, "origin", remoteDir)
	pushToRemote(t, repo, "origin", "main")

	cfg := newConfig(repo, remoteDir)
	cfg.RemoteURL = strings.TrimSuffix(remoteDir, ".git") // 等价形式
	result := newSyncer(t, cfg).Run()

	if result.Outcome == sync.OutcomeFailed {
		t.Fatalf("等价 URL 不应判定为不一致: %s", result.Message)
	}
}

// TestSync_RemoteMissing_Fail 验证配置的远程名不存在 → 显式 Failed（此前无限静默"下次重试"）。
func TestSync_RemoteMissing_Fail(t *testing.T) {
	repo := makeWorkRepo(t)
	remote := makeBareRemote(t)
	addRemote(t, repo, "origin", remote)
	pushToRemote(t, repo, "origin", "main")

	cfg := newConfig(repo, remote)
	cfg.Remote = "upstream" // 仓库只有 origin
	result := newSyncer(t, cfg).Run()

	if result.Outcome != sync.OutcomeFailed {
		t.Fatalf("Outcome = %s, 期望 Failed（远程名不存在为配置错误）", result.Outcome)
	}
}

// TestSync_BranchMismatch_Fail 验证本地当前分支与配置 branch 不一致 → 显式 Failed，且无写操作。
func TestSync_BranchMismatch_Fail(t *testing.T) {
	repo := makeWorkRepo(t)
	remote := makeBareRemote(t)
	addRemote(t, repo, "origin", remote)
	pushToRemote(t, repo, "origin", "main")
	runGit(t, repo, "checkout", "-b", "dev") // 当前分支 dev，配置 branch=main

	result := newSyncer(t, newConfig(repo, remote)).Run()

	if result.Outcome != sync.OutcomeFailed {
		t.Fatalf("Outcome = %s, 期望 Failed（分支不一致不得跨分支 rebase）", result.Outcome)
	}
	if cur := runGit(t, repo, "branch", "--show-current"); cur != "dev" {
		t.Errorf("HEAD 不应被改动，当前分支 = %s", cur)
	}
}

// TestSync_FetchFail_DegradesToNoChanges 验证 Fetch 失败（远程不可达）降级为 NoChanges：
// 不报 Failed 触发错误通知，避免网络抖动频繁打扰用户（flow.md 状态机含该路径）。
func TestSync_FetchFail_DegradesToNoChanges(t *testing.T) {
	repo := makeWorkRepo(t)
	deadRemote := filepath.Join(t.TempDir(), "missing.git") // 不存在的远程路径
	addRemote(t, repo, "origin", deadRemote)
	writeFile(t, repo, "a.txt", "x") // 本地未提交变更（将被提交）

	cfg := newConfig(repo, deadRemote) // RemoteURL 与实际远程一致（同指向不存在的路径）
	result := newSyncer(t, cfg).Run()

	if result.Outcome != sync.OutcomeNoChanges {
		t.Fatalf("Outcome = %s, 期望 NoChanges（fetch 失败降级，不报 Failed）", result.Outcome)
	}
	if !strings.Contains(result.Message, "远程暂不可达") {
		t.Errorf("消息应提示远程不可达: %s", result.Message)
	}
}

// TestSync_DryRun_PlanBranches 表驱动覆盖 dry-run 各状态分支（init/无 HEAD/配置错误/远程领先/分叉/本地领先）。
// dry-run 不联网：远程领先/分叉/本地领先场景先 fetch 使本地跟踪引用与远程一致。
func TestSync_DryRun_PlanBranches(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(t *testing.T) *config.Config
		expect string // 计划中应包含的步骤子串
	}{
		{
			name: "init 空目录空远程",
			setup: func(t *testing.T) *config.Config {
				repo := makeTempDir(t, "autosync-dryrun-*") // 空目录，非 git 仓库
				remote := makeBareRemote(t)
				return newConfig(repo, remote)
			},
			expect: "首次运行：将初始化仓库",
		},
		{
			name: "无 HEAD 远程有提交",
			setup: func(t *testing.T) *config.Config {
				repo := makeTempDir(t, "autosync-dryrun-*")
				runGit(t, repo, "init", "-b", "main") // 空仓库，无提交
				remote := makeBareRemote(t)
				pushAuxCommitToRemote(t, remote, "remote.txt", "remote")
				addRemote(t, repo, "origin", remote)
				return newConfig(repo, remote)
			},
			expect: "仓库无提交：将拉取远程分支对齐",
		},
		{
			name: "远程名不存在（配置错误）",
			setup: func(t *testing.T) *config.Config {
				repo := makeWorkRepo(t)
				remote := makeBareRemote(t)
				addRemote(t, repo, "origin", remote)
				cfg := newConfig(repo, remote)
				cfg.Remote = "upstream" // 仓库只有 origin
				return cfg
			},
			expect: "远程 upstream 不存在",
		},
		{
			name: "远程地址与配置不一致",
			setup: func(t *testing.T) *config.Config {
				repo := makeWorkRepo(t)
				remote := makeBareRemote(t)
				addRemote(t, repo, "origin", remote)
				pushToRemote(t, repo, "origin", "main")
				cfg := newConfig(repo, remote)
				cfg.RemoteURL = remote + "/other"
				return cfg
			},
			expect: "远程地址与配置不一致",
		},
		{
			name: "当前分支与配置不一致",
			setup: func(t *testing.T) *config.Config {
				repo := makeWorkRepo(t)
				remote := makeBareRemote(t)
				addRemote(t, repo, "origin", remote)
				pushToRemote(t, repo, "origin", "main")
				runGit(t, repo, "checkout", "-b", "dev") // 当前分支 dev
				return newConfig(repo, remote)
			},
			expect: "当前分支与配置不一致",
		},
		{
			name: "远程领先 RemoteAhead",
			setup: func(t *testing.T) *config.Config {
				repo := makeWorkRepo(t)
				remote := makeBareRemote(t)
				addRemote(t, repo, "origin", remote)
				pushToRemote(t, repo, "origin", "main")
				pushAuxCommitToRemote(t, remote, "remote.txt", "remote")
				runGit(t, repo, "fetch", "origin") // 本地跟踪引用对齐远程
				return newConfig(repo, remote)
			},
			expect: "远程有新提交（RemoteAhead）：将 pull --rebase",
		},
		{
			name: "真正分叉 Diverged",
			setup: func(t *testing.T) *config.Config {
				repo := makeWorkRepo(t)
				remote := makeBareRemote(t)
				addRemote(t, repo, "origin", remote)
				pushToRemote(t, repo, "origin", "main")
				commitFile(t, repo, "local.txt", "local")            // 本地新提交
				pushAuxCommitToRemote(t, remote, "remote.txt", "remote") // 远程新提交
				runGit(t, repo, "fetch", "origin")
				return newConfig(repo, remote)
			},
			expect: "远程有新提交（Diverged）：将 pull --rebase",
		},
		{
			name: "本地领先 LocalAhead",
			setup: func(t *testing.T) *config.Config {
				repo := makeWorkRepo(t)
				remote := makeBareRemote(t)
				addRemote(t, repo, "origin", remote)
				pushToRemote(t, repo, "origin", "main")
				commitFile(t, repo, "local.txt", "local") // 本地新提交
				runGit(t, repo, "fetch", "origin")        // 远程仍落后
				return newConfig(repo, remote)
			},
			expect: "本地领先（LocalAhead）：将 push（快进）",
		},
		{
			name: "一致 UpToDate",
			setup: func(t *testing.T) *config.Config {
				repo := makeWorkRepo(t)
				remote := makeBareRemote(t)
				addRemote(t, repo, "origin", remote)
				pushToRemote(t, repo, "origin", "main")
				runGit(t, repo, "fetch", "origin")
				return newConfig(repo, remote)
			},
			expect: "本地与远程一致（UpToDate）：无需推送",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := c.setup(t)
			plan := newSyncer(t, cfg).DryRun()
			joined := strings.Join(plan.Steps, "\n")
			if !strings.Contains(joined, c.expect) {
				t.Errorf("计划应包含 %q\n%s", c.expect, joined)
			}
		})
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
