// configstore_test.go 验证多任务配置存储：加载/CRUD/每任务路径/校验（真实文件，无 mock）。
package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"autosync/internal/config"
	"autosync/internal/configstore"
)

// writeConfigFile 将 YAML 内容写入临时目录的 autosync.conf.yaml，返回路径。
func writeConfigFile(t *testing.T, content string) string {
	t.Helper()
	d := makeTempDir(t, "autosync-cfg-*")
	p := filepath.Join(d, "autosync.conf.yaml")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

// twoRepoDirs 创建两个临时目录作为任务 repo_dir。
func twoRepoDirs(t *testing.T) (string, string) {
	t.Helper()
	return makeTempDir(t, "autosync-repo1-*"), makeTempDir(t, "autosync-repo2-*")
}

// mkTask 构造测试任务（仅必填项，其余由 Normalize 补默认值）。
func mkTask(name, repoDir, remoteURL string) *configstore.Task {
	return &configstore.Task{Name: name, Config: config.Config{RepoDir: repoDir, RemoteURL: remoteURL}}
}

// TestConfigStore_LoadMultiTask 验证多任务配置加载与默认值填充。
// 任务名须为 ASCII（safeName 按文件名安全形式判重，两个 CJK 名会解析碰撞）。
func TestConfigStore_LoadMultiTask(t *testing.T) {
	d1, d2 := twoRepoDirs(t)
	content := "tasks:\n" +
		"  - name: 'proj'\n" +
		"    repo_dir: '" + d1 + "'\n" +
		"    remote_url: 'https://github.com/a/b.git'\n" +
		"  - name: 'docs'\n" +
		"    repo_dir: '" + d2 + "'\n" +
		"    remote_url: 'https://github.com/c/d.git'\n" +
		"    conflict_strategy: 'remote_wins'\n"
	store, err := configstore.Load(writeConfigFile(t, content))
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	tasks := store.List()
	if len(tasks) != 2 {
		t.Fatalf("任务数 = %d, 期望 2", len(tasks))
	}
	if tasks[0].Name != "proj" || tasks[0].RepoDir != d1 {
		t.Errorf("任务0: name=%s repo=%s", tasks[0].Name, tasks[0].RepoDir)
	}
	if tasks[0].Remote != "origin" || tasks[0].Branch != "main" || tasks[0].ConflictStrategy != "conflict_files" {
		t.Errorf("任务0 默认值未填充: remote=%s branch=%s strategy=%s", tasks[0].Remote, tasks[0].Branch, tasks[0].ConflictStrategy)
	}
	if tasks[1].ConflictStrategy != "remote_wins" {
		t.Errorf("任务1 strategy=%s, 期望 remote_wins", tasks[1].ConflictStrategy)
	}
}

// TestConfigStore_CRUD 验证增删改查、重名拒绝与持久化往返。
func TestConfigStore_CRUD(t *testing.T) {
	d1, d2 := twoRepoDirs(t)
	p := filepath.Join(makeTempDir(t, "autosync-cfg-*"), "autosync.conf.yaml")
	store := configstore.NewStore(p)

	if err := store.Add(mkTask("t1", d1, "u1")); err != nil {
		t.Fatalf("Add t1: %v", err)
	}
	if err := store.Add(mkTask("t2", d2, "u2")); err != nil {
		t.Fatalf("Add t2: %v", err)
	}
	if store.Get("nope") != nil {
		t.Errorf("Get 不存在应返回 nil")
	}
	if store.Get("t1") == nil {
		t.Errorf("Get t1 应非 nil")
	}
	if err := store.Add(mkTask("t1", d1, "u3")); err == nil {
		t.Errorf("重复名 Add 应失败")
	}
	upd := mkTask("t1", d1, "u1-updated")
	if err := store.Update("t1", upd); err != nil {
		t.Fatalf("Update t1: %v", err)
	}
	if got := store.Get("t1").RemoteURL; got != "u1-updated" {
		t.Errorf("Update 未生效: %s", got)
	}
	if err := store.Delete("t2"); err != nil {
		t.Fatalf("Delete t2: %v", err)
	}
	if store.Get("t2") != nil {
		t.Errorf("Delete 后 t2 应不存在")
	}
	if err := store.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reloaded, err := configstore.Load(p)
	if err != nil {
		t.Fatalf("重载失败: %v", err)
	}
	rl := reloaded.List()
	if len(rl) != 1 || rl[0].Name != "t1" || rl[0].RemoteURL != "u1-updated" {
		t.Errorf("重载后任务不符: %+v", rl)
	}
}

// TestConfigStore_PerTaskPaths 验证每任务 state/lock 路径互异且按名安全化。
func TestConfigStore_PerTaskPaths(t *testing.T) {
	d1, d2 := twoRepoDirs(t)
	t1 := mkTask("alpha", d1, "u1")
	t2 := mkTask("beta", d2, "u2")
	if t1.ResolveStateFile() == t2.ResolveStateFile() {
		t.Errorf("两任务 state 路径相同: %s", t1.ResolveStateFile())
	}
	if !strings.HasSuffix(t1.ResolveStateFile(), "autosync.state-alpha.json") {
		t.Errorf("t1 state 路径不符: %s", t1.ResolveStateFile())
	}
	if !strings.HasSuffix(t1.ResolveLockFile(), "autosync.lock-alpha") {
		t.Errorf("t1 lock 路径不符: %s", t1.ResolveLockFile())
	}
	// 特殊字符名应被安全化，路径不含分隔符
	t3 := mkTask("我的/任务", d1, "u3")
	s3 := t3.ResolveStateFile()
	if strings.ContainsAny(s3, "/\\") && strings.Contains(s3, "我的") {
		t.Errorf("特殊字符名未安全化: %s", s3)
	}
}

// TestConfigStore_LoadMissingFile 验证配置文件不存在时返回空存储（托盘空配置启动），新增任务后 Save 可落盘重载。
func TestConfigStore_LoadMissingFile(t *testing.T) {
	p := filepath.Join(makeTempDir(t, "autosync-cfg-*"), "autosync.conf.yaml")
	store, err := configstore.Load(p)
	if err != nil {
		t.Fatalf("缺失文件应返回空存储而非错误: %v", err)
	}
	if len(store.List()) != 0 {
		t.Fatalf("空存储任务数 = %d, 期望 0", len(store.List()))
	}
	d := makeTempDir(t, "autosync-repo-*")
	if err := store.Add(mkTask("t1", d, "u1")); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	reloaded, err := configstore.Load(p)
	if err != nil {
		t.Fatalf("重载失败: %v", err)
	}
	if len(reloaded.List()) != 1 || reloaded.List()[0].Name != "t1" {
		t.Errorf("重载后任务不符: %+v", reloaded.List())
	}
}

// TestConfigStore_LoadEmptyConfig 验证空文件与 tasks:[] 均返回空存储而非报错。
func TestConfigStore_LoadEmptyConfig(t *testing.T) {
	for _, content := range []string{"", "tasks: []\n"} {
		store, err := configstore.Load(writeConfigFile(t, content))
		if err != nil {
			t.Fatalf("内容 %q 应返回空存储而非错误: %v", content, err)
		}
		if len(store.List()) != 0 {
			t.Errorf("内容 %q 任务数 = %d, 期望 0", content, len(store.List()))
		}
	}
}

// TestConfigStore_Validation 验证非法策略、重名、repo_dir 不存在均加载失败。
func TestConfigStore_Validation(t *testing.T) {
	d := makeTempDir(t, "autosync-repo-*")
	// 非法 conflict_strategy
	bad := "tasks:\n  - name: 'bad'\n    repo_dir: '" + d + "'\n    remote_url: 'u'\n    conflict_strategy: 'nope'\n"
	if _, err := configstore.Load(writeConfigFile(t, bad)); err == nil {
		t.Errorf("非法策略应加载失败")
	}
	// 重复名
	dup := "tasks:\n  - name: 'dup'\n    repo_dir: '" + d + "'\n    remote_url: 'u'\n  - name: 'dup'\n    repo_dir: '" + d + "'\n    remote_url: 'u'\n"
	if _, err := configstore.Load(writeConfigFile(t, dup)); err == nil {
		t.Errorf("重复名应加载失败")
	}
	// repo_dir 不存在
	missing := filepath.Join(d, "missing")
	miss := "tasks:\n  - name: 'miss'\n    repo_dir: '" + missing + "'\n    remote_url: 'u'\n"
	if _, err := configstore.Load(writeConfigFile(t, miss)); err == nil {
		t.Errorf("repo_dir 不存在应加载失败")
	}
}

// TestConfigStore_SaveAtomic 验证 Save 写入配置且不残留临时文件，可重载。
func TestConfigStore_SaveAtomic(t *testing.T) {
	d := makeTempDir(t, "autosync-repo-*")
	p := filepath.Join(makeTempDir(t, "autosync-cfg-*"), "autosync.conf.yaml")
	store := configstore.NewStore(p)
	if err := store.Add(mkTask("t1", d, "u1")); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reloaded, err := configstore.Load(p)
	if err != nil {
		t.Fatalf("重载失败: %v", err)
	}
	if len(reloaded.List()) != 1 || reloaded.List()[0].Name != "t1" {
		t.Errorf("重载后任务不符: %+v", reloaded.List())
	}
	// 原子写不应残留临时文件
	entries, _ := os.ReadDir(filepath.Dir(p))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("残留临时文件: %s", e.Name())
		}
	}
}

