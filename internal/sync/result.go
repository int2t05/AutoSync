// result.go 定义同步结果模型：Outcome 枚举与 SyncResult。
// Outcome 驱动通知策略与状态记录；SyncResult 携带供日志/通知/状态使用的摘要信息。
package sync

// Outcome 描述一次同步的最终结果。
type Outcome int

const (
	OutcomeInitDone          Outcome = iota // 首次初始化完成
	OutcomeNoChanges                        // 无变更且已是最新
	OutcomePushed                           // 直接推送成功（含新建远程分支）
	OutcomeAutoMerged                       // rebase 自动合并成功
	OutcomeConflictResolved                 // 冲突已按策略解决
	OutcomeFailed                           // 错误
)

// String 返回 Outcome 的中文标签，用于日志与状态展示。
func (o Outcome) String() string {
	switch o {
	case OutcomeInitDone:
		return "初始化完成"
	case OutcomeNoChanges:
		return "无变更"
	case OutcomePushed:
		return "已推送"
	case OutcomeAutoMerged:
		return "自动合并"
	case OutcomeConflictResolved:
		return "冲突已解决"
	case OutcomeFailed:
		return "失败"
	default:
		return "未知"
	}
}

// SyncResult 是单次同步的返回值。
type SyncResult struct {
	Outcome      Outcome // 结果枚举
	Message      string  // 摘要
	Details      string  // 细节（如备份分支名）
	BackupBranch string  // local_wins 时的备份分支名
	Err          error   // 失败时的底层错误
}
