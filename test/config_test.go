// config_test.go 验证配置加载、默认值填充与校验逻辑（真实临时文件，无 mock）。
package tests

import (
	"os"
	"path/filepath"
	"testing"

	"autosync/internal/config"
)

// makeRepoDir 创建一个真实临时目录作为 repo_dir，测试结束自动清理。
func makeRepoDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "autosync-repo-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

// writeConfig 把 content 写入 dir 下的 config.yaml，返回其真实路径。
func writeConfig(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}
	return p
}

// TestLoad_Valid_AppliesDefaults 验证最小合法配置加载成功且默认值正确填充。
// repo_dir 用单引号包裹，避免 Windows 路径反斜杠被 YAML 双引号转义。
func TestLoad_Valid_AppliesDefaults(t *testing.T) {
	repo := makeRepoDir(t)
	p := writeConfig(t, repo, "repo_dir: '"+repo+"'\nremote_url: 'git@github.com:u/r.git'\n")

	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if cfg.Remote != "origin" {
		t.Errorf("Remote 默认 = %q, 期望 origin", cfg.Remote)
	}
	if cfg.Branch != "main" {
		t.Errorf("Branch 默认 = %q, 期望 main", cfg.Branch)
	}
	if cfg.Interval != "1m" || cfg.IntervalDur.Seconds() != 60 {
		t.Errorf("Interval 默认 = %q / %v", cfg.Interval, cfg.IntervalDur)
	}
	if cfg.ConflictStrategy != "local_wins" {
		t.Errorf("策略默认 = %q, 期望 local_wins", cfg.ConflictStrategy)
	}
	if cfg.BackupKeep != 10 {
		t.Errorf("BackupKeep 默认 = %d, 期望 10", cfg.BackupKeep)
	}
	if cfg.RetryCount != 3 {
		t.Errorf("RetryCount 默认 = %d, 期望 3", cfg.RetryCount)
	}
	if cfg.RetryBaseDelay != "1s" || cfg.RetryBaseDelayDur.Seconds() != 1 {
		t.Errorf("RetryBaseDelay 默认 = %q / %v", cfg.RetryBaseDelay, cfg.RetryBaseDelayDur)
	}
	if len(cfg.Ignore) == 0 {
		t.Errorf("Ignore 应有默认条目")
	}
}

// TestLoad_MissingRepoDir 验证缺 repo_dir 报错。
func TestLoad_MissingRepoDir(t *testing.T) {
	d, err := os.MkdirTemp("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(d)
	p := writeConfig(t, d, "remote_url: 'git@github.com:u/r.git'\n")
	if _, err := config.Load(p); err == nil {
		t.Fatal("缺 repo_dir 应报错")
	}
}

// TestLoad_MissingRemoteURL 验证缺 remote_url 报错。
func TestLoad_MissingRemoteURL(t *testing.T) {
	repo := makeRepoDir(t)
	p := writeConfig(t, repo, "repo_dir: '"+repo+"'\n")
	if _, err := config.Load(p); err == nil {
		t.Fatal("缺 remote_url 应报错")
	}
}

// TestLoad_DirNotExist 验证 repo_dir 不存在报错。
func TestLoad_DirNotExist(t *testing.T) {
	d, err := os.MkdirTemp("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(d)
	p := writeConfig(t, d, "repo_dir: '/no/such/dir/xyz'\nremote_url: 'git@github.com:u/r.git'\n")
	if _, err := config.Load(p); err == nil {
		t.Fatal("repo_dir 不存在应报错")
	}
}

// TestLoad_InvalidStrategy 验证非法冲突策略报错。
func TestLoad_InvalidStrategy(t *testing.T) {
	repo := makeRepoDir(t)
	p := writeConfig(t, repo, "repo_dir: '"+repo+"'\nremote_url: 'git@github.com:u/r.git'\nconflict_strategy: 'bogus'\n")
	if _, err := config.Load(p); err == nil {
		t.Fatal("非法策略应报错")
	}
}

// TestLoad_BadInterval 验证 interval 无法解析报错。
func TestLoad_BadInterval(t *testing.T) {
	repo := makeRepoDir(t)
	p := writeConfig(t, repo, "repo_dir: '"+repo+"'\nremote_url: 'git@github.com:u/r.git'\ninterval: 'notaduration'\n")
	if _, err := config.Load(p); err == nil {
		t.Fatal("非法 interval 应报错")
	}
}
