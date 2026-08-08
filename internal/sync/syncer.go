// syncer.go 实现同步状态机主路径：init → commit → fetch → 关系判定 → push/rebase。
// rebase 冲突时中止并按 conflict_strategy 处理。
package sync

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"autosync/internal/config"
	"autosync/internal/gitop"
	"autosync/internal/log"
)

// Syncer 是单次同步的编排器，依赖 GitOperator 接口（依赖倒置）。
type Syncer struct {
	cfg    *config.Config
	git    gitop.GitOperator
	logger *log.Logger
}

// NewSyncer 构造同步器。
func NewSyncer(cfg *config.Config, git gitop.GitOperator, logger *log.Logger) *Syncer {
	return &Syncer{cfg: cfg, git: git, logger: logger}
}

// Run 执行单次同步流程，返回结果。
func (s *Syncer) Run() SyncResult {
	s.logger.Info("========== 同步开始 ==========")
	s.logger.Infof("目录=%s 策略=%s", s.cfg.RepoDir, s.cfg.ConflictStrategy)

	// S1：首次运行初始化
	if !s.git.IsRepo() {
		s.logger.Info("首次运行，初始化仓库...")
		if err := s.git.Init(s.cfg.Remote, s.cfg.RemoteURL, s.cfg.Branch); err != nil {
			return s.fail("初始化失败", err)
		}
		return SyncResult{Outcome: OutcomeInitDone, Message: "初始化完成，已推送首次同步"}
	}

	// C1+C4：配置 ↔ 仓库一致性核对——改 remote_url/branch 显式失败而非静默失效，
	// 且拒绝在无关远程/分支上做任何写操作（防把文件推到用户自己的其他仓库 / 跨分支 rebase 重写历史）
	remoteURL, err := s.git.GetRemoteURL(s.cfg.Remote)
	if err != nil {
		return s.fail(fmt.Sprintf("远程 %q 不存在（配置错误）", s.cfg.Remote), err)
	}
	if gitop.NormalizeRemoteURL(remoteURL) != gitop.NormalizeRemoteURL(s.cfg.RemoteURL) {
		return s.fail("远程地址与配置不一致", fmt.Errorf("仓库=%s 配置=%s", remoteURL, s.cfg.RemoteURL))
	}
	curBranch, err := s.git.CurrentBranch()
	if err != nil {
		return s.fail("获取当前分支失败", err)
	}
	if curBranch != s.cfg.Branch {
		return s.fail("当前分支与配置不一致", fmt.Errorf("本地=%s 配置=%s", curBranch, s.cfg.Branch))
	}

	// S1.5：仓库无 HEAD 提交（用户手动 git init 的空仓库）——对齐远程分支或首推，避免永久 Failed
	hasHead, err := s.git.HasHead()
	if err != nil {
		return s.fail("检查 HEAD 失败", err)
	}
	if !hasHead {
		s.logger.Info("仓库无提交，尝试对齐远程...")
		if err := s.git.Fetch(s.cfg.Remote); err != nil {
			s.logger.Warnf("拉取远程失败（下次重试）: %v", err)
			return SyncResult{Outcome: OutcomeNoChanges, Message: "本地无提交，远程暂不可达，下次重试"}
		}
		exists, err := s.git.RemoteBranchExists(s.cfg.Remote, s.cfg.Branch)
		if err != nil {
			return s.fail("检查远程分支失败", err)
		}
		if exists {
			if err := s.git.CheckoutRemote(s.cfg.Remote, s.cfg.Branch); err != nil {
				return s.fail("拉取远程分支失败", err)
			}
			s.logger.Info("已对齐远程分支，继续同步本地文件...")
		} else {
			if err := s.git.PushFirst(s.cfg.Remote, s.cfg.Branch); err != nil {
				return s.fail("初始化推送失败", err)
			}
			return SyncResult{Outcome: OutcomePushed, Message: "初始化完成（空仓库首推）"}
		}
	}

	// S2：暂存并提交本地变更
	if err := s.git.StageAll(); err != nil {
		return s.fail("暂存失败", err)
	}
	hasChanges, err := s.git.HasChanges()
	if err != nil {
		return s.fail("检查变更失败", err)
	}
	if hasChanges {
		if err := s.git.Commit(formatCommitMsg(s.cfg.CommitMsgFormat)); err != nil {
			return s.fail("提交失败", err)
		}
		s.logger.Info("本地变更已提交")
	} else {
		s.logger.Info("本地无新变更")
	}

	// S3：拉取远程引用。网络瞬态失败时降级：本地已提交，远程未同步，下次重试，
	// 不报 Failed 触发错误通知，避免网络抖动频繁打扰用户。
	if err := s.git.Fetch(s.cfg.Remote); err != nil {
		s.logger.Warnf("拉取远程失败（下次重试）: %v", err)
		return SyncResult{Outcome: OutcomeNoChanges, Message: "本地已提交，远程暂不可达，下次重试"}
	}

	// S4：远程分支是否存在
	exists, err := s.git.RemoteBranchExists(s.cfg.Remote, s.cfg.Branch)
	if err != nil {
		return s.fail("检查远程分支失败", err)
	}
	if !exists {
		s.logger.Info("远程分支不存在，直接推送...")
		if err := s.git.Push(s.cfg.Remote, s.cfg.Branch); err != nil {
			return s.fail("推送失败", err)
		}
		return SyncResult{Outcome: OutcomePushed, Message: "同步完成（新建远程分支）"}
	}

	// S5：判定本地与远程关系并据此推送或合并
	rel, err := s.git.RelationTo(s.cfg.Remote, s.cfg.Branch)
	if err != nil {
		return s.fail("检查分叉状态失败", err)
	}
	switch rel {
	case gitop.RelUpToDate:
		// 本地与远程一致，无需推送
		return SyncResult{Outcome: OutcomeNoChanges, Message: "已是最新，无需同步"}

	case gitop.RelLocalAhead:
		// 本地领先，直接快进推送
		s.logger.Info("本地领先，直接推送...")
		if err := s.git.Push(s.cfg.Remote, s.cfg.Branch); err != nil {
			return s.fail("推送失败", err)
		}
		return SyncResult{Outcome: OutcomePushed, Message: "同步完成"}

	case gitop.RelRemoteAhead, gitop.RelDiverged:
		// 远程有本地没有的提交：远程领先（快进）或真正分叉（重放），均用 rebase 合并
		s.logger.Warn("远程有新提交，尝试 rebase 合并...")
		if err := s.git.PullRebase(s.cfg.Remote, s.cfg.Branch); err != nil {
			// 仅"真冲突"（rebase 已开始）才中止并按 conflict_strategy 处理；
			// 网络/钩子等其他失败直接报错——绝不 reset --hard 销毁本地已提交工作。
			rebase, rerr := s.git.RebaseInProgress()
			if rerr != nil {
				return s.fail("检查 rebase 状态失败", rerr)
			}
			if rebase {
				s.logger.Warn("Rebase 冲突，中止 rebase 并按策略处理...")
				if err := s.git.RebaseAbort(); err != nil {
					return s.fail("中止 rebase 失败", err)
				}
				return s.handleConflict()
			}
			return s.fail("rebase 合并失败", err)
		}
		if err := s.git.Push(s.cfg.Remote, s.cfg.Branch); err != nil {
			return s.fail("Rebase 后推送失败", err)
		}
		return SyncResult{Outcome: OutcomeAutoMerged, Message: "同步完成（自动合并成功）"}
	}

	return s.fail("未知的关系状态", fmt.Errorf("relation=%d", rel))
}