// TestConfigStore_RepoDirUnique 验证四入口（Load/Add/Update/ReplaceAll）均拒绝重复 repo_dir。
func TestConfigStore_RepoDirUnique(t *testing.T) {
	d1, d2 := twoRepoDirs(t)

	// Load 拒绝重复 repo_dir
	dup := "tasks:\n  - name: 'a'\n    repo_dir: '" + d1 + "'\n    remote_url: 'u'\n  - name: 'b'\n    repo_dir: '" + d1 + "'\n    remote_url: 'u'\n"
	if _, err := configstore.Load(writeConfigFile(t, dup)); err == nil {
		t.Errorf("Load 重复 repo_dir 应失败")
	}

	// Add 拒绝与现有任务同 repo_dir；独立目录可通过
	p := filepath.Join(makeTempDir(t, "autosync-cfg-*"), "autosync.conf.yaml")
	store := configstore.NewStore(p)
	if err := store.Add(mkTask("a", d1, "u")); err != nil {
		t.Fatalf("Add a: %v", err)
	}
	if err := store.Add(mkTask("b", d1, "u")); err == nil {
		t.Errorf("Add 重复 repo_dir 应失败")
	}
	if err := store.Add(mkTask("b", d2, "u")); err != nil {
		t.Fatalf("Add b 独立 repo_dir 应通过: %v", err)
	}

	// Update 排除自身：a 保留原目录可通过；b 改为指向 a 的目录应失败
	if err := store.Update("a", mkTask("a", d1, "u2")); err != nil {
		t.Fatalf("Update a 自身目录应通过: %v", err)
	}
	if err := store.Update("b", mkTask("b", d1, "u2")); err == nil {
		t.Errorf("Update 指向他人 repo_dir 应失败")
	}

	// ReplaceAll 拒绝重复 repo_dir
	if err := store.ReplaceAll([]*configstore.Task{mkTask("x", d1, "u"), mkTask("y", d1, "u")}); err == nil {
		t.Errorf("ReplaceAll 重复 repo_dir 应失败")
	}
}

