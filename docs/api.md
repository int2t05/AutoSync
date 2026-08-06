# API · 命令接口

> CLI 即用户接口。请求 = 命令行调用，响应 = 标准输出 + 退出码。无子命令时按平台默认守护：Windows 托盘 / macOS 引擎 / Linux daemon。

## 通用

- 配置文件默认 `~/.autosync/config.yaml`，`--config` 覆盖路径。多任务配置为 `~/.autosync/autosync.conf.yaml`（托盘/daemon 共用）。
- byproduct 统一在 `~/.autosync/`：日志 `logs/autosync.log`、状态 `state/`、锁 `locks/`。可用 `AUTOSYNC_DATA_DIR` 覆盖。
- `show_console: true` 时日志同时输出到控制台。
- 退出码：`0` 成功 / 静默跳过；`1` 同步失败。

## sync

执行单次同步。

```
autosync [sync] [--config <path>] [--dry-run]
```

| 旗标 | 说明 |
|------|------|
| `--config <path>` | 配置文件路径 |
| `--dry-run` | 只读预览同步计划，不联网、不改仓库、不加锁 |

**请求（正常同步）**

```
$ autosync --config D:\sync\config.yaml
```

**响应（stdout，show_console=true 时）**

```
[2026-07-27 16:14:38] [INFO] ========== 同步开始 ==========
[2026-07-27 16:14:38] [INFO] 本地变更已提交
[2026-07-27 16:14:42] [INFO] 同步完成: 自动合并 — 同步完成（自动合并成功）
```
退出码 `0`。失败时退出码 `1` 并（按策略）弹系统通知。

**请求（dry-run）**

```
$ autosync --config D:\sync\config.yaml --dry-run
```

**响应**

```
AutoSync 同步计划（dry-run，不实际执行）
────────────────────────────────
1. 将提交本地变更（git add -A + commit）
2. 本地与远程一致（UpToDate）：无需推送
```
退出码 `0`。不写状态文件、不加锁、不通知。

## status

读取并展示上次同步状态。用宽松加载，允许 repo_dir 暂时不可用。

```
autosync status [--config <path>]
```

**请求**

```
$ autosync status --config D:\sync\config.yaml
```

**响应**

```
AutoSync 状态
────────────────────────────────
仓库目录: D:\Download\File
远程:     https://github.com/int2t05/File.git (main)
策略:     local_wins | 间隔: 1m
────────────────────────────────
上次同步: 2026-07-27 16:14:45
结果:     自动合并
摘要:     同步完成（自动合并成功）
备份分支: backup/remote-20260727_161438
```
退出码 `0`。未同步过时显示"尚未同步过"。

## install

设置开机自启：Windows 写注册表 `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` 启动托盘守护；Linux 写 systemd user service 启动 daemon。macOS 由 Swift 壳 SMAppService 管理（Go 端返回未实现）。

```
autosync install [--config <conf>]
```

**请求**

```
$ autosync install
```

**响应（Windows）**

```
✅ 已设置开机自启："D:\AutoSync\AutoSync.exe" tray --background
```

**响应（Linux）**

写 `~/.config/systemd/user/autosync.service`（`ExecStart` 调 `daemon`）+ `systemctl --user enable --now`。提示 `loginctl enable-linger $USER` 以开机即启（无需登录）。退出码 `0`。

Windows 自启命令带 `--background` 后台启动（不弹窗口）；Linux 自启由 systemd unit `ExecStart` 调 `daemon`。`--config` 指定配置路径（相对路径转绝对，全平台），缺省由守护自行解析 `autosync.conf.yaml`。macOS 返回未实现（壳管 SMAppService）。

## uninstall

移除开机自启：Windows 删注册表项；Linux `systemctl --user disable --now` + 删 unit 文件。

```
autosync uninstall
```

退出码 `0`。

## daemon

前台多任务守护（Linux 主用法；复用 TaskScheduler，无 GUI）。systemd user service 的 `ExecStart` 指向本命令。

```
autosync daemon [--config <conf>]
```

- 加载 `autosync.conf.yaml` 多任务，每任务独立 ticker 定时同步（启动即触发首次同步）。
- 获取守护级单实例锁（`autosync.daemon.lock`），防多实例；第二实例退出码 `1`。
- 阻塞等待 SIGINT/SIGTERM 优雅退出（Ctrl+C 或 `systemctl --user stop autosync`）。
- 通知走 beeep（Linux: D-Bus → notify-send → kdialog）。
- 无运行时控制 IPC：手动同步用 `autosync sync` 单次，暂停靠编辑配置后重启 daemon（见 [TODO](TODO.md) 后续方向）。

## tray（托盘模式）

无参数启动托盘守护进程（Fyne 窗口 + 托盘 + 内置定时器，Windows）。

```
autosync                      # 启动托盘守护（双击等同，弹出配置窗口）
autosync tray --background    # 后台启动（不弹窗口，供开机自启 / 无头）
```

- 双击 exe 无 cmd 黑窗（`-H windowsgui`）：弹出配置窗口，关闭按钮即缩至托盘，守护继续运行。
- 配置窗口：任务列表增删改，每任务 repo_dir / remote_url / branch / interval / conflict_strategy 等。
- 托盘菜单：各任务手动同步 / 暂停、开机自启开关、打开配置、退出（同步状态经 `autosync status` 查询）。
- 多任务配置：`autosync.conf.yaml` 的 `tasks: [...]`。
- 开机自启：`install` 写注册表 Run 键（命令带 `--background`）、`uninstall` 移除（见上）；托盘菜单亦可切换。

## 配置文件

| 字段 | 必填 | 默认 | 说明 |
|------|------|------|------|
| `repo_dir` | 是 | — | 同步目标文件夹 |
| `remote_url` | 是 | — | 远程地址，首次初始化用 |
| `remote` | 否 | `origin` | 远程名 |
| `branch` | 否 | `main` | 同步分支 |
| `interval` | 否 | `1m` | 轮询间隔（最小粒度 1 分钟） |
| `conflict_strategy` | 否 | `conflict_files` | `local_wins` / `remote_wins` / `conflict_files` |
| `backup_keep` | 否 | `10` | backup 分支保留数 |
| `retry_count` | 否 | `3` | 网络操作重试次数 |
| `retry_base_delay` | 否 | `1s` | 重试退避基数（指数） |
| `git_timeout` | 否 | `60s` | 单条 git 命令超时（大仓库/慢网络请调大） |
| `commit_msg_format` | 否 | `auto sync: {{.Timestamp}}` | 提交消息模板 |
| `log_file` | 否 | `autosync.log` | 日志文件名 |
| `state_file` | 否 | `autosync.state.json` | 状态文件名 |
| `show_console` | 否 | `false` | 是否输出到控制台 |
| `ignore` | 否 | 见下 | 追加到 repo_dir/.gitignore 的条目 |

`ignore` 默认：`*.tmp`、`Thumbs.db`、`desktop.ini`、`.DS_Store`、`autosync.log`、`autosync.state.json`、`config.yaml`。

多任务配置（`autosync.conf.yaml`）顶层为 `tasks: [...]`，每项 `name` + 上述字段（`name` 唯一）。模板见 `autosync.conf.example.yaml`。

## 通知策略

| 同步结果 | 通知 | 级别 |
|----------|------|------|
| NoChanges / Pushed / AutoMerged | 静默 | — |
| InitDone | 是 | 信息 |
| ConflictResolved | 是（含备份分支） | 警告 |
| Failed | 是 | 错误 |