// fail 记录错误日志并返回失败的 SyncResult。
func (s *Syncer) fail(msg string, err error) SyncResult {
	s.logger.Error(msg + ": " + err.Error())
	return SyncResult{Outcome: OutcomeFailed, Message: msg, Err: err}
}

// handleConflict 按 conflict_strategy 处理 rebase 失败后的冲突。
func (s *Syncer) handleConflict() SyncResult {
	timestamp := time.Now().Format("20060102_150405")
	switch s.cfg.ConflictStrategy {
	case "local_wins":
		return s.conflictLocalWins(timestamp)
	case "remote_wins":
		return s.conflictRemoteWins()
	case "conflict_files":
		return s.conflictFiles(timestamp)
	default:
		return s.fail("未知冲突策略", fmt.Errorf("conflict_strategy=%s", s.cfg.ConflictStrategy))
	}
}

// conflictLocalWins 备份远程旧版本到分支，再用 --force-with-lease 强制推送本地。
func (s *Syncer) conflictLocalWins(timestamp string) SyncResult {
	backupName := fmt.Sprintf("backup/remote-%s", timestamp)
	s.logger.Info("备份远程到分支: " + backupName)
	if err := s.git.CreateBackupBranch(s.cfg.Remote, s.cfg.Branch, backupName); err != nil {
		return s.fail("创建备份分支失败", err)
	}
	if err := s.git.PushBranch(s.cfg.Remote, backupName); err != nil {
		return s.fail("推送备份分支失败", err)
	}
	s.logger.Info("强制推送本地（--force-with-lease）...")
	if err := s.git.PushForce(s.cfg.Remote, s.cfg.Branch); err != nil {
		return s.fail("强制推送失败", err)
	}
	s.cleanupBackups()
	return SyncResult{
		Outcome:      OutcomeConflictResolved,
		Message:      "冲突已解决：本地优先",
		Details:      fmt.Sprintf("远程旧版本已备份到分支 %s", backupName),
		BackupBranch: backupName,
	}
}

