// ConfigView 配置窗口：任务列表 CRUD，经 config-list/config-save 委托引擎（壳零 YAML 依赖）。
import SwiftUI
import AppKit

struct ConfigView: View {
    @ObservedObject var engine: EngineManager
    @State private var drafts: [TaskDTO] = []
    @State private var selected: String?
    @State private var editing: TaskDTO?
    @State private var editingNew = false

    var body: some View {
        VStack(spacing: 0) {
            List(selection: $selected) {
                ForEach(drafts) { task in
                    HStack {
                        Text(task.name).font(.body)
                        Spacer()
                        Text(task.interval ?? "").foregroundColor(.secondary).font(.caption)
                    }
                    .tag(task.name)
                }
            }
            Divider()
            HStack {
                Button("新增") {
                    editing = TaskDTO(name: "新任务", repoDir: "", remoteURL: "", branch: "main",
                                     interval: "1m", conflictStrategy: "local_wins")
                    editingNew = true
                }
                Button("编辑") {
                    guard let s = selected, let t = drafts.first(where: { $0.name == s }) else { return }
                    editing = t
                    editingNew = false
                }
                Button("删除") {
                    guard let s = selected else { return }
                    drafts.removeAll { $0.name == s }
                    selected = nil
                }
                Spacer()
                Button("保存") { engine.send(EngineCommand(id: 1, cmd: "config-save", tasks: drafts)) }
            }
            .padding(8)
        }
        .frame(width: 560, height: 420)
        .onAppear { engine.send(EngineCommand(id: 1, cmd: "config-list")) }
        .onReceive(engine.$configTasks) { configTasks in
            drafts = configTasks
        }
        .sheet(item: $editing) { draft in
            TaskEditView(draft: draft, isNew: editingNew) { saved in
                if editingNew {
                    if !drafts.contains(where: { $0.name == saved.name }) { drafts.append(saved) }
                } else {
                    if let i = drafts.firstIndex(where: { $0.name == draft.name }) { drafts[i] = saved }
                }
                editing = nil
            }
        }
    }
}

/// 任务新增/编辑表单（repoDir 用 NSOpenPanel 原生目录选择器，符合 HIG）。
struct TaskEditView: View {
    @State var draft: TaskDTO
    let isNew: Bool
    let onSave: (TaskDTO) -> Void
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(isNew ? "新增任务" : "编辑任务").font(.headline)
            Form {
                TextField("名称", text: $draft.name)
                HStack {
                    TextField("目录", text: $draft.repoDir)
                    Button("选择…") { chooseDir() }
                }
                TextField("远程地址", text: $draft.remoteURL)
                TextField("分支", text: $draft.branch.orDefault("main"))
                TextField("间隔", text: $draft.interval.orDefault("1m"))
                Picker("冲突策略", selection: $draft.conflictStrategy.orDefault("local_wins")) {
                    Text("local_wins").tag("local_wins")
                    Text("remote_wins").tag("remote_wins")
                    Text("abort").tag("abort")
                }
            }
            HStack {
                Spacer()
                Button("取消") { dismiss() }
                Button("确定") { onSave(draft); dismiss() }
            }
        }
        .padding()
        .frame(width: 460)
    }

    private func chooseDir() {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        if panel.runModal() == .OK, let url = panel.url {
            draft.repoDir = url.path
        }
    }
}

// 可选字符串绑定转非可选（nil 用默认值），供 TextField/Picker 等需 Binding<String> 的控件使用。
extension Binding where Value == String? {
    func orDefault(_ defaultValue: String) -> Binding<String> {
        Binding<String>(get: { wrappedValue ?? defaultValue }, set: { wrappedValue = $0 })
    }
}
