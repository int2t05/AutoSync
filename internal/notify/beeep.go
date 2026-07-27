// beeep.go 用 gen2brain/beeep 实现 Notifier，跨平台系统通知。
// Windows 用 toast、macOS 用 osascript、Linux 用 notify-send。
// 投递为平台副作用，不单测；通知策略（PolicyFor）已单测覆盖。
package notify

import "github.com/gen2brain/beeep"

// beeepNotifier 通过 beeep 库投递系统通知。
type beeepNotifier struct{}

// NewBeeepNotifier 创建基于 beeep 的通知器。
func NewBeeepNotifier() Notifier {
	return &beeepNotifier{}
}

// Notify 投递一条系统通知。severity 当前不影响 beeep 投递（标题已含语义），保留供未来区分图标。
func (n *beeepNotifier) Notify(title, body string, severity Severity) error {
	return beeep.Notify(title, body, "")
}
