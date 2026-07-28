# AutoSync Linux 安装

## 1. 安装二进制
```bash
tar -xzf autosync-linux-amd64.tar.gz   # arm64 机器用 autosync-linux-arm64.tar.gz
cd autosync-linux-amd64
bash install-linux.sh
```

## 2. 配置同步任务
编辑 `~/.config/AutoSync/autosync.conf.yaml`（模板已由 install 脚本生成），填写 `tasks` 列表。字段见同目录 `autosync.conf.example.yaml`。

## 3. 前台试运行
```bash
autosync daemon
```
Ctrl+C 退出。

## 4. 开机自启（systemd user service）
```bash
autosync install
loginctl enable-linger $USER   # 开机即启，无需登录
```

验证服务状态：
```bash
systemctl --user status autosync
```

## 5. 日常命令
- `autosync daemon` — 前台多任务守护（systemd 即调用此命令）
- `autosync status` — 查看上次同步状态
- `autosync sync --config <task.yaml>` — 单次同步（CLI，非守护）
- `autosync install` / `autosync uninstall` — 注册/移除 systemd 开机自启

## 依赖
- 系统 git（PATH 中）
- git 凭证（SSH key 或 credential helper）
- libnotify-bin（桌面通知，可选；beeep 优先走 D-Bus，缺失时降级 notify-send/kdialog）

## 排查
- 服务未启动：`systemctl --user status autosync` 看日志；`journalctl --user -u autosync`
- 通知不显示：GNOME 需确认通知未禁用；无 D-Bus 环境装 `libnotify-bin`
- 配置校验失败：`autosync daemon` 前台运行看错误输出
