// configstore.go 管理多任务同步配置（autosync.conf.yaml 的 tasks 列表）。
// Task 内嵌 Config 复用默认值与校验。
// 每任务按名解析独立的 state/lock 文件，互不干扰。
package configstore

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	path string
	mu   sync.Mutex
	tasks []*Task
	prev []string // 上次成功落盘的任务 safeName 键，Save 成功后据此清理被移除任务的孤儿 byproduct
}

// taskFile 是顶层 YAML 结构。
type taskFile struct {
	Tasks []*Task `yaml:"tasks"`
}

// Load 从 path 读取多任务配置，对每个任务填充默认值并完整校验（含 repo_dir 存在性）。
// 文件不存在或无任务时返回空存储：托盘以空配置启动，由配置窗口新增任务后 Save 落盘。
func Load(path string) (*Store, error) {
	return loadStore(path, false)
}

// LoadLenient 加载多任务配置但跳过 repo_dir 存在性校验。
// 供 status 等命令在仓库目录暂时不可用时仍能列出任务与读取状态。
func LoadLenient(path string) (*Store, error) {
	return loadStore(path, true)
}

// loadStore 读取并校验多任务配置；lenient 为 true 时跳过 repo_dir 存在性校验。
func loadStore(path string, lenient bool) (*Store, error) {
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
		var nerr error
		if lenient {
			nerr = t.NormalizeLenient()
		} else {
			nerr = t.Normalize()
		}
		if nerr != nil {
			return nil, fmt.Errorf("任务 %q 校验失败: %w", t.Name, nerr)
		}
	}
	if err := checkUniqueNames(tasks); err != nil {
		return nil, err
	}
	if err := checkUniqueRepoDirs(tasks); err != nil {
		return nil, err
	}
	return &Store{path: path, tasks: tasks, prev: taskKeys(tasks)}, nil
}

// NewStore 创建空存储（供新增任务后保存）。
func NewStore(path string) *Store {
	return &Store{path: path}
}

// parseTasks 解析 YAML 顶层 tasks 列表；空文件或 tasks:[] 返回空。
// KnownFields(true) 使未知字段报错而非静默忽略，杜绝拼错字段名无提示。
func parseTasks(data []byte) ([]*Task, error) {
	var tf taskFile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&tf); err != nil {
		if err == io.EOF {
			return nil, nil // 空文件：空任务列表
		}
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}
	return tf.Tasks, nil
}

// checkUniqueNames 校验任务名按 safeName 解析后唯一（"a b" 与 "a_b" 同键冲突，
// 否则二者共享 state/lock 文件互相串扰）。
func checkUniqueNames(tasks []*Task) error {
	seen := make(map[string]string) // safeName -> 原任务名
	for _, t := range tasks {
		key := config.SafeName(t.Name)
		if owner, dup := seen[key]; dup {
			return fmt.Errorf("任务名冲突: %q 与 %q 均解析为 %q", owner, t.Name, key)
		}
		seen[key] = t.Name
	}
	return nil
}

// checkUniqueRepoDirs 校验任务 repo_dir 跨任务唯一，防止多任务并发读写同一仓库互相破坏。
// 键为 filepath.Clean 归一化路径；Windows 与 macOS 默认文件系统大小写不敏感，统一小写比较，
// 其余平台保持大小写敏感（macOS APFS 大小写不敏感，仅大小写不同的路径实为同一目录）。
func checkUniqueRepoDirs(tasks []*Task) error {
	seen := make(map[string]string) // 归一化 repo_dir -> 任务名
	for _, t := range tasks {
		key := filepath.Clean(t.RepoDir)
		if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
			key = strings.ToLower(key)
		}
		if owner, dup := seen[key]; dup {
			return fmt.Errorf("任务 %q 与 %q 的 repo_dir 重复: %q", owner, t.Name, t.RepoDir)
		}
		seen[key] = t.Name
	}
	return nil
}