// TestConfigStore_SafeNameCollision 验证四入口按 safeName 判重（"a b" 与 "a_b" 冲突）。
func TestConfigStore_SafeNameCollision(t *testing.T) {
	d1, d2 := twoRepoDirs(t)

	// Load："a b" 与 "a_b" 均解析为 a_b → 拒绝
	dup := "tasks:\n  - name: 'a b'\n    repo_dir: '" + d1 + "'\n    remote_url: 'u'\n  - name: 'a_b'\n    repo_dir: '" + d2 + "'\n    remote_url: 'u'\n"
	if _, err := configstore.Load(writeConfigFile(t, dup)); err == nil {
		t.Errorf("Load safeName 冲突应失败")
	}

	// Add：已有 a_b 时新增 "a b" 应失败
	p := filepath.Join(makeTempDir(t, "autosync-cfg-*"), "autosync.conf.yaml")
	store := configstore.NewStore(p)
	if err := store.Add(mkTask("a_b", d1, "u")); err != nil {
		t.Fatalf("Add a_b: %v", err)
	}
	if err := store.Add(mkTask("a b", d2, "u")); err == nil {
		t.Errorf("Add safeName 冲突应失败")
	}
	if err := store.Add(mkTask("c", d2, "u")); err != nil {
		t.Fatalf("Add c: %v", err)
	}

	// Update：c 改名为 "a b"（safeName a_b）与现有 a_b 冲突 → 失败
	if err := store.Update("c", mkTask("a b", d2, "u")); err == nil {
		t.Errorf("Update 改名 safeName 冲突应失败")
	}

	// ReplaceAll：两任务 safeName 冲突应失败
	if err := store.ReplaceAll([]*configstore.Task{mkTask("c d", d1, "u"), mkTask("c_d", d2, "u")}); err == nil {
		t.Errorf("ReplaceAll safeName 冲突应失败")
	}
}
