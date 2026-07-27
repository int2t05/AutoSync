// notify_test.go 验证通知策略 PolicyFor：各 Outcome → 是否通知/级别（纯逻辑，无 mock）。
package tests

import (
	"strings"
	"testing"

	"autosync/internal/notify"
	"autosync/internal/sync"
)

// TestPolicyFor_SuccessSilent 验证成功类结果静默不通知（无感核心）。
func TestPolicyFor_SuccessSilent(t *testing.T) {
	for _, oc := range []sync.Outcome{sync.OutcomeNoChanges, sync.OutcomePushed, sync.OutcomeAutoMerged} {
		d := notify.PolicyFor(sync.SyncResult{Outcome: oc, Message: "ok"})
		if d.Notify {
			t.Errorf("Outcome %s 应静默（不通知）", oc)
		}
	}
}

// TestPolicyFor_InitDoneInfo 验证首次初始化发 Info 通知。
func TestPolicyFor_InitDoneInfo(t *testing.T) {
	d := notify.PolicyFor(sync.SyncResult{Outcome: sync.OutcomeInitDone, Message: "初始化完成"})
	if !d.Notify || d.Severity != notify.SeverityInfo {
		t.Errorf("InitDone 应 Info 通知，got notify=%v sev=%v", d.Notify, d.Severity)
	}
}

// TestPolicyFor_ConflictResolvedWarning 验证冲突解决发 Warning 通知，含备份分支名。
func TestPolicyFor_ConflictResolvedWarning(t *testing.T) {
	d := notify.PolicyFor(sync.SyncResult{
		Outcome:      sync.OutcomeConflictResolved,
		Message:      "本地优先",
		BackupBranch: "backup/remote-20260727_143000",
	})
	if !d.Notify || d.Severity != notify.SeverityWarning {
		t.Errorf("ConflictResolved 应 Warning 通知，got notify=%v sev=%v", d.Notify, d.Severity)
	}
	if !strings.Contains(d.Body, "backup/remote-20260727_143000") {
		t.Errorf("通知正文应含备份分支名: %q", d.Body)
	}
}

// TestPolicyFor_FailedError 验证失败/中止发 Error 通知。
func TestPolicyFor_FailedError(t *testing.T) {
	for _, oc := range []sync.Outcome{sync.OutcomeConflictAborted, sync.OutcomeFailed} {
		d := notify.PolicyFor(sync.SyncResult{Outcome: oc, Message: "出错"})
		if !d.Notify || d.Severity != notify.SeverityError {
			t.Errorf("Outcome %s 应 Error 通知，got notify=%v sev=%v", oc, d.Notify, d.Severity)
		}
	}
}