// conflictRemoteWins 放弃本地未推送改动，重置到远程版本。
func (s *Syncer) conflictRemoteWins() SyncResult {
	s.logger.Info("放弃本地变更，重置到远程版本...")
	if err := s.git.ResetHardToRemote(s.cfg.Remote, s.cfg.Branch); err != nil {
		return s.fail("重置到远程失败", err)
	}
	return SyncResult{
		Outcome: OutcomeConflictResolved,
		Message: "冲突已解决：远程优先",
		Details: "本地未推送改动已被远程版本覆盖",
	}
}

// conflictFiles 冲突时本地版落 .sync-conflict-<ts>.<ext> 副本，远程版生效，副本入 git 推送。
// 解决 reset 删副本陷阱：先读本地内容到内存 → ResetHardToRemote → 再写副本 → add+commit+push。
// 副本在 clean -fd 执行时不存在于工作区，故不被清除（根因解法，非 work-around）。
func (s *Syncer) conflictFiles(timestamp string) SyncResult {
	// 列差异文件（Modified + Deleted：本地有内容且与远程不同）
	files, err := s.git.DiffNameOnly(s.cfg.Remote, s.cfg.Branch)
	if err != nil {
		return s.fail("列差异文件失败", err)
	}

	// 读本地版内容到内存（RebaseAbort 后工作区 = 本地 HEAD）
	type localCopy struct {
		relPath string
		content []byte
	}
	var copies []localCopy
	for _, rel := range files {
		full := filepath.Join(s.cfg.RepoDir, rel)
		info, err := os.Stat(full)
		if err != nil {
			s.logger.Warnf("读取本地文件 %s 失败，跳过: %v", rel, err)
			continue
		}
		if info.Size() > maxConflictCopySize {
			s.logger.Warnf("本地文件 %s 超上限（%d 字节），跳过保留为副本", rel, info.Size())
			continue
		}
		data, err := os.ReadFile(full)
		if err != nil {
			s.logger.Warnf("读取本地文件 %s 失败，跳过: %v", rel, err)
			continue
		}
		copies = append(copies, localCopy{relPath: rel, content: data})
	}

	// 重置到远程（reset --hard + clean -fd）——副本尚未写入，clean 无关
	s.logger.Info("重置到远程版本（本地版将以副本保留）...")
	if err := s.git.ResetHardToRemote(s.cfg.Remote, s.cfg.Branch); err != nil {
		return s.fail("重置到远程失败", err)
	}

	// 无本地内容可保留：远程已生效，无需提交推送
	if len(copies) == 0 {
		return SyncResult{
			Outcome: OutcomeConflictResolved,
			Message: "冲突已解决：远程优先（无本地文件需保留为副本）",
		}
	}

	// 从内存写副本文件（不受 reset 影响）
	for _, c := range copies {
		copyPath := filepath.Join(s.cfg.RepoDir, conflictCopyName(c.relPath, timestamp))
		if err := os.MkdirAll(filepath.Dir(copyPath), 0o755); err != nil {
			return s.fail("创建副本目录失败", err)
		}
		if err := os.WriteFile(copyPath, c.content, 0o644); err != nil {
			return s.fail("写冲突副本失败", err)
		}
	}

	// 副本入 git 提交推送（fast-forward，远程为父提交）
	if err := s.git.StageAll(); err != nil {
		return s.fail("暂存副本失败", err)
	}
	if err := s.git.Commit(fmt.Sprintf("conflict: preserve local as sync-conflict copies (%s)", timestamp)); err != nil {
		return s.fail("提交副本失败", err)
	}
	if err := s.git.Push(s.cfg.Remote, s.cfg.Branch); err != nil {
		return s.fail("推送副本失败", err)
	}
	return SyncResult{
		Outcome: OutcomeConflictResolved,
		Message: "冲突已解决：本地版保留为副本，远程版生效",
		Details: fmt.Sprintf("已保留 %d 个本地版本副本", len(copies)),
	}
}

