# AutoSync

> 基于系统 git + Go 的跨平台文件夹双向同步工具。配置一次，定时轮询、自动提交、冲突自动处理，完全无感。

## 简介

AutoSync 把指定本地文件夹与一个 git 远程仓库保持双向一致。它通过 shell out 调用系统 git 完成所有操作（不内嵌 git 库），由操作系统调度器（Windows schtasks / macOS launchd / cron）按间隔触发**一次性命令**——程序本身不常驻。

- **简洁**：单二进制 + 一个 YAML 配置；无 Web、无数据库、无守护进程。
- **高效**：1 分钟轮询，状态机驱动，仅在有变更或远程有新提交时动作。
- **无感**：成功同步静默无通知；仅初始化、冲突、失败时弹系统通知。
- **零丢失**：`local_wins` 冲突时把远程旧版本备份到 `backup/remote-*` 分支，可随时恢复。

## 工作原理

每次触发执行一次同步状态机：

```
init(首次) → add -A + commit → fetch → RelationTo(四态) →
  ├─ UpToDate    → 无操作
  ├─ LocalAhead  → push（快进）
  ├─ RemoteAhead → pull --rebase → push
  └─ Diverged    → pull --rebase → 成功则 push；冲突则按 conflict_strategy 处理
```

冲突三策略：

| 策略 | 行为 |
|------|------|
| `local_wins`（默认） | 备份远程旧版本到 `backup/remote-<时间戳>` 分支，`--force-with-lease` 推送本地 |
| `remote_wins` | 放弃本地未推送改动，`reset --hard` 到远程版本 |
| `abort` | 中止同步，不动仓库，提示手动处理（退出码 1） |

`backup/remote-*` 分支自动清理，默认保留最新 10 个（本地 + 远程）。

## 构建与安装

前置：Go 1.26+、系统 git、系统 git 凭证（SSH key 或 credential helper）。

```bash
# Makefile（类 Unix shell / Git Bash）
make build        # Windows 双版本：AutoSync.exe（控制台）+ AutoSync_Silent.exe（静默）
make build-all    # 三平台交叉编译（4 目标）
make test         # 全部测试（test/ 目录，真实 git，无 mock）
make test-race    # 带竞态检测

# Windows PowerShell
.\build.ps1 build
.\build.ps1 test
```

## 配置

复制 `config.example.yaml` 为 `config.yaml`，与二进制放同一目录（可用 `--config` 覆盖路径），按实际填写：

```yaml
repo_dir: "D:\\MySyncFolder"          # 同步目标文件夹（必填）
remote_url: "git@github.com:you/repo.git"  # 远程地址，首次初始化用（必填）
branch: "main"                        # 同步分支（默认 main）
interval: "1m"                        # 轮询间隔（默认 1m，最小粒度 1 分钟）
conflict_strategy: "local_wins"       # local_wins | remote_wins | abort
backup_keep: 10                       # backup 分支保留数
retry_count: 3                        # 网络操作重试次数
retry_base_delay: "1s"                # 重试退避基数（1s/2s/4s 指数）
show_console: false                   # 是否输出到控制台（false = 仅日志文件）
ignore:                               # 自动追加到 repo_dir/.gitignore（仅追加，不覆盖）
  - "*.tmp"
  - "Thumbs.db"
  - "autosync.log"
  - "config.yaml"
```

日志（`autosync.log`）、状态（`autosync.state.json`）、单实例锁（`autosync.lock`）均落在二进制同目录。

## 使用

```bash
# 单次同步（默认子命令）
autosync                      # 用同目录 config.yaml
autosync --config D:\path\to\config.yaml
autosync --dry-run            # 只读预览同步计划，不联网、不改仓库

# 查看上次同步状态
autosync status

# 注册 / 移除系统定时任务（Windows：schtasks，每 interval 触发一次 sync）
autosync install --config D:\path\to\config.yaml
autosync uninstall
```

退出码：失败 / 冲突中止 → 1；其余 → 0。成功同步无系统通知；初始化 / 冲突 / 失败才通知。

## 平台支持

| 平台 | 同步核心 | 调度自安装 |
|------|----------|-----------|
| Windows | ✅ | ✅ schtasks |
| macOS / Linux | ✅ | ⏳ 后续（launchd / cron，当前返回未实现） |

核心同步逻辑跨平台；调度自安装当前仅 Windows 实现，macOS/Linux 可用系统 cron 手动注册 `autosync sync`。

## 项目结构

```
cmd/autosync/      入口：CLI 分发与依赖装配
internal/config    配置加载/默认值/校验
internal/log       分级日志（文件+控制台，并发安全）
internal/gitignore .gitignore 自动维护（追加去重）
internal/gitop     GitOperator 接口 + exec 实现 + 重试装饰器
internal/sync      同步状态机、冲突处理、dry-run、backup 清理
internal/notify    通知策略 + beeep 实现
internal/state     上次同步状态持久化（status 命令用）
internal/lock      单实例锁（PID，跨平台）
internal/sched     Scheduler 接口 + schtasks 实现
test/              全部测试（真实 git 临时仓库，禁止 mock）
docs/              PRD.md / TECH.md / PLAN.md
```

## 非目标（v1.0 不做）

GUI / 托盘、实时文件监听（inotify/FSEvents）、应用内凭证管理、多文件夹、守护进程模式。这些列于 [docs/PLAN.md](docs/PLAN.md) 后续路线。

## 文档

- [docs/PRD.md](docs/PRD.md) — 需求
- [docs/TECH.md](docs/TECH.md) — 系统设计
- [docs/PLAN.md](docs/PLAN.md) — 开发计划与验收
