# TECH.md: AutoSync 系统设计

> 配套文档：[PRD.md](PRD.md)（需求）。本文档解决"怎么建"——系统架构、模块边界、领域模型、状态机、接口契约与实现顺序。

## 1. 概述

AutoSync 是基于系统 git、用 Go 编写的跨平台文件夹双向同步工具。本文档以系统设计思想组织：分层架构、依赖倒置、显式领域模型、状态机驱动的同步引擎、平台抽象与可测试性。

**设计原则**

- **简洁**：一次性命令 + OS 调度器，无守护进程；最小依赖（系统 git + 2 个 Go 库）
- **可测试**：核心逻辑通过 interface 解耦，状态机可脱离真实 git 单测
- **跨平台**：平台差异隔离在叶子模块，核心逻辑纯 Go
- **无感**：成功静默，仅冲突/失败通知

## 2. 设计决策汇总

| 维度 | 决策 |
|------|------|
| Git 实现 | shell out（exec.Command 调系统 git）|
| 代码组织 | 分层多包 `internal/*` + `cmd/autosync` |
| 测试策略 | 单测 + git 集成测试（临时真实仓库）|
| 失败重试 | 运行内退避重试 3 次后通知 |
| 触发间隔 | 默认 1 分钟（OS 调度器）|
| 冲突默认 | local_wins（备份远程，可配置）|
| backup 清理 | 自动，保留近 N=10 个 |
| 平台范围 | Windows 优先，接口跨平台，macOS/Linux 后续 |
| 交互 | CLI + 系统通知，成功静默 |
| dry-run | 纳入（`sync --dry-run`）|

## 3. 系统架构

### 3.1 分层与依赖

```
cmd/autosync            main：CLI 分发、依赖装配（DI）
    │
    ├── internal/config   Config：加载/校验/默认值
    ├── internal/log      Logger：分级日志（文件+可选控制台）
    ├── internal/state    StateStore：上次同步状态（status 命令用）
    ├── internal/gitop    GitOperator 接口 + execGit 实现 + fake（测试）
    ├── internal/sync     Syncer：同步状态机 + 领域模型 + dryRun 装饰器
    ├── internal/notify   Notifier 接口 + beeep 实现
    └── internal/sched    Scheduler 接口 + 平台实现（schtasks/launchd/cron）
```

**依赖方向**：`sync` 依赖 `gitop`/`notify` 的**接口**而非实现；`cmd` 在启动时装配具体实现（依赖倒置）。平台代码（notify/sched 的实现）用构建标签隔离，不污染核心。

### 3.2 运行模型

- 二进制为**一次性命令**：`autosync sync` 跑一次同步即退出
- 定时由 OS 调度器承担：`autosync install` 注册 schtasks/launchd/cron，按 `interval` 触发 `autosync sync`
- 无守护进程、无内部 ticker；崩溃不影响下次 OS 触发

### 3.3 运行时目录布局

```
<autosync-home>/          # 二进制所在目录（用户自选）
├── autosync(.exe)         # 二进制
├── config.yaml            # 配置
├── autosync.log           # 日志
└── autosync.state.json    # 上次同步状态（status 用）

<repo_dir>/                # 被同步的文件夹（独立路径）
└── .gitignore             # 工具自动维护 ignore 条目
```

工具制品（config/log/state/二进制）若落在 `repo_dir` 内，须被 `.gitignore` 忽略——由工具自动保障。

## 4. 模块设计

| 包 | 职责 | 关键类型 | 依赖 |
|----|------|----------|------|
| `cmd/autosync` | CLI 分发、装配、退出码 | `main` | 所有 internal |
| `internal/config` | 加载 YAML、校验、默认值 | `Config` | yaml.v3 |
| `internal/log` | 分级日志、并发安全 | `Logger` | stdlib |
| `internal/state` | 读写上次同步状态 JSON | `StateStore`, `State` | stdlib |
| `internal/gitop` | GitOperator 接口、exec 实现、测试 fake | `GitOperator`, `execGit`, `fakeGit` | stdlib |
| `internal/sync` | 同步状态机、冲突处理、backup 清理、dry-run | `Syncer`, `SyncResult`, `Outcome` | gitop, log |
| `internal/notify` | Notifier 接口、beeep 实现、通知策略 | `Notifier`, `beeepNotifier` | beeep |
| `internal/sched` | Scheduler 接口、平台 install/uninstall | `Scheduler` | stdlib + 平台 |

## 5. 核心领域模型

