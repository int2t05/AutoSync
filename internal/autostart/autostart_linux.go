// autostart_linux.go Linux 开机自启：写 systemd user service 并 enable+start。
// 用户级 unit（$XDG_CONFIG_HOME/systemd/user/autosync.service，默认 ~/.config/...）；loginctl enable-linger 后开机即启（无需登录）。
//
//go:build linux

package autostart

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// unitName systemd user service 名称。
const unitName = "autosync.service"

// unitPath 返回 user unit 文件路径（$XDG_CONFIG_HOME/systemd/user/autosync.service）。
func unitPath() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg, "systemd", "user", unitName), nil
}

// buildUnit 构造 systemd unit 文件内容，ExecStart 复用 BuildRunCommand（路径引号包裹，兼容空格）。
func buildUnit(exePath, configPath string) string {
	return fmt.Sprintf(`[Unit]
Description=AutoSync 文件同步守护
After=network.target

[Service]
ExecStart=%s
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`, BuildRunCommand(exePath, configPath))
}

// Enable 写 systemd user unit 并 daemon-reload + enable --now 启动。
func Enable(exePath, configPath string) error {
	path, err := unitPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(buildUnit(exePath, configPath)), 0o644); err != nil {
		return err
	}
	if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl daemon-reload 失败: %w: %s", err, out)
	}
	if out, err := exec.Command("systemctl", "--user", "enable", "--now", unitName).CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl enable --now 失败: %w: %s", err, out)
	}
	return nil
}

// Disable 停止并禁用 unit，删除 unit 文件后 daemon-reload。全程容忍 systemctl 不可用（无 systemd 环境）。
func Disable() error {
	exec.Command("systemctl", "--user", "disable", "--now", unitName).Run() // 容忍已不存在 / 无 systemd
	path, err := unitPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	exec.Command("systemctl", "--user", "daemon-reload").Run() // 容忍失败：文件已删，reload 非必需
	return nil
}

// IsEnabled 返回 unit 是否已 enable（systemctl is-enabled 退出码 0）。
func IsEnabled() bool {
	return exec.Command("systemctl", "--user", "is-enabled", unitName).Run() == nil
}

// BuildRunCommand 构造 daemon 启动命令（供 install 打印与 unit ExecStart；路径引号包裹兼容空格）。
func BuildRunCommand(exePath, configPath string) string {
	cmd := "\"" + exePath + "\" daemon"
	if configPath != "" {
		cmd += " --config \"" + configPath + "\""
	}
	return cmd
}
