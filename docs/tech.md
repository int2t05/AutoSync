# TECH · 系统设计

> 解决"怎么建"：架构、模块边界、核心抽象与关键决策。业务流程见 [flow.md](flow.md)，命令接口见 [api.md](api.md)。

## 架构

```mermaid
flowchart TB
  subgraph cmd[cmd/autosync]
    MAIN[main<br/>CLI 分发 + 依赖装配]
  end
  subgraph internal[internal]
    CFG[config<br/>配置默认值与校验]
    LOG[log<br/>分级日志]
    GI[gitignore<br/>追加式维护]
    GOP[gitop<br/>GitOperator + 重试装饰器]
    SYNC[sync<br/>状态机 + 冲突处理 + dry-run]
    NOTIFY[notify<br/>通知策略 + beeep]
    STATE[state<br/>状态持久化]
    LOCK[lock<br/>单实例锁]
    TASKSCHED[tasksched<br/>任务调度]
  end
  MAIN --> CFG & LOG & GI & GOP & SYNC & NOTIFY & STATE & LOCK & TASKSCHED
  SYNC --> GOP
  NOTIFY --> SYNC
  GOP -.shell out.-> GIT[(系统 git)]
```

无子命令时按平台分流为常驻守护（Windows 托盘 / macOS engine / Linux daemon）；`autosync sync` 子命令仍为一次性 CLI（脚本 / 无头）。

## 核心抽象

依赖倒置：Syncer 依赖 `GitOperator` 接口而非具体实现，便于装饰器扩展。

| 接口            | 位置   | 职责                           |
| --------------- | ------ | ------------------------------ |
| `GitOperator` | gitop  | git 操作抽象（读 / 写 / 冲突） |
| `TaskScheduler` | tasksched | 每任务 ticker + 手动触发 |
| `Notifier`    | notify | 系统通知投递                   |

装饰器：`retryGit` 嵌入 `GitOperator`，仅覆盖网络方法（Fetch / Push / PushForce / PushBranch / DeleteRemoteBranch），其余按嵌入委托。

`GitOperator` 除状态机主路径外，还承担失败分类与配置一致性：`RebaseInProgress`（区分 rebase 冲突与网络/钩子失败）、`HasHead`/`CheckoutRemote`/`PushFirst`（空仓库无 HEAD 分支）、`GetRemoteURL`/`CurrentBranch`（配合纯函数 `NormalizeRemoteURL` 做配置↔仓库核对）。

## 关系四态

```mermaid
flowchart LR
  S[RelationTo] --> EQ{HEAD == remote?}
  EQ -->|是| U[UpToDate<br/>无操作]
  EQ -->|否| MB[merge-base]
  MB --> ML{mb == local?}
  ML -->|是| RA[RemoteAhead<br/>pull --rebase]
  ML -->|否| MR{mb == remote?}
  MR -->|是| LA[LocalAhead<br/>push]
  MR -->|否| DV[Diverged<br/>pull --rebase]
```

四态替代布尔"是否分叉"：布尔判定漏掉 RemoteAhead，该态直接 push 会非快进失败。merge-base 失败（本地与远程无共同祖先：远程被 force-push 重写或换仓库）显式报错、不回退 Diverged——避免 local_wins 借 rebase/force push 覆盖无关远程。

## 关键决策

