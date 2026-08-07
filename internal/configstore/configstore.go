// configstore.go 管理多任务同步配置（autosync.conf.yaml 的 tasks 列表）。
// Task 内嵌 Config 复用默认值与校验。
// 每任务按名解析独立的 state/lock 文件，互不干扰。
package configstore

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"

	"autosync/internal/config"
	"autosync/internal/lock"
	"gopkg.in/yaml.v3"
)

// Task 是一个同步任务：名称 + V1.0 同步配置（内嵌复用默认值与校验）。
type Task struct {
	Name string `yaml:"name"`
	config.Config `yaml:",inline"`
}

// Store 是多任务配置存储，对应 autosync.conf.yaml 的 tasks 列表。
type Store struct {
	path  string
	mu    sync.Mutex
	tasks []*Task
}

// taskFile 是顶层 YAML 结构。
type taskFile struct {
	Tasks []*Task `yaml:"tasks"`
}

// Load 从 path 读取多任务配置，对每个任务填充默认值并校验。
// 文件不存在或无任务时返回空存储：托盘以空配置启动，由配置窗口新增任务后 Save 落盘。
// 无 tasks 键的旧单配置视为名为 "default" 的单任务。
func Load(path string) (*Store, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Store{path: path}, nil // 无配置文件：空存储启动
		}
		return nil, fmt.Errorf("读取配置文件失败 %s: %w", path, err)
	}
	tasks, err := parseTasks(data)
	if err != nil {
		return nil, err
	}
	for _, t := range tasks {
		if t.Name == "" {
			return nil, fmt.Errorf("任务名不能为空")
		}
		if err := t.Normalize(); err != nil {
			return nil, fmt.Errorf("任务 %q 校验失败: %w", t.Name, err)
		}
	}
	if err := checkUniqueNames(tasks); err != nil {
		return nil, err
	}
	if err := checkUniqueRepoDirs(tasks); err != nil {
		return nil, err
	}
	return &Store{path: path, tasks: tasks}, nil
}

// NewStore 创建空存储（供新增任务后保存）。
func NewStore(path string) *Store {
	return &Store{path: path}
}

// parseTasks 解析 YAML 顶层 tasks 列表；空文件或 tasks:[] 返回空。
func parseTasks(data []byte) ([]*Task, error) {
	var tf taskFile
	if err := yaml.Unmarshal(data, &tf); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}
	return tf.Tasks, nil
}

// checkUniqueNames 校验任务名按 safeName 解析后唯一（"a b" 与 "a_b" 同键冲突，
// 否则二者共享 state/lock 文件互相串扰）。
func checkUniqueNames(tasks []*Task) error {
	seen := make(map[string]string) // safeName -> 原任务名
	for _, t := range tasks {
		key := safeName(t.Name)
		if owner, dup := seen[key]; dup {
			return fmt.Errorf("任务名冲突: %q 与 %q 均解析为 %q", owner, t.Name, key)
		}
		seen[key] = t.Name
	}
	return nil
}

// checkUniqueRepoDirs 校验任务 repo_dir 跨任务唯一，防止多任务并发读写同一仓库互相破坏。
// 键为 filepath.Clean 归一化路径；Windows 文件系统大小写不敏感，统一小写比较，其他平台保持大小写敏感。
func checkUniqueRepoDirs(tasks []*Task) error {
	seen := make(map[string]string) // 归一化 repo_dir -> 任务名
	for _, t := range tasks {
		key := filepath.Clean(t.RepoDir)
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if owner, dup := seen[key]; dup {
			return fmt.Errorf("任务 %q 与 %q 的 repo_dir 重复: %q", owner, t.Name, t.RepoDir)
		}
		seen[key] = t.Name
	}
	return nil
}

// List 返回全部任务（只读访问，调用方不应修改）。
func (s *Store) List() []*Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Task, len(s.tasks))
	copy(out, s.tasks)
	return out
}

// Get 按名查找任务，未找到返回 nil。
func (s *Store) Get(name string) *Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.tasks {
		if t.Name == name {
			return t
		}
	}
	return nil
}

