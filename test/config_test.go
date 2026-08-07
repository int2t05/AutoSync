// config_test.go 验证配置默认值填充与校验逻辑（真实目录，无 mock）。
package tests

import (
	"os"
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

// mkConfig 构造最小合法配置（仅必填项，其余由 Normalize 补默认值）。
func mkConfig(repoDir, remoteURL string) *config.Config {
	return &config.Config{RepoDir: repoDir, RemoteURL: remoteURL}
}

// TestConfig_Normalize_AppliesDefaults 验证最小合法配置 Normalize 后默认值正确填充。
func TestConfig_Normalize_AppliesDefaults(t *testing.T) {
	repo := makeRepoDir(t)
	cfg := mkConfig(repo, "git@github.com:u/r.git")
	if err := cfg.Normalize(); err != nil {
		t.Fatalf("Normalize 失败: %v", err)
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
	if cfg.ConflictStrategy != "conflict_files" {
		t.Errorf("策略默认 = %q, 期望 conflict_files", cfg.ConflictStrategy)
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

// TestConfig_MissingRepoDir 验证缺 repo_dir 报错。
func TestConfig_MissingRepoDir(t *testing.T) {
	if err := mkConfig("", "git@github.com:u/r.git").Normalize(); err == nil {
		t.Fatal("缺 repo_dir 应报错")
	}
}

// TestConfig_MissingRemoteURL 验证缺 remote_url 报错。
func TestConfig_MissingRemoteURL(t *testing.T) {
	repo := makeRepoDir(t)
	if err := mkConfig(repo, "").Normalize(); err == nil {
		t.Fatal("缺 remote_url 应报错")
	}
}

// TestConfig_DirNotExist 验证 repo_dir 不存在报错。
func TestConfig_DirNotExist(t *testing.T) {
	if err := mkConfig("/no/such/dir/xyz", "git@github.com:u/r.git").Normalize(); err == nil {
		t.Fatal("repo_dir 不存在应报错")
	}
}

// TestConfig_InvalidStrategy 验证非法冲突策略报错。
func TestConfig_InvalidStrategy(t *testing.T) {
	repo := makeRepoDir(t)
	cfg := mkConfig(repo, "git@github.com:u/r.git")
	cfg.ConflictStrategy = "bogus"
	if err := cfg.Normalize(); err == nil {
		t.Fatal("非法策略应报错")
	}
}

// TestConfig_BadInterval 验证 interval 无法解析报错。
func TestConfig_BadInterval(t *testing.T) {
	repo := makeRepoDir(t)
	cfg := mkConfig(repo, "git@github.com:u/r.git")
	cfg.Interval = "notaduration"
	if err := cfg.Normalize(); err == nil {
		t.Fatal("非法 interval 应报错")
	}
}

// TestConfig_IntervalFloor 验证 interval 小于 1 分钟报错（防毫秒级忙轮询）。
func TestConfig_IntervalFloor(t *testing.T) {
	repo := makeRepoDir(t)
	for _, iv := range []string{"50ms", "500ms", "59s"} {
		cfg := mkConfig(repo, "git@github.com:u/r.git")
		cfg.Interval = iv
		if err := cfg.Normalize(); err == nil {
			t.Errorf("interval=%s 应因低于 1 分钟报错", iv)
		}
	}
	// 恰好 1m 通过
	cfg := mkConfig(repo, "git@github.com:u/r.git")
	cfg.Interval = "1m"
	if err := cfg.Normalize(); err != nil {
		t.Errorf("interval=1m 应通过: %v", err)
	}
}

// TestConfig_NormalizeLenient_SkipsRepoDir 验证 NormalizeLenient 跳过 repo_dir 存在性校验。
func TestConfig_NormalizeLenient_SkipsRepoDir(t *testing.T) {
	cfg := mkConfig("/no/such/dir/xyz", "git@github.com:u/r.git")
	if err := cfg.NormalizeLenient(); err != nil {
		t.Fatalf("NormalizeLenient 应跳过 repo_dir 存在性: %v", err)
	}
	if cfg.Interval != "1m" {
		t.Errorf("Interval 默认未填充: %q", cfg.Interval)
	}
}