```go
// 冲突策略
type ConflictStrategy string
const (
    StrategyLocalWins  ConflictStrategy = "local_wins"
    StrategyRemoteWins ConflictStrategy = "remote_wins"
    StrategyAbort      ConflictStrategy = "abort"
)

// 同步结果枚举（驱动通知策略与状态记录）
type Outcome int
const (
    OutcomeInitDone         // 首次初始化完成
    OutcomeNoChanges        // 无变更且已是最新
    OutcomePushed           // 直接推送成功（含新建远程分支）
    OutcomeAutoMerged       // rebase 自动合并成功
    OutcomeConflictResolved // 冲突已按策略解决
    OutcomeConflictAborted  // abort 策略，未处理
    OutcomeFailed           // 错误
)

// 单次同步结果
type SyncResult struct {
    Outcome     Outcome
    Message     string   // 摘要
    Details     string   // 细节（如备份分支名）
    BackupBranch string   // local_wins 时填充
    Err         error
}

// 持久化状态（status 命令 + 抑制重复失败通知）
type State struct {
    LastSyncAt   time.Time
    LastOutcome  Outcome
    LastMessage  string
    ConsecutiveFailures int  // 可选：抑制重复通知
}
```

**通知策略映射**：`NoChanges/Pushed/AutoMerged` → 静默；`InitDone` → 信息通知；`ConflictResolved` → 警告通知（含备份分支）；`ConflictAborted/Failed` → 错误通知。

## 6. 同步状态机

```
S0 Start
 │
 ├─ S1 EnsureRepo ──非仓库──► S2 Init ─────────────────► END(InitDone)
 │   │仓库
 ├─ S2 StageAll + Commit(若有变更)
 │
 ├─ S3 Fetch ──(重试3次仍失败)──► END(Failed)
 │
 ├─ S4 RemoteBranchExists?
 │     ├─ 否 ──► S9 Push ──► END(Pushed)
 │     └─ 是
 ├─ S5 RelationTo（本地 vs 远程四态）
 │     ├─ UpToDate ──────────────────────────► END(NoChanges)
 │     ├─ LocalAhead ──► S9 Push ────────────► END(Pushed)
 │     └─ RemoteAhead / Diverged
 ├─ S6 PullRebase
 │     ├─ 成功 ──► S9 Push ──► END(AutoMerged)
 │     └─ 失败 ──► S7 RebaseAbort ──► S8 ResolveConflict
 ├─ S8 ResolveConflict(strategy)
 │     ├─ local_wins  ──► S8a 创建+推送备份分支 ──► S8b PushForce ──► S8c Cleanup ──► END(ConflictResolved)
 │     ├─ remote_wins ──► S8d ResetHard+Clean ─────────────────────────► END(ConflictResolved)
 │     └─ abort       ─────────────────────────────────────────────────► END(ConflictAborted)
 └─ END：写状态、按 Outcome 通知、释放锁
```

| 转移 | 触发 | 下一状态 |
|------|------|----------|
| S1→S2 | 无 `.git` | Init |
| S1→S2 | 已是仓库 | StageAll |
| S3→END | fetch 重试失败 | Failed |
| S4→S9 | 远程分支不存在 | Push |
| S5→END | UpToDate | NoChanges |
| S5→S9 | LocalAhead | Push |
| S5→S6 | RemoteAhead/Diverged | PullRebase |
| S6→S9 | rebase 成功 | Push |
| S6→S7 | rebase 失败 | RebaseAbort→ResolveConflict |
| S8→END | 策略执行完 | ConflictResolved / Aborted |

## 7. 关键接口契约

```go
// internal/gitop —— 同步引擎唯一依赖的 git 抽象（依赖倒置 + 可测试）
type GitOperator interface {
    IsRepo() bool
    Init(remote, remoteURL, branch string) error
    StageAll() error
    HasChanges() (bool, error)
    Commit(msg string) error
    Fetch(remote string) error
    RemoteBranchExists(remote, branch string) (bool, error)
    RelationTo(remote, branch string) (Relation, error)  // 四态：UpToDate/LocalAhead/RemoteAhead/Diverged
    PullRebase(remote, branch string) error
    RebaseAbort() error
    Push(remote, branch string) error
    // —— 以下为 P3 冲突处理扩展 ——
    PushForce(remote, branch string) error          // 用 --force-with-lease（比 --force 安全）
    CreateBackupBranch(remote, branch, backupName string) error
    PushBranch(remote, branchName string) error
    DeleteRemoteBranch(remote, branchName string) error
    DeleteLocalBranch(branchName string) error
    ListBackupBranches(remote string) ([]string, error)  // 匹配 backup/remote-*
    ResetHardToRemote(remote, branch string) error       // reset --hard + clean -fd
}

// internal/notify
type Notifier interface {
    Notify(title, body string, severity Severity) error
}

// internal/sched
type Scheduler interface {
    Install(binPath string, cfg Config) error
    Uninstall() error
    Status() (installed bool, nextRun time.Time, err error)
}
```