- **`--force-with-lease`**：local_wins 强推用 lease 而非裸 `--force`，fetch 与 push 间远程被改写则拒绝。
- **单实例锁**：`O_EXCL` 创建锁文件写 PID + 进程启动时间；持有者存活且身份一致（启动时间相符）才跳过，已死 / 损坏 / PID 复用则接管。`pidAlive` 跨平台（Unix `kill -0` / Windows `tasklist` 带超时），`processStartTime` 读创建时间（Windows `GetProcessTimes` / Linux `/proc/<pid>/stat` / darwin `sysctl`）。
- **git 命令统一超时**：全部 git 命令带超时（默认 `git_timeout=60s`），防网络挂起冻结调度/退出。超时双保险——`CommandContext` 杀直接进程 + 输出重定向到临时文件而非管道（hook/ssh 等孙子进程继承的是文件句柄而非管道写端，文件不阻塞 `cmd.Wait`，规避 Windows 管道继承挂起）。
- **配置↔仓库一致性**：同步前核对 `git remote get-url` 与配置 `remote_url`（`NormalizeRemoteURL` 归一化比较）、当前分支与配置 `branch`，不一致显式 Failed——改配置不再静默失效，也拒绝在无关远程/分支上做写操作。
- **热重载异步**：`TaskScheduler.Reload` 后台重建（Stop 有界），UI/IPC 线程不冻结；退出等待进行中同步有界。
- **config-save 落盘回滚**：engine 与托盘同构——先快照旧任务列表，`ReplaceAll` 校验替换后 `Save` 落盘，失败即回滚内存态且调度器不 Reload，壳 / 窗口保持原配置。
- **任务重命名迁移**：`Update` 替换成功后 safeName 变化时，重命名 state 文件到新键并 `CleanStale` 清理旧锁（无存活持有者才删）；`ReplaceAll` 全量替换语义不迁移。
- **重试**：纯控制流 `Retry` 指数退避，装饰器仅覆盖网络方法。
- **追加式 .gitignore**：仅追加缺失条目，绝不覆盖用户既有配置。
- **配置宽松加载**：`LoadLenient` 跳过 repo_dir 存在性校验，供 status / install 在仓库未就绪时使用。

## 目录结构

```
cmd/autosync/         入口：CLI 分发与依赖装配
cmd/genicon          图标生成：SVG→PNG（oksvg 光栅化，仅改图标时运行）
internal/config      默认值 / 校验 + byproduct 路径解析（~/.autosync）
internal/log         分级日志（文件 + 控制台，并发安全）
internal/gitignore    .gitignore 追加式维护
internal/gitop        GitOperator 接口 + exec 实现 + 重试装饰器
internal/sync         同步状态机 / 冲突处理 / dry-run / backup 清理
internal/notify       通知策略 + beeep 实现
internal/state        上次同步状态持久化
internal/lock         单实例锁（PID，跨平台）
internal/configstore  多任务配置 Store（autosync.conf.yaml，CRUD + 持久化 + 宽松加载）
internal/tasksched    任务调度：每任务 ticker + TaskRunner 复用 Syncer
internal/tray         托盘守护应用（Fyne，构建标签 traygui 隔离）
internal/autostart    开机自启（Windows 注册表 Run 键 / macOS stub 由壳管 / Linux systemd user service）
internal/assets       嵌入图标资源（icon.svg → icon.png，供托盘/窗口/exe）
internal/engine       engine 子命令 IPC（macOS Swift 壳经 stdin/stdout JSON 调用）
test/                 全部测试（真实 git 临时仓库，禁止 mock）
macos/                Swift MenuBarExtra 原生壳工程（macOS GUI）
```

## byproduct 与路径

所有 byproduct 统一存放于用户主目录下 `~/.autosync/`（可用 `AUTOSYNC_DATA_DIR` 覆盖），使 exe 位置独立（可装进只读目录、可任意位置打开）：

```
~/.autosync/
  autosync.conf.yaml          # 多任务配置（托盘 / daemon / CLI 共用）
  logs/autosync.log           # 日志（CLI + 守护共享）
  state/autosync.state-<name>.json   # 每任务状态
  locks/autosync.lock-<name>         # 每任务锁 / 守护锁 autosync.daemon.lock
```

路径解析集中于 `internal/config/paths.go`（`UserDataDir` / `LogFilePath` / `StateFilePath` / `LockFilePath` 等），`log`/`state`/`lock` 包只接收路径不解析。

## 平台策略

