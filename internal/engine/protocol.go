// protocol.go 定义 engine 子命令的 IPC 消息结构（壳↔引擎，JSON 行协议）。
// 命令（壳→引擎，stdin）与事件（引擎→壳，stdout）均每行一个 JSON 对象。
// 字段命名统一 snake_case，与 configstore.Task 的 yaml tag 风格一致，便于 Swift Codable 映射。
package engine

// Command 是壳→引擎的 IPC 命令。
type Command struct {
	ID    int      `json:"id,omitempty"`    // 命令 ID，有则响应带同 id；quit 无 id
	Cmd   string   `json:"cmd"`             // status/sync-now/pause/resume/config-list/config-save/quit
	Task  string   `json:"task,omitempty"`  // 任务名（sync-now/pause/resume）
	Tasks []*TaskDTO `json:"tasks,omitempty"` // config-save 的任务列表
}

// TaskDTO 是 IPC 任务投影（与 configstore.Task 字段一一对应，snake_case JSON）。
// engine 收 config-save 后转 configstore.Task 调 Store.ReplaceAll。
type TaskDTO struct {
	Name             string   `json:"name"`
	RepoDir          string   `json:"repo_dir"`
	RemoteURL        string   `json:"remote_url"`
	Remote           string   `json:"remote,omitempty"`
	Branch           string   `json:"branch,omitempty"`
	Interval         string   `json:"interval,omitempty"`
	ConflictStrategy string   `json:"conflict_strategy,omitempty"`
	BackupKeep       int      `json:"backup_keep,omitempty"`
	RetryCount       int      `json:"retry_count,omitempty"`
	RetryBaseDelay   string   `json:"retry_base_delay,omitempty"`
	CommitMsgFormat  string   `json:"commit_msg_format,omitempty"`
	ShowConsole      bool     `json:"show_console,omitempty"`
	Ignore           []string `json:"ignore,omitempty"`
}

// Event 是引擎→壳的 IPC 事件。
type Event struct {
	ID           int          `json:"id,omitempty"`           // 对应命令 ID（若有）
	Event        string       `json:"event"`                  // ready/status/sync-result/paused/resumed/config-list/config-saved/notify/bye/error
	Version      string       `json:"version,omitempty"`      // ready
	LogPath      string       `json:"logPath,omitempty"`      // ready
	DataDir      string       `json:"dataDir,omitempty"`      // ready
	Tasks        []TaskStatus `json:"tasks,omitempty"`        // ready/status/config-list/config-saved
	Task         string       `json:"task,omitempty"`         // sync-result/paused/resumed
	Outcome      string       `json:"outcome,omitempty"`      // sync-result
	Message      string       `json:"message,omitempty"`      // sync-result/error
	BackupBranch string       `json:"backupBranch,omitempty"` // sync-result
	At           string       `json:"at,omitempty"`           // sync-result 时间
	Severity     string       `json:"severity,omitempty"`     // notify: info/warn/error
	Title        string       `json:"title,omitempty"`        // notify
	Body         string       `json:"body,omitempty"`         // notify
	Reason       string       `json:"reason,omitempty"`       // bye
}

// TaskStatus 是任务状态投影（ready/status 事件），含运行态与上次同步结果。
type TaskStatus struct {
	Name        string `json:"name"`
	RepoDir     string `json:"repo_dir"`
	Interval    string `json:"interval"`
	Paused      bool   `json:"paused"`
	LastSyncAt  string `json:"last_sync_at,omitempty"`
	LastOutcome string `json:"last_outcome,omitempty"`
	LastMessage string `json:"last_message,omitempty"`
}

// severityString 把 notify.Severity 映射为 IPC notify 事件的 severity 字符串。
func severityString(sev int) string {
	switch sev {
	case 1:
		return "warn"
	case 2:
		return "error"
	default:
		return "info"
	}
}