**实现**：
- `execGit`：shell out 系统 git，统一设 `GIT_TERMINAL_PROMPT=0`、`GIT_MERGE_AUTOEDIT=no`，捕获合并输出
- `dryRunGit`（P4）：装饰器，拦截写操作（Commit/Push/Rebase/Reset/Branch...）改为记录，读操作放行
- 注：按项目规则禁止 mock/fake 测试桩，测试统一用真实 git 临时仓库驱动（见 §12）。`.gitignore` 维护已独立为 `internal/gitignore` 包，不在 GitOperator。

## 8. 横切关注点

### 8.1 单实例锁（1 分钟间隔防重叠）

`autosync-home/autosync.lock`，用 `O_CREATE|O_EXCL` 创建 + 写入 PID。
- 获取失败：读 PID，进程存活则**静默跳过本次**（上一个 tick 未跑完）；PID 已死或文件超时（如 10 分钟）则接管
- 跨平台：不依赖 flock，仅用 `O_EXCL` + PID 存活检测

### 8.2 失败重试

仅对网络类操作（`Fetch`、`Push`、`PushForce`、`PushBranch`、`DeleteRemoteBranch`）重试：3 次，指数退避（1s/2s/4s）。重试耗尽才判定失败并通知。非网络失败（rebase 冲突、配置错误）不重试。

### 8.3 backup 分支自动清理

- 触发：每次 `local_wins` 解决冲突后执行（仅在有新备份时清理，避免无谓网络操作）
- 逻辑：`ListBackupBranches` → 按名称内时间戳排序 → 保留最新 N（默认 10）→ 删除其余（本地 + 远程）
- `backup_keep` 可配置

### 8.4 dry-run

`autosync sync --dry-run`：执行只读分析（status / rev-parse / merge-base），**跳过 fetch 与所有写操作**，输出计划（将提交哪些变更、是否分叉、会触发哪种冲突策略、将备份到哪个分支）。对 force-push 工具提供预览。由 `dryRunGit` 装饰器实现。

### 8.5 错误处理

- 所有 git 失败捕获 stdout/stderr → 日志 + 上传 `SyncResult.Err`
- `rebase --abort` 后须确保无残留冲突状态
- 网络失败不触发 force push

## 9. 平台抽象

| 模块 | Windows（MVP） | macOS | Linux |
|------|----------------|-------|-------|
| notify | beeep（toast） | beeep | beeep |
| sched | schtasks `/SC MINUTE /MO N` | launchd plist（后续） | cron/systemd-timer（后续） |

- 文件组织：`sched_windows.go` / `sched_darwin.go` / `sched_linux.go`，构建标签隔离
- MVP：macOS/Linux 的 `Scheduler.Install` 返回 `ErrNotImplemented`，但 `go build` 须通过
- notify 由 beeep 统一处理，无需平台分文件

## 10. 配置模型

```yaml
# config.yaml（与二进制同目录）
repo_dir: "D:\\MySyncFolder"          # 必填
remote_url: "git@github.com:u/r.git"  # 必填
remote: "origin"                      # 默认 origin
branch: "main"                        # 默认 main
interval: "1m"                        # 默认 1m，OS 调度器间隔
conflict_strategy: "local_wins"       # local_wins | remote_wins | abort
backup_keep: 10                       # backup 分支保留数，默认 10
retry_count: 3                        # 网络操作重试次数，默认 3
retry_base_delay: "1s"                # 重试退避基数，默认 1s
commit_msg_format: "auto sync: {{.Timestamp}}"  # Go 模板
log_file: "autosync.log"
state_file: "autosync.state.json"
show_console: false
ignore:                               # 自动写入 repo_dir/.gitignore
  - "*.tmp"
  - "Thumbs.db"
  - "desktop.ini"
  - ".DS_Store"
  - "autosync.log"
  - "autosync.state.json"
  - "config.yaml"
```

**校验**：必填项缺失、目录不存在、策略非法、interval 解析失败 → 启动即报错退出码 1。

## 11. 构建与发布

```bash
# Windows（带控制台）
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o AutoSync.exe ./cmd/autosync
# Windows（静默，无窗口）
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w -H windowsgui" -o AutoSync_Silent.exe ./cmd/autosync
# macOS / Linux 编译验证
GOOS=darwin  GOARCH=amd64 go build -o autosync ./cmd/autosync
GOOS=linux   GOARCH=amd64 go build -o autosync ./cmd/autosync
```

`Makefile` / `build.ps1`：`make build`（Windows 双版本）、`make build-all`（三平台交叉编译）、`make test`。版本号通过 `-ldflags "-X main.version=..."` 注入。