// Add 新增任务；校验合法性、名与 repo_dir 唯一性后追加。不落盘，需 Save 持久化。
func (s *Store) Add(t *Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.Name == "" {
		return fmt.Errorf("任务名不能为空")
	}
	if err := t.Normalize(); err != nil {
		return fmt.Errorf("任务 %q 校验失败: %w", t.Name, err)
	}
	// 与现有任务合并校验唯一性，通过后才追加
	combined := append(append([]*Task(nil), s.tasks...), t)
	if err := checkUniqueNames(combined); err != nil {
		return err
	}
	if err := checkUniqueRepoDirs(combined); err != nil {
		return err
	}
	s.tasks = append(s.tasks, t)
	return nil
}

// Update 用 t 替换名为 name 的任务；name 不存在或新任务与他人名/repo_dir 冲突返回错误。
func (s *Store) Update(name string, t *Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.Name == "" {
		return fmt.Errorf("任务名不能为空")
	}
	if err := t.Normalize(); err != nil {
		return fmt.Errorf("任务 %q 校验失败: %w", t.Name, err)
	}
	// 从列表移除被替换的自身后与新任务合并校验，防新旧同名/同目录误报
	next := make([]*Task, 0, len(s.tasks))
	var old *Task
	for _, ex := range s.tasks {
		if ex.Name == name {
			old = ex
			continue
		}
		next = append(next, ex)
	}
	if old == nil {
		return fmt.Errorf("任务不存在: %q", name)
	}
	next = append(next, t)
	if err := checkUniqueNames(next); err != nil {
		return err
	}
	if err := checkUniqueRepoDirs(next); err != nil {
		return err
	}
	s.tasks = next
	migrateByproducts(old, t)
	return nil
}

// migrateByproducts 任务重命名后迁移 byproduct：state 文件重命名到新键，旧锁文件清理。
// state 文件可能尚未生成（任务从未运行），静默跳过；锁文件可能正被进行中的同步持有，
// CleanStale 仅在无存活持有者时删除，避免误删活动锁。
func migrateByproducts(old, cur *Task) {
	if safeName(old.Name) == safeName(cur.Name) {
		return
	}
	if _, err := os.Stat(old.ResolveStateFile()); err == nil {
		_ = os.Rename(old.ResolveStateFile(), cur.ResolveStateFile())
	}
	lock.CleanStale(old.ResolveLockFile())
}

// Delete 按名删除任务。
func (s *Store) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, t := range s.tasks {
		if t.Name == name {
			s.tasks = append(s.tasks[:i], s.tasks[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("任务不存在: %q", name)
}

// ReplaceAll 用 tasks 全量替换当前任务列表（校验名唯一与合法性），不落盘，需 Save 持久化。
// 供 engine config-save 命令原子替换配置：先全量校验，任一失败则不动现有列表。
// 全量替换语义下不迁移 byproduct 文件（重命名迁移仅在 Update 单任务场景）。
func (s *Store) ReplaceAll(tasks []*Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range tasks {
		if t.Name == "" {
			return fmt.Errorf("任务名不能为空")
		}
		if err := t.Normalize(); err != nil {
			return fmt.Errorf("任务 %q 校验失败: %w", t.Name, err)
		}
	}
	if err := checkUniqueNames(tasks); err != nil {
		return err
	}
	if err := checkUniqueRepoDirs(tasks); err != nil {
		return err
	}
	s.tasks = append([]*Task(nil), tasks...)
	return nil
}

// Save 将全部任务以 tasks 列表形式写回配置文件。
// 原子写：先写临时文件再重命名，避免崩溃中途损坏配置。
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := yaml.Marshal(&taskFile{Tasks: s.tasks})
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".autosync.conf.*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("写入配置失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("关闭临时文件失败: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("替换配置失败: %w", err)
	}
	return nil
}

// ResolveStateFile 返回该任务的状态文件路径（~/.autosync/state/autosync.state-<name>.json）。
func (t *Task) ResolveStateFile() string {
	return config.StateFilePath(safeName(t.Name))
}

// ResolveLockFile 返回该任务的锁文件路径（~/.autosync/locks/autosync.lock-<name>）。
func (t *Task) ResolveLockFile() string {
	return config.LockFilePath(safeName(t.Name))
}

// unsafeNameRe 匹配文件名不安全字符。
var unsafeNameRe = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// safeName 把任务名转为文件名安全形式：非字母数字/下划线/连字符替换为 _。
func safeName(name string) string {
	s := unsafeNameRe.ReplaceAllString(name, "_")
	s = strings.Trim(s, "._")
	if s == "" {
		s = "default"
	}
	return s
}