main 分支统一维护三平台（Windows + macOS + Linux），核心逻辑跨平台；平台差异用构建标签隔离（`//go:build windows` / `darwin` / `!windows && !darwin`）。`pidAlive`、`autostart` 按平台分文件实现；`tray` 用 `traygui` 标签隔离 Fyne（Windows）；macOS GUI 由 Swift `MenuBarExtra` 原生壳承担，Go 引擎以 `engine` 子命令作子进程经 stdin/stdout JSON IPC 供壳调用（见 macOS 原生壳架构）；Linux 走 `daemon` 子命令 + systemd user service（无 GUI，对齐 Syncthing/Rclone）。路径用 `filepath`，不硬编码分隔符。

## 托盘守护架构

托盘守护层复用引擎（Syncer / gitop / notify / state / lock），进程内调用，不启子进程。

```mermaid
flowchart TB
  EXE[autosync.exe 无参数] --> APP[TrayApp Fyne]
  APP --> WIN[配置窗口<br/>任务列表 CRUD]
  APP --> ICON[托盘图标 + 菜单]
  APP --> SCHED[TaskScheduler<br/>每任务 ticker]
  SCHED --> RUN[TaskRunner]
  RUN --> SYNC[Syncer 复用]
  SYNC --> GITOP[gitop 复用]
  RUN --> STATE[state 每任务] & NOTIFY[notify 复用]
  AUTO[注册表 Run 键] -->|登录自启| APP
```

- **进程模型**：无参数启动 = 托盘守护（常驻 + 内置 ticker）；`autosync sync` = 一次性 CLI（脚本 / 无头）。单实例锁防多开。
- **多任务**：`autosync.conf.yaml` 的 `tasks: [...]`，每任务独立 state（`autosync.state-<name>.json`）与 lock。
- **GUI**：Fyne（纯 Go，窗口 + 托盘一体，跨平台）。
- **自启**：`install` / `uninstall` 开关开机自启（Windows 注册表 `HKCU\...\Run` 键 / Linux systemd user service / macOS 由壳 SMAppService 管理）。
- **托盘菜单**：各任务手动同步 / 暂停、开机自启开关、打开配置、退出（同步状态经 `autosync status` 查询）。

## macOS 原生壳架构

macOS 走 Swift `MenuBarExtra` 原生壳，Go 引擎以 `engine` 子命令作子进程经 stdin/stdout JSON IPC 供壳调用。引擎核心三平台共享，仅入口与平台层分化。

```mermaid
flowchart TB
  APP[AutoSync.app Swift MenuBarExtra 壳] -->|JSON stdin/stdout| ENG[autosync-engine Go 子进程]
  ENG --> SCHED[TaskScheduler 复用]
  SCHED --> SYNC[Syncer 复用]
  APP --> NOTIFY[UNUserNotificationCenter] & AUTO[SMAppService 登录项] & LOCK[flock 单实例]
```

- **进程模型**：macOS 双进程（壳 + 引擎），壳管 GUI/自启/通知/单实例，引擎管同步调度；Windows 仍单进程 Fyne 托盘。
- **IPC**：JSON 行协议（`internal/engine/protocol.go`），命令 status/sync-now/pause/resume/config-list/config-save/quit，事件 ready/status/sync-result/notify/bye 等。
- **事件写出**：有界队列 + 单写者 goroutine——壳不读 stdout（管道写满）时事件被丢弃而非阻塞引擎循环（防双向死锁）；quit 时先停调度器再冲刷 bye。
- **依赖倒置**：`tasksched` 的 Notifier 与 onResult 回调由调用方注入（Windows beeep / macOS IPC 委托壳）。
- **分发**：未签名 DMG + `xattr` 文档（起步），后续可升级 Developer ID 公证。

## 测试策略

- 全部测试在 `test/` 目录，包名 `tests`。
- 真实 git 临时仓库 + 裸远程驱动状态机，禁止 mock / fake / stub。
- `TestMain` 设置 git 提交身份，不依赖全局配置。
- 纯函数（Retry / BuildInstallArgs / PolicyFor）单测；状态机用真实仓库集成测。
