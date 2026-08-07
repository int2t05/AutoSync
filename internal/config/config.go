// config.go 负责同步配置的默认值填充与校验。
// 配置项内嵌于多任务配置文件 ~/.autosync/autosync.conf.yaml 的 tasks 列表（可用 --config 覆盖路径）。
// 校验：必填项缺失、目录不存在、策略非法、时间间隔无法解析或低于 1 分钟均报错，
// 调用方应据此以退出码 1 终止，避免运行中失败。
package config

import (
	"fmt"
	"os"
	"time"
)

// Config 描述单个同步任务所需的全部配置。
// 字段与 autosync.conf.yaml 的 tasks 项一一对应；带 yaml:"-" 的字段为解析后的派生值，不来自配置文件。
type Config struct {
	RepoDir           string        `yaml:"repo_dir" json:"repo_dir"`                       // 同步目标文件夹（必填）
	RemoteURL         string        `yaml:"remote_url" json:"remote_url"`                   // 远程仓库地址，首次初始化用（必填）
	Remote            string        `yaml:"remote" json:"remote,omitempty"`                 // 远程名，默认 origin
	Branch            string        `yaml:"branch" json:"branch,omitempty"`                 // 同步分支，默认 main
	Interval          string        `yaml:"interval" json:"interval,omitempty"`             // 轮询间隔字符串，默认 "1m"
	IntervalDur       time.Duration `yaml:"-" json:"-"`                                      // 解析后的轮询间隔
	ConflictStrategy  string        `yaml:"conflict_strategy" json:"conflict_strategy,omitempty"` // 冲突策略：local_wins|remote_wins|conflict_files
	BackupKeep        int           `yaml:"backup_keep" json:"backup_keep,omitempty"`       // backup 分支保留数，默认 10
	RetryCount        int           `yaml:"retry_count" json:"retry_count,omitempty"`       // 网络操作重试次数，默认 3
	RetryBaseDelay    string        `yaml:"retry_base_delay" json:"retry_base_delay,omitempty"` // 重试退避基数字符串，默认 "1s"
	RetryBaseDelayDur time.Duration `yaml:"-" json:"-"`                                      // 解析后的重试退避基数
	CommitMsgFormat   string        `yaml:"commit_msg_format" json:"commit_msg_format,omitempty"` // 提交消息模板，默认 "auto sync: {{.Timestamp}}"
	GitTimeout        string        `yaml:"git_timeout" json:"git_timeout,omitempty"`       // 单条 git 命令超时，默认 "60s"（防挂起冻结调度/退出）
	GitTimeoutDur     time.Duration `yaml:"-" json:"-"`                                      // 解析后的 git 命令超时
	ShowConsole       bool          `yaml:"show_console" json:"show_console,omitempty"`      // 是否输出到控制台
	Ignore            []string      `yaml:"ignore" json:"ignore,omitempty"`                 // 写入 repo_dir/.gitignore 的条目
}

// defaultIgnore 是未配置 ignore 时的默认忽略条目：
// 排除系统垃圾文件，避免污染同步仓库（byproduct 已位于 ~/.autosync/，不进仓库）。
var defaultIgnore = []string{
	"*.tmp",
	"Thumbs.db",
	"desktop.ini",
	".DS_Store",
}

// applyDefaults 为未设置（零值）的可选项填充默认值。
// 已由用户显式设置的项保持不变。
func (c *Config) applyDefaults() {
	if c.Remote == "" {
		c.Remote = "origin"
	}
	if c.Branch == "" {
		c.Branch = "main"
	}
	if c.Interval == "" {
		c.Interval = "1m"
	}
	if c.ConflictStrategy == "" {
		c.ConflictStrategy = "conflict_files"
	}
	if c.BackupKeep == 0 {
		c.BackupKeep = 10
	}
	if c.RetryCount == 0 {
		c.RetryCount = 3
	}
	if c.RetryBaseDelay == "" {
		c.RetryBaseDelay = "1s"
	}
	if c.CommitMsgFormat == "" {
		c.CommitMsgFormat = "auto sync: {{.Timestamp}}"
	}
	if c.GitTimeout == "" {
		c.GitTimeout = "60s"
	}
	if len(c.Ignore) == 0 {
		c.Ignore = append([]string(nil), defaultIgnore...)
	}
}

// validate 校验必填项、目录存在性、策略合法性与时间间隔可解析性。
// 校验通过时填充 IntervalDur / RetryBaseDelayDur 派生字段。
func (c *Config) validate() error {
	if err := c.validateRequired(); err != nil {
		return err
	}
	return c.validateRepoDir()
}

// validateRequired 校验必填项、策略合法性与时间间隔可解析性（不检查 repo_dir 存在性）。
// 供 LoadLenient 使用，使 status 等命令在仓库目录暂时不可用时仍能加载配置。
func (c *Config) validateRequired() error {
	if c.RepoDir == "" {
		return fmt.Errorf("config: repo_dir 不能为空")
	}
	if c.RemoteURL == "" {
		return fmt.Errorf("config: remote_url 不能为空")
	}
	switch c.ConflictStrategy {
	case "local_wins", "remote_wins", "conflict_files":
	default:
		return fmt.Errorf("config: conflict_strategy 非法 %q（需 local_wins|remote_wins|conflict_files）", c.ConflictStrategy)
	}
	dur, err := time.ParseDuration(c.Interval)
	if err != nil {
		return fmt.Errorf("config: interval 解析失败 %q: %w", c.Interval, err)
	}
	if dur < time.Minute {
		return fmt.Errorf("config: interval 不能小于 1 分钟（当前 %q）", c.Interval)
	}
	c.IntervalDur = dur
	rdur, err := time.ParseDuration(c.RetryBaseDelay)
	if err != nil {
		return fmt.Errorf("config: retry_base_delay 解析失败 %q: %w", c.RetryBaseDelay, err)
	}
	c.RetryBaseDelayDur = rdur
	gdur, err := time.ParseDuration(c.GitTimeout)
	if err != nil {
		return fmt.Errorf("config: git_timeout 解析失败 %q: %w", c.GitTimeout, err)
	}
	if gdur <= 0 {
		return fmt.Errorf("config: git_timeout 需大于 0（当前 %q）", c.GitTimeout)
	}
	c.GitTimeoutDur = gdur
	return nil
}

// validateRepoDir 校验 repo_dir 存在且为目录。
func (c *Config) validateRepoDir() error {
	info, err := os.Stat(c.RepoDir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("config: repo_dir 不存在或非目录: %s", c.RepoDir)
	}
	return nil
}

// Normalize 填充默认值并完整校验（含 repo_dir 存在性），填充派生字段。
// 导出供 configstore 复用，避免多任务配置重复实现默认值与校验。
func (c *Config) Normalize() error {
	c.applyDefaults()
	return c.validate()
}

// NormalizeLenient 填充默认值并校验（跳过 repo_dir 存在性），填充派生字段。
// 供 configstore.LoadLenient 复用，使 status 等命令在仓库目录暂时不可用时仍能读取配置。
func (c *Config) NormalizeLenient() error {
	c.applyDefaults()
	return c.validateRequired()
}
