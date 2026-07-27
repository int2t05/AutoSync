# TECH · 系统设计

> 解决"怎么建"：架构、模块边界、核心抽象与关键决策。业务流程见 [flow.md](flow.md)，命令接口见 [api.md](api.md)。

## 架构

```mermaid
flowchart TB
  subgraph cmd[cmd/autosync]
    MAIN[main<br/>CLI 分发 + 依赖装配]
  end
  subgraph internal[internal]
    CFG[config<br/>配置加载校验]
    LOG[log<br/>分级日志]
    GI[gitignore<br/>追加式维护]
    GOP[gitop<br/>GitOperator + 重试装饰器]
    SYNC[sync<br/>状态机 + 冲突处理 + dry-run]
    NOTIFY[notify<br/>通知策略 + beeep]
    STATE[state<br/>状态持久化]
    LOCK[lock<br/>单实例锁]
    SCHED[sched<br/>调度自安装]
  end
  MAIN --> CFG & LOG & GI & GOP & SYNC & NOTIFY & STATE & LOCK & SCHED
  SYNC --> GOP
  NOTIFY --> SYNC
  GOP -.shell out.-> GIT[(系统 git)]
```

程序不常驻。系统调度器按间隔触发 `autosync sync`，main 装配依赖后由 Syncer 执行一次状态机，结束即退出。

## 核心抽象

依赖倒置：Syncer 依赖 `GitOperator` 接口而非具体实现，便于装饰器扩展。

| 接口            | 位置   | 职责                           |
| --------------- | ------ | ------------------------------ |
| `GitOperator` | gitop  | git 操作抽象（读 / 写 / 冲突） |
| `Scheduler`   | sched  | 定时任务注册 / 移除            |
| `Notifier`    | notify | 系统通知投递                   |

装饰器：`retryGit` 嵌入 `GitOperator`，仅覆盖网络方法（Fetch / Push / PushForce / PushBranch / DeleteRemoteBranch），其余按嵌入委托。

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

四态替代布尔"是否分叉"：布尔判定漏掉 RemoteAhead，该态直接 push 会非快进失败。

## 关键决策

- **`--force-with-lease`**：local_wins 强推用 lease 而非裸 `--force`，fetch 与 push 间远程被改写则拒绝。
- **单实例锁**：`O_EXCL` 创建锁文件写 PID；存活进程持有则跳过，已死 / 损坏则接管。`pidAlive` 跨平台（Unix `kill -0` / Windows `tasklist`）。
- **重试**：纯控制流 `Retry` 指数退避，装饰器仅覆盖网络方法。
- **追加式 .gitignore**：仅追加缺失条目，绝不覆盖用户既有配置。
- **配置宽松加载**：`LoadLenient` 跳过 repo_dir 存在性校验，供 status / install 在仓库未就绪时使用。

## 目录结构

```
cmd/autosync/      入口：CLI 分发与依赖装配
internal/config    配置加载 / 默认值 / 校验
internal/log       分级日志（文件 + 控制台，并发安全）
internal/gitignore .gitignore 追加式维护
internal/gitop     GitOperator 接口 + exec 实现 + 重试装饰器
internal/sync      同步状态机 / 冲突处理 / dry-run / backup 清理
internal/notify    通知策略 + beeep 实现
internal/state     上次同步状态持久化
internal/lock      单实例锁（PID，跨平台）
internal/sched     Scheduler 接口 + schtasks 实现
test/              全部测试（真实 git 临时仓库，禁止 mock）
```

## 平台策略

核心逻辑跨平台；平台差异用构建标签隔离（`//go:build windows` / `!windows`）。`pidAlive`、`Scheduler` 按平台分文件实现。路径用 `filepath`，不硬编码分隔符。

## V1.1 架构：托盘守护

V1.1 在 V1.0 引擎之上加托盘守护层，引擎（Syncer / gitop / notify / state / lock）进程内复用，不启子进程。

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
- **多任务**：`autosync.conf.yaml` 的 `tasks: [...]`，每任务独立 state（`autosync.state-<name>.json`）与 lock。旧单配置无 `tasks` 视为单任务（向后兼容）。
- **GUI**：Fyne（纯 Go，窗口 + 托盘一体，跨平台）。
- **自启**：`install` / `uninstall` 改为注册表 `HKCU\...\Run` 键开关（替代 schtasks）。非 Windows 留 stub。
- **托盘菜单**：各任务手动同步 / 暂停、打开配置、状态、开机自启开关、退出。

## 测试策略

- 全部测试在 `test/` 目录，包名 `tests`。
- 真实 git 临时仓库 + 裸远程驱动状态机，禁止 mock / fake / stub。
- `TestMain` 设置 git 提交身份，不依赖全局配置。
- 纯函数（Retry / BuildInstallArgs / PolicyFor）单测；状态机用真实仓库集成测。
