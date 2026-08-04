// state.go 持久化上次同步状态，供 status 命令读取。
// 存储为 JSON 文件，位于 ~/.autosync/state/ 子目录下（见 config.StateFilePath），并发安全。
package state

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// State 记录上次同步的结果摘要。
type State struct {
	LastSyncAt   time.Time `json:"last_sync_at"`            // 上次同步时间
	LastOutcome  string    `json:"last_outcome"`            // 上次结果标签（Outcome.String()）
	LastMessage  string    `json:"last_message"`            // 摘要
	BackupBranch string    `json:"backup_branch,omitempty"` // local_wins 时的备份分支名
}

// Store 负责状态的读写，互斥锁保护并发安全。
type Store struct {
	path string
	mu   sync.Mutex
}

// New 创建状态存储器。
func New(path string) *Store {
	return &Store{path: path}
}

// Load 读取状态；文件不存在返回零 State（首次运行场景，不报错）。
func (s *Store) Load() (*State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := &State{}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return st, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, st); err != nil {
		return nil, err
	}
	return st, nil
}

// Save 写入状态（覆盖）。
func (s *Store) Save(st State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0644)
}
