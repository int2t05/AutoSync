// syncer.go 实现同步状态机主路径：init → commit → fetch → 关系判定 → push/rebase。
// P2 覆盖无冲突路径；rebase 冲突时中止并返回 Failed，冲突解决策略在 P3 接入。
package sync

import (
	"fmt"
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
			// P2：检测到冲突，中止 rebase 并返回失败；P3 接入冲突解决策略
			s.logger.Warn("Rebase 冲突，中止 rebase...")
			s.git.RebaseAbort()
			return s.fail("检测到冲突，解决策略待 P3 实现", err)
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

// formatCommitMsg 渲染提交消息模板，当前支持 {{.Timestamp}} 占位符。
// TODO: 支持完整 Go 模板语法与更多变量（变更文件数等）
func formatCommitMsg(format string) string {
	ts := time.Now().Format("2006-01-02 15:04:05")
	return strings.ReplaceAll(format, "{{.Timestamp}}", ts)
}
