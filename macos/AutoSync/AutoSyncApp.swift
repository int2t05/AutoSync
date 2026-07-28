// AutoSyncApp macOS 菜单栏守护应用入口：MenuBarExtra + 配置窗口。
// LSUIElement=true 使其不显 Dock 图标；引擎经子进程 IPC 调用（EngineManager）。
import SwiftUI

@main
struct AutoSyncApp: App {
    @StateObject private var engine: EngineManager

    init() {
        // 单实例锁：已被持则退出（菜单栏已有首实例）
        if !SingleInstanceLock.acquire() {
            NSLog("AutoSync 已在运行，退出")
            exit(1)
        }
        let configPath = AutoSyncApp.resolveConfigPath()
        _engine = StateObject(wrappedValue: EngineManager(configPath: configPath))
    }

    var body: some Scene {
        MenuBarExtra("AutoSync", image: "MenubarIcon") {
            MenuView(engine: engine)
        }
        .menuBarExtraStyle(.menu)

        WindowGroup("AutoSync 配置", id: "config") {
            ConfigView(engine: engine)
        }
        .defaultSize(width: 560, height: 420)
    }

    /// 配置文件路径：~/Library/Application Support/AutoSync/autosync.conf.yaml（与 Go os.UserConfigDir 一致）。
    static func resolveConfigPath() -> String {
        let fm = FileManager.default
        let dir = fm.urls(for: .applicationSupportDirectory, in: .userDomainMask).first!
        let appDir = dir.appendingPathComponent("AutoSync", isDirectory: true)
        try? fm.createDirectory(at: appDir, withIntermediateDirectories: true)
        return appDir.appendingPathComponent("autosync.conf.yaml").path
    }
}
