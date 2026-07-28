// EngineManager 管理引擎子进程生命周期与 IPC（ObservableObject，兼容 macOS 13）。
// 用 Process 拉起 autosync-engine，Pipe 接 stdin/stdout，后台队列按行解码 JSON 事件，
// 主线程更新 @Published 供 SwiftUI 刷新。terminationHandler 在引擎崩溃时指数退避重启（最多 3 次）。
import Foundation
import SwiftUI

final class EngineManager: ObservableObject {
    enum EngineState { case launching, ready, offline, error }

    @Published var state: EngineState = .launching
    @Published var tasks: [TaskStatus] = []
    @Published var configTasks: [TaskDTO] = []
    @Published var lastResults: [String: String] = [:]
    @Published var logPath: String = ""

    private var process: Process?
    private var stdin: FileHandle?
    private var stdoutPipe: Pipe?
    private let ioQueue = DispatchQueue(label: "autosync.engine.io")
    private var lineBuffer = Data()
    private let decoder = JSONDecoder()
    private let encoder = JSONEncoder()
    private var restartAttempts = 0
    private let configPath: String

    init(configPath: String) {
        self.configPath = configPath
    }

    /// 启动引擎子进程并进入读循环。
    func start() {
        guard let engineURL = resolveEngineURL() else { state = .error; return }
        let p = Process()
        p.executableURL = engineURL
        p.arguments = ["engine", "--config", configPath]
        let pipe = Pipe()
        p.standardOutput = pipe
        p.standardError = pipe // 引擎 stderr 合并到 stdout（仅 JSON，错误经日志文件诊断）
        var env = ProcessInfo.processInfo.environment
        // launchd 启动的 GUI app PATH 可能缺 git，补常见路径
        env["PATH"] = (env["PATH"] ?? "") + ":/usr/bin:/bin:/usr/local/bin:/opt/homebrew/bin"
        p.environment = env
        do {
            try p.run()
        } catch {
            state = .error
            return
        }
        p.terminationHandler = { [weak self] _ in
            DispatchQueue.main.async { self?.handleTermination() }
        }
        process = p
        stdin = (p.standardInput as? Pipe)?.fileHandleForWriting
        stdoutPipe = pipe
        state = .launching
        readLoop()
    }

    private func handleTermination() {
        guard state != .offline else { return } // 主动退出不重启
        state = .offline
        if restartAttempts < 3 {
            restartAttempts += 1
            let delay = pow(2.0, Double(restartAttempts)) // 2/4/8 秒指数退避
            DispatchQueue.main.asyncAfter(deadline: .now() + delay) { [weak self] in self?.start() }
        }
    }

    /// 主动关闭：发 quit，1s 后强制 terminate。
    func shutdown() {
        state = .offline // 阻止 terminationHandler 重启
        send(EngineCommand(cmd: "quit"))
        ioQueue.asyncAfter(deadline: .now() + 1) { [weak self] in self?.process?.terminate() }
    }

    /// 发送一条 JSON 命令到引擎 stdin（追加换行）。
    func send(_ command: EngineCommand) {
        guard let data = try? encoder.encode(command) else { return }
        stdin?.write(data)
        stdin?.write(Data([0x0A]))
    }

    private func readLoop() {
        guard let pipe = stdoutPipe else { return }
        pipe.fileHandleForReading.readabilityHandler = { [weak self] handle in
            guard let self else { return }
            let data = handle.availableData
            guard !data.isEmpty else {
                handle.readabilityHandler = nil
                return
            }
            self.ioQueue.async {
                self.lineBuffer.append(data)
                self.drainLines()
            }
        }
    }

    /// 在 ioQueue 按行切分缓冲区并解码事件，派发到主线程。
    private func drainLines() {
        while let nl = lineBuffer.firstIndex(of: 0x0A) {
            let lineData = lineBuffer.subdata(in: 0..<nl)
            lineBuffer.removeSubrange(0...nl)
            guard !lineData.isEmpty,
                  let ev = try? decoder.decode(EngineEvent.self, from: lineData) else { continue }
            DispatchQueue.main.async { self.dispatch(ev) }
        }
    }

    private func dispatch(_ ev: EngineEvent) {
        switch ev.event {
        case "ready":
            state = .ready
            logPath = ev.logPath ?? ""
            tasks = ev.tasks ?? []
        case "status":
            tasks = ev.tasks ?? []
        case "config-list", "config-saved":
            configTasks = ev.configTasks ?? []
            tasks = ev.tasks ?? []
        case "sync-result":
            if let task = ev.task, let outcome = ev.outcome {
                lastResults[task] = outcome
            }
        case "notify":
            if let title = ev.title, let body = ev.body {
                NotificationManager.shared.send(title: title, body: body, severity: ev.severity ?? "info")
            }
        case "bye":
            state = .offline
        default:
            break
        }
    }

    /// 定位引擎二进制：.app bundle 内优先，开发期 fallback Bundle 资源。
    private func resolveEngineURL() -> URL? {
        let bundleEngine = Bundle.main.bundleURL.appendingPathComponent("Contents/MacOS/autosync-engine")
        if FileManager.default.isExecutableFile(atPath: bundleEngine.path) {
            return bundleEngine
        }
        return Bundle.main.url(forResource: "autosync-engine", withExtension: nil)
    }
}
