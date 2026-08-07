// state.go 持久化上次同步状态，供 status 命令读取。
// 存储为 JSON 文件，位于 ~/.autosync/state/ 子目录下（见 config.StateFilePath），并发安全。
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// State 记录上次同步的结果摘要与任务运行标志。
type State struct {
	LastSyncAt   time.Time `json:"last_sync_at"`            // 上次同步时间
	LastOutcome  string    `json:"last_outcome"`            // 上次结果标签（Outcome.String()）
	LastMessage  string    `json:"last_message"`            // 摘要
	BackupBranch string    `json:"backup_branch,omitempty"` // local_wins 时的备份分支名
	Paused       bool      `json:"paused,omitempty"`        // 任务暂停标志（热重载/重启后保持）
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
	return s.loadLocked()
}

// loadLocked 读取状态（须持 s.mu）；文件不存在返回零 State。
func (s *Store) loadLocked() (*State, error) {
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
	return s.saveLocked(st)
}

// saveLocked 序列化并原子写入状态（临时文件 + rename，防崩溃写坏半份 JSON，须持 s.mu）。
func (s *Store) saveLocked(st State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".autosync.state-*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("写入状态失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("关闭临时文件失败: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("替换状态文件失败: %w", err)
	}
	return nil
}

// Update 在锁内读-改-写：读取现有状态，回调 mod 就地修改后保存。
// 两路写入者（同步结果 / 暂停标志）共用，互不覆盖对方字段。
func (s *Store) Update(mod func(*State)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return err
	}
	mod(st)
	return s.saveLocked(*st)
}