// maxConflictCopySize 是 conflict_files 单文件保留为副本的上限（字节）。
// 超出上限的文件跳过保留——整文件载入内存，超大文件会 OOM。
const maxConflictCopySize = 512 << 20

// conflictCopyName 将 path 转为冲突副本名：file.ext → file.sync-conflict-<timestamp>.ext
// 点文件（如 .gitignore）整体作名：.gitignore → .gitignore.sync-conflict-<timestamp>
func conflictCopyName(path, timestamp string) string {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)
	if name == "" { // 点文件：filepath.Ext(".gitignore")=".gitignore"，name 为空
		name = base
		ext = ""
	}
	copyBase := name + ".sync-conflict-" + timestamp + ext
	if dir == "." || dir == "" {
		return copyBase
	}
	return filepath.Join(dir, copyBase)
}

// cleanupBackups 清理 backup/remote-* 备份分支，保留最新 backup_keep 个（本地+远程）。
// 按分支名内时间戳降序排序，超出保留数的旧分支从本地与远程删除。
func (s *Syncer) cleanupBackups() {
	branches, err := s.git.ListBackupBranches(s.cfg.Remote)
	if err != nil {
		s.logger.Warn("列出备份分支失败: " + err.Error())
		return
	}
	if len(branches) <= *s.cfg.BackupKeep {
		return
	}
	sort.Sort(sort.Reverse(sort.StringSlice(branches)))
	for _, b := range branches[*s.cfg.BackupKeep:] {
		s.logger.Info("清理旧备份分支: " + b)
		if err := s.git.DeleteLocalBranch(b); err != nil {
			s.logger.Warn("删除本地分支 " + b + " 失败: " + err.Error())
		}
		if err := s.git.DeleteRemoteBranch(s.cfg.Remote, b); err != nil {
			s.logger.Warn("删除远程分支 " + b + " 失败: " + err.Error())
		}
	}
}

// DryRunPlan 是 dry-run 的只读分析结果。
type DryRunPlan struct {
	Steps []string // 计划步骤
}

