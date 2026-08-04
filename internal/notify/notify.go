// notify.go 定义系统通知抽象与通知策略。
// PolicyFor 是纯逻辑（Outcome → 是否通知/级别），可单测；具体投递由 beeep 实现。
package notify

import (
	"autosync/internal/sync"
)

// Severity 通知级别。
type Severity int

const (
	SeverityInfo    Severity = iota // 信息
	SeverityWarning                 // 警告
	SeverityError                   // 错误
)

// String 返回 IPC notify 事件用的 severity 字符串（info/warn/error）。
func (s Severity) String() string {
	switch s {
	case SeverityWarning:
		return "warn"
	case SeverityError:
		return "error"
	default:
		return "info"
	}
}

// Decision 是通知策略的决策结果。
type Decision struct {
	Notify   bool     // 是否发送通知
	Severity Severity // 通知级别
	Title    string   // 通知标题
	Body     string   // 通知正文
}

// Notifier 是系统通知投递抽象。
type Notifier interface {
	Notify(title, body string, severity Severity) error
}

// PolicyFor 根据同步结果决定通知策略（纯逻辑，可测）。
// 成功类（NoChanges/Pushed/AutoMerged）静默；InitDone 信息；ConflictResolved 警告；Failed 错误。
func PolicyFor(result sync.SyncResult) Decision {
	switch result.Outcome {
	case sync.OutcomeInitDone:
		return Decision{Notify: true, Severity: SeverityInfo, Title: "AutoSync 初始化完成", Body: result.Message}
	case sync.OutcomeNoChanges, sync.OutcomePushed, sync.OutcomeAutoMerged:
		// 日常成功静默，仅写日志（无感核心）
		return Decision{Notify: false}
	case sync.OutcomeConflictResolved:
		body := result.Message
		if result.BackupBranch != "" {
			body += "\n备份分支: " + result.BackupBranch
		}
		return Decision{Notify: true, Severity: SeverityWarning, Title: "AutoSync 冲突已自动处理", Body: body}
	case sync.OutcomeFailed:
		return Decision{Notify: true, Severity: SeverityError, Title: "AutoSync 同步失败", Body: result.Message}
	}
	return Decision{Notify: false}
}
