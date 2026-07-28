// NotificationCenter 经 UNUserNotificationCenter 投递系统通知（引擎经 IPC notify 事件委托）。
// 授权在首次需要通知时请求（非启动时），符合 HIG。
import UserNotifications

final class NotificationManager {
    static let shared = NotificationManager()
    private var authorized = false

    private init() {}

    /// 首次发通知时请求授权（alert + sound）。
    func requestAuthorizationIfNeeded() async {
        guard !authorized else { return }
        let center = UNUserNotificationCenter.current()
        let granted = (try? await center.requestAuthorization(options: [.alert, .sound, .badge])) ?? false
        authorized = granted
    }

    /// 投递一条系统通知，severity 映射中断级别（error 为 timeSensitive）。
    func send(title: String, body: String, severity: String) {
        Task {
            await requestAuthorizationIfNeeded()
            let content = UNMutableNotificationContent()
            content.title = title
            content.body = body
            content.sound = .default
            if severity == "error" {
                content.interruptionLevel = .timeSensitive
            }
            let req = UNNotificationRequest(identifier: UUID().uuidString, content: content, trigger: nil)
            try? await UNUserNotificationCenter.current().add(req)
        }
    }
}
