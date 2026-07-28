// notifier_helper_test.go 提供 recordingNotifier：实现 notify.Notifier，记录调用以断言通知投递。
// 定位为副作用端口 sink（记录对外通知副作用），非业务逻辑 mock；git/sync/state 仍是真实数据。
package tests

import "autosync/internal/notify"

// recordedNotification 记录一次通知调用的参数。
type recordedNotification struct {
	Title, Body string
	Severity    notify.Severity
}

// recordingNotifier 记录所有 Notify 调用，供 tasksched/engine 测试断言通知是否触发。
type recordingNotifier struct {
	calls []recordedNotification
}

// Notify 记录调用，始终成功（通知投递副作用由测试断言 calls）。
func (n *recordingNotifier) Notify(title, body string, severity notify.Severity) error {
	n.calls = append(n.calls, recordedNotification{Title: title, Body: body, Severity: severity})
	return nil
}