// List 返回全部任务的深拷贝，调用方可安全修改而不影响内部状态。
func (s *Store) List() []*Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Task, len(s.tasks))
	for i, t := range s.tasks {
		out[i] = cloneTask(t)
	}
	return out
}

// Path 返回配置文件路径（供开机自启命令携带 --config）。
func (s *Store) Path() string { return s.path }

// Get 按名查找任务的深拷贝，未找到返回 nil；调用方修改返回对象不影响内部状态。
// 查找键与判重键统一（safeName 规范化）：任务名 "a b" 可经 "a b" 或 "a_b" 两种写法命中。
func (s *Store) Get(name string) *Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := config.SafeName(name)
	for _, t := range s.tasks {
		if config.SafeName(t.Name) == key {
			return cloneTask(t)
		}
	}
	return nil
}

// NormalizeName 返回任务名的规范化查找键（safeName），供其他包与判重逻辑保持一致。
func NormalizeName(name string) string { return config.SafeName(name) }

// cloneTask 深拷贝任务：切片字段（Ignore）复制底层数组，防调用方经返回指针改写内部状态。
func cloneTask(t *Task) *Task {
	c := *t
	c.Ignore = append([]string(nil), t.Ignore...)
	return &c
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
// 目标 state 已存在时先移除再 Rename（Windows rename 不覆盖目标），避免旧残留与新数据并存。
func migrateByproducts(old, cur *Task) {
	if config.SafeName(old.Name) == config.SafeName(cur.Name) {
		return
	}
	oldState, newState := old.ResolveStateFile(), cur.ResolveStateFile()
	if _, err := os.Stat(oldState); err == nil {
		os.Remove(newState)
		if err := os.Rename(oldState, newState); err != nil {
			// 迁移失败（极少）：旧 state 保留，下次同步会写入新键，无数据丢失
			return
		}
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
// 原子写：临时文件写入并 Sync 后重命名，避免崩溃中途损坏配置或丢落盘数据。
// 落盘成功后清理已移除任务的孤儿 byproduct（见 cleanRemovedByproducts）。
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
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("同步配置失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("关闭临时文件失败: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("替换配置失败: %w", err)
	}
	s.cleanRemovedByproducts()
	return nil
}

// cleanRemovedByproducts 清理已从配置持久化移除的任务的孤儿 byproduct（state/lock）。
// 依据上次成功落盘的键对比当前键：仅清理已确认移除的任务；Save 失败回滚（ReplaceAll 恢复旧列表）
// 后再次 Save 时任务键仍在集合中，不会误删。锁内调用（读写 s.tasks / s.prev）。
func (s *Store) cleanRemovedByproducts() {
	cur := make(map[string]bool, len(s.tasks))
	for _, t := range s.tasks {
		cur[config.SafeName(t.Name)] = true
	}
	for _, k := range s.prev {
		if !cur[k] {
			os.Remove(config.StateFilePath(k))
			lock.CleanStale(config.LockFilePath(k)) // 仅清非存活持有者的锁，避免误删进行中的同步
		}
	}
	s.prev = s.prev[:0]
	for k := range cur {
		s.prev = append(s.prev, k)
	}
}

// taskKeys 返回任务列表的 safeName 键集合（供 prev 快照）。
func taskKeys(tasks []*Task) []string {
	keys := make([]string, 0, len(tasks))
	for _, t := range tasks {
		keys = append(keys, config.SafeName(t.Name))
	}
	return keys
}

// ResolveStateFile 返回该任务的状态文件路径（~/.autosync/state/autosync.state-<name>.json）。
// 任务名安全化在 config.StateFilePath 内统一完成（SafeName 单一边界）。
func (t *Task) ResolveStateFile() string {
	return config.StateFilePath(t.Name)
}

// ResolveLockFile 返回该任务的锁文件路径（~/.autosync/locks/autosync.lock-<name>）。
func (t *Task) ResolveLockFile() string {
	return config.LockFilePath(t.Name)
}
