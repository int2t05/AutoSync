# FLOW · 业务流程

> 各业务流程的 mermaid 图与数据流描述。架构与接口见 [tech.md](tech.md)，命令见 [api.md](api.md)。

## 同步主状态机

```mermaid
flowchart TD
  START([触发 sync]) --> LOCK{获取单实例锁?}
  LOCK -->|否| SKIP([静默跳过])
  LOCK -->|是| ISREPO{是仓库?}
  ISREPO -->|否| INIT[init + 首次提交 + push -u]
  INIT --> DONE1([InitDone])
  ISREPO -->|是| ADD[add -A]
  ADD --> CHG{有变更?}
  CHG -->|是| COMMIT[commit]
  CHG -->|否| NOCOMMIT[跳过提交]
  COMMIT --> FETCH[fetch]
  NOCOMMIT --> FETCH
  FETCH --> EXIST{远程分支存在?}
  EXIST -->|否| PUSHDIRECT[push 新建远程分支]
  PUSHDIRECT --> DONE2([Pushed])
  EXIST -->|是| REL[RelationTo 四态]
  REL --> U[UpToDate]
  REL --> LA[LocalAhead]
  REL --> RA[RemoteAhead/Diverged]
  U --> DONE3([NoChanges])
  LA --> PUSHPUSH[push 快进]
  PUSHPUSH --> DONE4([Pushed])
  RA --> REBASE[pull --rebase]
  REBASE --> OK{成功?}
  OK -->|是| PUSHMERGE[push]
  PUSHMERGE --> DONE5([AutoMerged])
  OK -->|否| ABORT[rebase --abort]
  ABORT --> CONFLICT[冲突处理]
  CONFLICT --> DONE6([ConflictResolved])
```

**数据流**

1. **加锁**：`O_EXCL` 创建 `autosync.lock` 写 PID；失败则读 PID 判断持有进程存活，存活跳过，已死接管。
2. **初始化**：`git init -b <branch>` → `remote add` → `add -A` → `commit --allow-empty` → `push -u`。
3. **提交**：`git add -A` → `git status --porcelain` 判变更 → `git commit -m "<模板>"`。
4. **拉取引用**：`git fetch <remote>`（重试装饰器包裹）。
5. **关系判定**：`rev-parse HEAD` 与 `<remote>/<branch>` 比较；不等则 `merge-base` 定四态。
6. **合并**：`git pull --rebase <remote> <branch>`；失败 `git rebase --abort` 后转冲突处理。
7. **推送**：`git push` 或 `git push --force-with-lease`（重试包裹）。
8. **收尾**：写 `autosync.state.json` → 按结果通知 → 释放锁。

## 冲突处理

```mermaid
flowchart TD
  C[rebase 冲突] --> AB[rebase --abort]
  AB --> S{conflict_strategy}
  S -->|local_wins| LW[建备份分支 backup/remote-ts<br/>指向 remote/<branch>]
  LW --> LW2[push 备份分支]
  LW2 --> LW3[push --force-with-lease 本地]
  LW3 --> LW4[清理旧备份分支]
  LW4 --> R1([ConflictResolved])
  S -->|remote_wins| RW[reset --hard remote/<branch>]
  RW --> RW2[clean -fd]
  RW2 --> R2([ConflictResolved])
  S -->|conflict_files| CF[读本地差异文件到内存<br/>ResetHardToRemote<br/>写 .sync-conflict-ts 副本<br/>add + commit + push]
  CF --> R3([ConflictResolved])
```

**数据流**

- **local_wins**：`git branch backup/remote-<ts> <remote>/<branch>` → `git push <remote> backup/remote-<ts>` → `git push --force-with-lease <remote> <branch>` → 列 `backup/remote-*` 按名降序留最新 `backup_keep` 个，余下本地 `-D` + 远程 `--delete`。远程旧版本保存在备份分支可 checkout 恢复。
- **remote_wins**：`git reset --hard <remote>/<branch>` → `git clean -fd`。本地未推送改动丢弃。
- **conflict_files**：`git diff --name-only --diff-filter=MD HEAD <remote>/<branch>` 列差异文件 → `os.ReadFile` 读本地版到内存 → `git reset --hard <remote>/<branch>` + `git clean -fd`（副本未写入，不受影响）→ 写 `<file>.sync-conflict-<ts>.<ext>` 副本 → `git add -A` + `commit` + `push`。本地版以副本入 git 同步所有设备，远程版为主文件。

## dry-run

```mermaid
flowchart TD
  D([dry-run]) --> IR{是仓库?}
  IR -->|否| S1[计划: 将初始化]
  IR -->|是| HC[HasChanges?]
  HC -->|是| S2[计划: 将提交]
  HC -->|否| S3[计划: 无新变更]
  S2 --> EX[远程分支存在?]
  S3 --> EX
  EX -->|否| S4[计划: 将 push 新建]
  EX -->|是| RL[RelationTo]
  RL --> S5[计划: 按四态/策略说明]
```

**数据流**：仅 `status --porcelain` / `rev-parse` / `merge-base` 等读命令，跳过 `fetch` 与所有写操作。基于本地陈旧远程引用判定，可能误报 UpToDate（见 TODO）。不写状态、不加锁、不通知。

## 备份清理

`ListBackupBranches` 收集本地 `refs/heads/backup/remote-*` 与远程 `refs/remotes/<remote>/backup/remote-*`，去远程前缀合并去重；按分支名内时间戳降序排序，保留前 `backup_keep` 个，其余 `branch -D`（本地）+ `push --delete`（远程）。

## 托盘守护流程

```mermaid
flowchart TD
  START([双击 / 自启]) --> LOCK{单实例锁?}
  LOCK -->|否| EXIT([已运行，退出])
  LOCK -->|是| LOAD[加载 autosync.conf.yaml<br/>多任务]
  LOAD --> TRAY[启动托盘 + 配置窗口]
  TRAY --> SCHED[TaskScheduler 每任务 ticker]
  SCHED --> TICK{每任务 interval 到}
  TICK --> RUN[TaskRunner 执行该任务<br/>复用 sync 状态机]
  RUN --> UPDATE[更新该任务 state + 托盘状态]
  TRAY -->|右键手动| RUNM[立即执行指定任务]
  TRAY -->|编辑| SAVE[ConfigStore 持久化 + 热重载]
```

**数据流**

- **启动**：`autosync.conf.yaml` 加载任务列表 → 每任务按 interval 起 ticker → 到点调 TaskRunner → TaskRunner 构造 Syncer 执行同步状态机（见上）→ 结果写该任务 state + 通知 + 回显托盘。
- **手动同步**：托盘菜单选任务 → 立即调 TaskRunner（与定时同路径，单实例锁保护单仓库不并发）。
- **配置变更**：窗口编辑 → 保存 `autosync.conf.yaml` → 热重载该任务 ticker（无需重启）。
- **自启**：注册表 Run 键 → 登录后系统拉起 `autosync`（无参数）→ 托盘守护。
