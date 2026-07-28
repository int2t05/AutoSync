// Models.swift 定义与 Go internal/engine/protocol.go 一一对应的 Codable 结构。
// EngineCommand/EngineEvent 字段为 camelCase（与 Go json tag 一致）；
// TaskStatus/TaskDTO 为 snake_case（用 CodingKeys 映射）。
import Foundation

/// 壳→引擎命令（JSON 行）。
struct EngineCommand: Codable {
    var id: Int?
    var cmd: String
    var task: String?
    var tasks: [TaskDTO]?
}

/// 引擎→壳事件（JSON 行）。
struct EngineEvent: Codable {
    var id: Int?
    var event: String
    var version: String?
    var logPath: String?
    var dataDir: String?
    var tasks: [TaskStatus]?
    var configTasks: [TaskDTO]?
    var task: String?
    var outcome: String?
    var message: String?
    var backupBranch: String?
    var at: String?
    var severity: String?
    var title: String?
    var body: String?
    var reason: String?

    enum CodingKeys: String, CodingKey {
        case id, event, version, logPath, dataDir, tasks
        case configTasks = "config_tasks"
        case task, outcome, message, backupBranch, at, severity, title, body, reason
    }
}

/// 任务运行态与上次同步结果（ready/status 事件）。
struct TaskStatus: Codable, Identifiable {
    var name: String
    var repoDir: String
    var interval: String
    var paused: Bool
    var lastSyncAt: String?
    var lastOutcome: String?
    var lastMessage: String?
    var id: String { name }

    enum CodingKeys: String, CodingKey {
        case name, repoDir = "repo_dir", interval, paused
        case lastSyncAt = "last_sync_at", lastOutcome = "last_outcome", lastMessage = "last_message"
    }
}

/// IPC 任务完整配置投影（config-list/config-save）。
struct TaskDTO: Codable, Identifiable {
    var name: String
    var repoDir: String
    var remoteURL: String
    var remote: String?
    var branch: String?
    var interval: String?
    var conflictStrategy: String?
    var backupKeep: Int?
    var retryCount: Int?
    var retryBaseDelay: String?
    var commitMsgFormat: String?
    var showConsole: Bool?
    var ignore: [String]?
    var id: String { name }

    enum CodingKeys: String, CodingKey {
        case name, repoDir = "repo_dir", remoteURL = "remote_url", remote, branch, interval
        case conflictStrategy = "conflict_strategy", backupKeep = "backup_keep", retryCount = "retry_count"
        case retryBaseDelay = "retry_base_delay", commitMsgFormat = "commit_msg_format"
        case showConsole = "show_console", ignore
    }
}