## 12. 测试策略

| 层级 | 范围 | 方式 |
|------|------|------|
| 单测 | 纯函数 | 配置解析、日志、.gitignore 维护等，基于真实临时文件 |
| 集成 | 状态机主路径 | 临时目录初始化真实 git 仓库 + 本地裸 remote，跑 `execGit` + `Syncer`，断言仓库状态与 Outcome |
| 集成场景 | 冲突构造（P3） | 两侧分别提交冲突文件，验证 local_wins 备份分支可恢复、remote_wins 丢弃本地 |

**禁止 mock**：按项目规则不使用任何 mock/fake/stub 测试桩；所有测试基于真实数据（真实 git 仓库、真实文件）。测试代码统一在 `test/` 目录，黑盒测试导出 API。

覆盖目标：状态机所有 Outcome 至少一条用例；冲突三策略各一条；cleanup 保留 N 条；retry 在前 N-1 次失败后成功。

## 13. 项目结构

```
autosync/
├── cmd/autosync/main.go            # CLI 分发 + 装配
├── internal/
│   ├── config/config.go            # Config + Load + Validate
│   ├── log/log.go                  # Logger
│   ├── state/state.go              # StateStore + State
│   ├── gitignore/gitignore.go      # .gitignore 自动维护（纯文件 I/O，追加去重）
│   ├── gitop/gitop.go              # GitOperator 接口 + execGit 实现 + Relation
│   ├── sync/
│   │   ├── syncer.go               # 状态机引擎
│   │   ├── conflict.go             # 冲突处理 + backup 清理（P3）
│   │   └── result.go               # Outcome / SyncResult
│   ├── notify/
│   │   ├── notify.go               # Notifier 接口 + 策略映射
│   │   └── beeep.go                # beeep 实现
│   └── sched/
│       ├── sched.go                # Scheduler 接口
│       ├── sched_windows.go        # schtasks
│       ├── sched_darwin.go         # launchd（后续）
│       └── sched_linux.go          # cron（后续）
├── test/                           # 所有测试（真实数据，禁止 mock）
│   ├── main_test.go                # TestMain：git 身份环境
│   ├── git_helper_test.go          # 真实 git 仓库夹具
│   ├── config_test.go / log_test.go / gitignore_test.go
│   └── sync_test.go                # 同步状态机集成测试
├── config.example.yaml
├── go.mod / go.sum
├── Makefile / build.ps1
├── docs/PRD.md / docs/TECH.md
└── README.md
```

## 14. 实现顺序（里程碑）

| 里程碑 | 内容 | 验证 |
|--------|------|------|
| M0 | 骨架 + config + log + 项目结构 | `go build` 通过；config 校验单测 |
| M1 | gitop 接口 + execGit + 集成测试夹具（临时仓库） | 对临时仓库跑通 init/add/commit/fetch/push |
| M2 | Syncer 状态机（S1→S5→S9 主路径） | 集成：本地变更→push；无变更→NoChanges |
| M3 | 分叉 + rebase + 三种冲突策略 + backup 清理 | 集成：构造冲突，验证三策略与备份可恢复 |
| M4 | notify + 通知策略映射 | 各 Outcome 触发正确通知级别 |
| M5 | sched（Windows schtasks install/uninstall） | install 后 schtasks 可见，uninstall 后移除 |
| M6 | state 文件 + status 命令 | status 输出上次同步结果 |
| M7 | 单实例锁 + 重试 + dry-run | 锁防重叠；重试单测；dry-run 输出计划 |
| M8 | 交叉编译 + Makefile + README | 三平台 `go build` 通过 |

## 15. 风险与权衡

- **local_wins + 多设备并发**：最后同步者覆盖，旧版本散落 backup 分支（可恢复）。真并发编辑场景建议改 `abort`。已纳入 PRD 已知权衡。
- **`--force-with-lease`**：优于 `--force`，防止 fetch 与 push 之间远程被他人改写。但 lease 失败会转为失败通知，需用户感知。
- **1 分钟间隔 + 重试**：单次运行含重试最坏约 7s+操作时间，远小于 1 分钟，不会跨越下个 tick；锁兜底防重叠。
- **持久网络中断**：重试耗尽后每分钟通知一次（用户选择"运行内重试后通知"）。如需降噪，后续可加"连续 N 次失败才通知"（state 文件已预留 `ConsecutiveFailures` 字段）。
- **schtasks 最小粒度 1 分钟**：与默认 interval 一致；若需亚分钟级，须改内置 daemon（非目标）。
- **macOS/Linux 自安装未实现**：MVP 返回 `ErrNotImplemented`；用户可手动配 launchd/cron 调用 `autosync sync`。