// DryRun 执行只读分析，输出同步计划而不改动仓库。
// 跳过 fetch 与所有写操作，基于本地状态与已有远程引用判定关系。
// 无法预判 rebase 是否冲突（需实际写操作），仅提示将使用的冲突策略。
// TODO: 可选 fetch 以预判远程领先（当前基于陈旧远程引用，可能误报 UpToDate）
func (s *Syncer) DryRun() DryRunPlan {
	var steps []string
	if !s.git.IsRepo() {
		steps = append(steps, "首次运行：将初始化仓库（git init -b + remote add + 首次提交 + push -u）")
		return DryRunPlan{Steps: steps}
	}
	// C1+C4：配置 ↔ 仓库一致性只读核对（与真实运行一致）
	remoteURL, err := s.git.GetRemoteURL(s.cfg.Remote)
	if err != nil {
		return DryRunPlan{Steps: []string{"远程 " + s.cfg.Remote + " 不存在（配置错误）"}}
	}
	if gitop.NormalizeRemoteURL(remoteURL) != gitop.NormalizeRemoteURL(s.cfg.RemoteURL) {
		return DryRunPlan{Steps: []string{"远程地址与配置不一致：仓库=" + remoteURL + " 配置=" + s.cfg.RemoteURL}}
	}
	curBranch, err := s.git.CurrentBranch()
	if err != nil {
		return DryRunPlan{Steps: []string{"获取当前分支失败: " + err.Error()}}
	}
	if curBranch != s.cfg.Branch {
		return DryRunPlan{Steps: []string{"当前分支与配置不一致：本地=" + curBranch + " 配置=" + s.cfg.Branch}}
	}
	hasHead, err := s.git.HasHead()
	if err != nil {
		return DryRunPlan{Steps: []string{"检查 HEAD 失败: " + err.Error()}}
	}
	if !hasHead {
		steps = append(steps, "仓库无提交：将拉取远程分支对齐，若远程无该分支则完成首次推送")
		return DryRunPlan{Steps: steps}
	}
	hasChanges, err := s.git.HasChanges()
	if err != nil {
		return DryRunPlan{Steps: []string{"检查变更失败: " + err.Error()}}
	}
	if hasChanges {
		steps = append(steps, "将提交本地变更（git add -A + commit）")
	} else {
		steps = append(steps, "本地无新变更")
	}
	// 跳过 fetch（dry-run 不联网），基于已有远程引用判定
	exists, err := s.git.RemoteBranchExists(s.cfg.Remote, s.cfg.Branch)
	if err != nil {
		return DryRunPlan{Steps: append(steps, "检查远程分支失败: "+err.Error())}
	}
	if !exists {
		steps = append(steps, fmt.Sprintf("远程分支 %s/%s 不存在：将直接 push（新建远程分支）", s.cfg.Remote, s.cfg.Branch))
		return DryRunPlan{Steps: steps}
	}
	rel, err := s.git.RelationTo(s.cfg.Remote, s.cfg.Branch)
	if err != nil {
		return DryRunPlan{Steps: append(steps, "检查关系失败: "+err.Error())}
	}
	switch rel {
	case gitop.RelUpToDate:
		steps = append(steps, "本地与远程一致（UpToDate）：无需推送")
	case gitop.RelLocalAhead:
		steps = append(steps, "本地领先（LocalAhead）：将 push（快进）")
	case gitop.RelRemoteAhead, gitop.RelDiverged:
		steps = append(steps, fmt.Sprintf("远程有新提交（%s）：将 pull --rebase；若冲突，按 %s 策略处理", rel, s.cfg.ConflictStrategy))
	}
	return DryRunPlan{Steps: steps}
}

// formatCommitMsg 渲染提交消息模板，当前支持 {{.Timestamp}} 占位符。
// TODO: 支持完整 Go 模板语法与更多变量（变更文件数等）
func formatCommitMsg(format string) string {
	ts := time.Now().Format("2006-01-02 15:04:05")
	return strings.ReplaceAll(format, "{{.Timestamp}}", ts)
}
