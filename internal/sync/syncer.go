// syncer.go 实现同步状态机主路径：init → commit → fetch → 关系判定 → push/rebase。
// P2 覆盖无冲突路径；rebase 冲突时中止并返回 Failed，冲突解决策略在 P3 接入。
package sync

import (
	"fmt"
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
	s.logger.Info(fmt.Sprintf("目录=%s 策略=%s", s.cfg.RepoDir, s.cfg.ConflictStrategy))

	// S1：首次运行初始化
	if !s.git.IsRepo() {
		s.logger.Info("首次运行，初始化仓库...")
		if err := s.git.Init(s.cfg.Remote, s.cfg.RemoteURL, s.cfg.Branch); err != nil {
			return s.fail("初始化失败", err)
		}
		return SyncResult{Outcome: OutcomeInitDone, Message: "初始化完成，已推送首次同步"}
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

	// S3：拉取远程引用
	if err := s.git.Fetch(s.cfg.Remote); err != nil {
		return s.fail("拉取远程信息失败（网络问题？）", err)
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
			// rebase 冲突：中止 rebase，按 conflict_strategy 处理
			s.logger.Warn("Rebase 冲突，中止 rebase 并按策略处理...")
			s.git.RebaseAbort()
			return s.handleConflict()
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
	case "abort":
		s.logger.Warn("冲突策略 abort：不做变更，提示手动处理")
		return SyncResult{
			Outcome:  OutcomeConflictAborted,
			Message:  "检测到冲突，已中止同步",
			Details:  "请手动 git pull --rebase 处理后重试",
		}
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

// cleanupBackups 清理 backup/remote-* 备份分支，保留最新 backup_keep 个（本地+远程）。
// 按分支名内时间戳降序排序，超出保留数的旧分支从本地与远程删除。
func (s *Syncer) cleanupBackups() {
	branches, err := s.git.ListBackupBranches(s.cfg.Remote)
	if err != nil {
		s.logger.Warn("列出备份分支失败: " + err.Error())
		return
	}
	if len(branches) <= s.cfg.BackupKeep {
		return
	}
	sort.Sort(sort.Reverse(sort.StringSlice(branches)))
	for _, b := range branches[s.cfg.BackupKeep:] {
		s.logger.Info("清理旧备份分支: " + b)
		if err := s.git.DeleteLocalBranch(b); err != nil {
			s.logger.Warn("删除本地分支 " + b + " 失败: " + err.Error())
		}
		if err := s.git.DeleteRemoteBranch(s.cfg.Remote, b); err != nil {
			s.logger.Warn("删除远程分支 " + b + " 失败: " + err.Error())
		}
	}
}

// formatCommitMsg 渲染提交消息模板，当前支持 {{.Timestamp}} 占位符。
// TODO: 支持完整 Go 模板语法与更多变量（变更文件数等）
func formatCommitMsg(format string) string {
	ts := time.Now().Format("2006-01-02 15:04:05")
	return strings.ReplaceAll(format, "{{.Timestamp}}", ts)
}
