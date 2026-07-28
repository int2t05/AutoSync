// Autostart 经 SMAppService 注册/取消登录项（macOS 13+），用户主动切换，不默认开启。
import ServiceManagement

@MainActor
final class AutostartManager: ObservableObject {
    @Published private(set) var isRegistered: Bool = false

    init() { refresh() }

    /// 同步当前注册状态（系统设置「登录项」可见）。
    func refresh() {
        isRegistered = SMAppService.mainApp.status == .enabled
    }

    /// 开关注册：失败保持原状态（不抛错，UI 据状态刷新）。
    func set(_ on: Bool) {
        do {
            if on {
                try SMAppService.mainApp.register()
            } else {
                try SMAppService.mainApp.unregister()
            }
            refresh()
        } catch {
            refresh() // 失败回查真实状态
        }
    }
}
