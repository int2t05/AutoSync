// MenuView 菜单栏 extra 的菜单内容：任务手动同步/暂停 + 引擎状态 + 开机自启 + 配置 + 退出。
import SwiftUI

struct MenuView: View {
    @ObservedObject var engine: EngineManager
    @StateObject private var autostart = AutostartManager()
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        VStack {
            if engine.state == .offline || engine.state == .error {
                HStack {
                    Image(systemName: "exclamationmark.triangle.fill").foregroundColor(.red)
                    Text("引擎离线")
                }
                Button("重启引擎") { engine.start() }
                Divider()
            } else if engine.state == .launching {
                Text("启动中…").foregroundColor(.secondary)
                Divider()
            }
            ForEach(engine.tasks) { task in
                TaskMenuItem(task: task, lastOutcome: engine.lastResults[task.name] ?? task.lastOutcome, engine: engine)
            }
            if !engine.tasks.isEmpty { Divider() }
            Toggle("开机自启", isOn: Binding(get: { autostart.isRegistered }, set: { autostart.set($0) }))
            Button("配置…") { openWindow(id: "config") }
            Divider()
            Button("退出 AutoSync") {
                engine.shutdown()
                NSApp.terminate(nil)
            }
        }
    }
}

/// 单个任务的子菜单：手动同步 / 暂停-恢复 + 上次结果。
struct TaskMenuItem: View {
    let task: TaskStatus
    let lastOutcome: String?
    @ObservedObject var engine: EngineManager

    var body: some View {
        Menu(task.name) {
            Button("手动同步") { engine.send(EngineCommand(cmd: "sync-now", task: task.name)) }
            Button(task.paused ? "恢复" : "暂停") {
                engine.send(EngineCommand(cmd: task.paused ? "resume" : "pause", task: task.name))
            }
            if let outcome = lastOutcome {
                Divider()
                Text("上次：\(outcome)").font(.caption)
            }
        }
    }
}
