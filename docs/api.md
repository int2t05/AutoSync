# API · 命令接口

> CLI 即用户接口。请求 = 命令行调用，响应 = 标准输出 + 退出码。无子命令时默认 `sync`。

## 通用

- 配置文件默认与二进制同目录的 `config.yaml`，`--config` 覆盖路径。
- 日志写 `autosync.log`（二进制同目录），`show_console: true` 时同时输出到控制台。
- 退出码：`0` 成功 / 静默跳过；`1` 同步失败或冲突中止。

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

设置开机自启：写注册表 `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`，登录即启动托盘守护。

```
autosync install [--config <tray-conf>]
```

**请求**

```
$ autosync install
```

**响应**

```
✅ 已设置开机自启："D:\AutoSync\AutoSync.exe" tray
```
退出码 `0`。`--config` 指定托盘配置路径（相对路径转绝对），缺省由托盘自行解析同目录 `autosync.conf.yaml`。非 Windows 返回未实现错误，退出码 `1`。

## uninstall

移除开机自启注册表项。

```
autosync uninstall
```

**请求**

```
$ autosync uninstall
```

**响应**

```
✅ 已移除开机自启
```
退出码 `0`。

## V1.1 托盘模式

无参数启动托盘守护进程（Fyne 窗口 + 托盘 + 内置定时器）。

```
autosync                      # 启动托盘守护
```

- 配置窗口：任务列表增删改，每任务 repo_dir / remote_url / branch / interval / conflict_strategy 等。
- 托盘菜单：各任务手动同步 / 暂停、开机自启开关、打开配置、退出（同步状态经 `autosync status` 查询）。
- 多任务配置：`autosync.conf.yaml` 的 `tasks: [...]`。
- 开机自启：`install` 写注册表 Run 键、`uninstall` 移除（见上）；托盘菜单亦可切换。

## 配置文件

| 字段 | 必填 | 默认 | 说明 |
|------|------|------|------|
| `repo_dir` | 是 | — | 同步目标文件夹 |
| `remote_url` | 是 | — | 远程地址，首次初始化用 |
| `remote` | 否 | `origin` | 远程名 |
| `branch` | 否 | `main` | 同步分支 |
| `interval` | 否 | `1m` | 轮询间隔（最小粒度 1 分钟） |
| `conflict_strategy` | 否 | `local_wins` | `local_wins` / `remote_wins` / `abort` |
| `backup_keep` | 否 | `10` | backup 分支保留数 |
| `retry_count` | 否 | `3` | 网络操作重试次数 |
| `retry_base_delay` | 否 | `1s` | 重试退避基数（指数） |
| `commit_msg_format` | 否 | `auto sync: {{.Timestamp}}` | 提交消息模板 |
| `log_file` | 否 | `autosync.log` | 日志文件名 |
| `state_file` | 否 | `autosync.state.json` | 状态文件名 |
| `show_console` | 否 | `false` | 是否输出到控制台 |
| `ignore` | 否 | 见下 | 追加到 repo_dir/.gitignore 的条目 |

`ignore` 默认：`*.tmp`、`Thumbs.db`、`desktop.ini`、`.DS_Store`、`autosync.log`、`autosync.state.json`、`config.yaml`。

## 通知策略

| 同步结果 | 通知 | 级别 |
|----------|------|------|
| NoChanges / Pushed / AutoMerged | 静默 | — |
| InitDone | 是 | 信息 |
| ConflictResolved | 是（含备份分支） | 警告 |
| ConflictAborted / Failed | 是 | 错误 |
