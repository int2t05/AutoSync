# PRD: AutoSync — 跨平台 Git 双向同步工具

## 1. 概述 (Overview)

AutoSync 是一个基于 Git 命令、用 Go 编写的跨平台文件夹同步工具。它将指定本地文件夹与 Git 远程仓库（如 GitHub）保持双向一致：本地改动自动提交并推送，远程/其他设备的改动自动拉取合并。面向"配置一次后完全无感"的个人多设备同步场景。

核心定位：**简洁、高效、无感**。不追求企业级协作能力，而是把"一个文件夹在多台设备间自动同步"这件事做到极简可靠。

**本轮已确认的设计基线：**

| 维度     | 决策                                                                   |
| -------- | ---------------------------------------------------------------------- |
| 触发方式 | 定时轮询（默认每 5 分钟，可配置）                                      |
| 冲突策略 | 本地优先·备份远程（默认；三种策略皆可配置）                           |
| 平台范围 | Windows 优先，架构层跨平台（选用跨平台库，macOS/Linux 后续低成本扩展） |
| 交互形态 | CLI 核心 + 系统通知（无 GUI、无托盘；日常成功静默，仅冲突/错误时通知） |

## 2. 目标 (Goals)

- 本地变更在默认 5 分钟内自动出现在远程，全过程无需用户手动操作
- 远程/其他设备变更自动拉取并与本地合并（双向）
- 冲突发生时数据零丢失：远程旧版本自动备份到分支，可恢复
- 单次同步在常见规模（< 10k 文件）下完成时间 < 5 秒
- 日常成功同步"零打扰"：无弹窗、无通知，仅写日志
- Windows 可用、macOS/Linux 可编译运行（架构不欠技术债）
- 单二进制交付，除系统已安装的 git 外零运行时依赖

## 3. 非目标 (Non-Goals)

- 不做多人协作工作流（PR、code review、分支策略）——用正常 git 工作流
- 不做 GUI 界面 / 系统托盘（MVP 阶段；列为后续增强）
- 不做实时文件监听（inotify/fswatch）——MVP 用定时轮询
- 不做应用内认证管理（不存储/输入 SSH 密钥或 token）——依赖系统已配置的 git 凭证
- 不做多文件夹同步（MVP 单文件夹；多文件夹通过多实例配置实现）
- 不做大文件 / Git LFS 管理（仅靠 .gitignore 排除）
- 不做内置长驻守护进程（MVP 为一次性命令 + OS 调度器）

## 4. 用户故事 (User Stories)

### US-001: 加载与校验配置

**Description:** 作为用户，我希望程序从 config.yaml 读取配置并在启动时校验，以便错误配置能立即暴露而非运行中失败。

**Acceptance Criteria:**

- [ ] 读取与二进制同目录的 config.yaml（支持 `--config` 覆盖路径）
- [ ] 缺失必填项（repo_dir / remote_url）时输出明确错误并退出码 1
- [ ] repo_dir 不存在时输出明确错误并退出码 1
- [ ] 可选项提供合理默认值（remote=origin, branch=main, interval=5m, conflict_strategy=local_wins）
- [ ] go build 通过，go vet 无警告

### US-002: 首次运行初始化仓库

**Description:** 作为用户，我希望对未纳入 git 的文件夹首次运行时自动初始化并首次推送，以便零手动配置即可开始同步。

**Acceptance Criteria:**

- [ ] 检测 repo_dir 下无 .git 时执行：init → remote add → add -A → commit "init: first sync" → push -u
- [ ] 已是 git 仓库时跳过初始化
- [ ] 初始化失败时输出明确错误并退出码 1
- [ ] go build 通过

### US-003: 本地变更检测与自动提交

**Description:** 作为用户，我希望本地任何变更都被自动暂存并提交，以便每次同步都有可追溯的提交记录。

**Acceptance Criteria:**

- [ ] 执行 git add -A
- [ ] 用 git status --porcelain 判断是否有变更；无变更则跳过提交
- [ ] 有变更时提交，消息格式 "auto sync: YYYY-MM-DD HH:MM:SS"（格式可配置）
- [ ] go build 通过

### US-004: 拉取远程并检测分叉

**Description:** 作为同步器，我需要 fetch 远程并判断本地与远程是否分叉，以便决定直接推送还是需要合并。

**Acceptance Criteria:**

- [ ] 执行 git fetch <remote></remote>；网络失败时输出明确错误并退出码 1
- [ ] 远程分支不存在时：直接 push 并结束（视为新建远程分支）
- [ ] 远程分支存在时，用 merge-base 判断是否分叉（merge-base 既不等于 local HEAD 也不等于 remote HEAD 即为分叉）
- [ ] go build 通过

### US-005: 自动 rebase 合并

**Description:** 作为同步器，我希望分叉时优先尝试 rebase 自动合并，以便多数情况无需冲突处理即可双向合并。

**Acceptance Criteria:**

- [ ] 分叉时执行 git pull --rebase <remote></remote> <branch></branch>
- [ ] rebase 成功后执行 push 并结束，日志记录"自动合并成功"
- [ ] rebase 失败时执行 git rebase --abort，转入冲突处理流程
- [ ] go build 通过

### US-006: 冲突处理（本地优先·备份远程）

**Description:** 作为用户，我希望 rebase 失败时默认以本地为准，但远程旧版本被备份到分支，以便数据可恢复。

**Acceptance Criteria:**

- [ ] local_wins：从 remote/<branch></branch> 创建备份分支 backup/remote-<时间戳> 并推送到远程，随后 git push --force 本地版本
- [ ] remote_wins：git reset --hard <remote></remote>/<branch></branch> + git clean -fd，放弃本地未推送改动
- [ ] abort：不做任何变更，记日志 + 系统通知，退出码非零
- [ ] 策略由 config.yaml 的 conflict_strategy 字段控制，默认 local_wins
- [ ] local_wins 完成后，通知中包含备份分支名以便恢复
- [ ] go build 通过

### US-007: 推送与完成

**Description:** 作为同步器，我需要在合并/无冲突后把本地状态推送到远程，以便其他设备能拉取到最新。

**Acceptance Criteria:**

- [ ] 无分叉时直接 git push <remote></remote> <branch></branch>
- [ ] 推送失败时输出明确错误并退出码 1
- [ ] 全流程在日志中记录开始与结束
- [ ] go build 通过

### US-008: 跨平台系统通知

**Description:** 作为用户，我希望仅在冲突/错误等需要关注时收到系统通知，以便日常同步完全无感。

**Acceptance Criteria:**

- [ ] 日常成功同步：无通知（仅日志）
- [ ] 冲突已处理（local_wins/remote_wins）：发送系统通知，含结果摘要
- [ ] 同步失败（网络/推送失败等）：发送系统通知，含错误摘要
- [ ] 首次初始化完成：发送一次通知
- [ ] Windows 上以原生通知呈现；使用跨平台通知库（如 gen2brain/beeep），macOS/Linux 可编译
- [ ] go build 通过

### US-009: 日志记录

**Description:** 作为用户/开发者，我希望所有同步行为写入日志文件，以便排查问题。

**Acceptance Criteria:**

- [ ] 日志路径可配置（默认 exe 同目录 autosync.log）
- [ ] 每条含时间戳、级别（INFO/WARN/ERROR）、消息
- [ ] 记录每次同步的开始/结束、执行的 git 命令摘要、结果
- [ ] 日志文件被 .gitignore 自动排除
- [ ] go build 通过

### US-010: .gitignore 自动维护

**Description:** 作为用户，我希望同步工具自动忽略自身产生的文件和系统垃圾文件，以便仓库不被污染。

**Acceptance Criteria:**

- [ ] 配置中 ignore 列表的条目若不存在于 .gitignore 则追加
- [ ] 默认忽略：*.tmp, Thumbs.db, desktop.ini, .DS_Store, autosync.log, config.yaml
- [ ] 不重复追加已存在条目
- [ ] go build 通过

### US-011: 定时调度自安装（install / uninstall）

**Description:** 作为用户，我希望一条命令即可注册/卸载系统定时任务，以便实现"无感"自动同步而无需手动配置调度器。

**Acceptance Criteria:**

- [ ] `autosync install`：注册系统定时任务，按配置间隔调用 `autosync sync`
- [ ] `autosync uninstall`：移除已注册的定时任务
- [ ] Windows 实现：通过 schtasks 注册（/SC MINUTE /MO <间隔>）
- [ ] macOS/Linux：定义 launchd/cron 接口，MVP 可仅 Windows 实现，其余平台返回"未实现"提示（不阻塞编译）
- [ ] install 失败时输出明确错误并退出码 1
- [ ] go build 通过

## 5. 功能需求 (Functional Requirements)

- **FR-1:** 程序须支持子命令 `sync`（默认）、`install`、`uninstall`、`status`
- **FR-2:** `sync` 执行单次同步流程后退出（一次性命令，不常驻）
- **FR-3:** 程序须从与二进制同目录的 config.yaml 读取配置，并支持 `--config` 路径覆盖
- **FR-4:** 配置项须包含：repo_dir, remote, remote_url, branch, interval, conflict_strategy, commit_msg_format, log_file, show_console, ignore[]
- **FR-5:** 首次运行（无 .git）须自动 init + 首次推送
- **FR-6:** 每次同步须 add -A，有变更则 commit（消息含时间戳，格式可配置）
- **FR-7:** 每次同步须 fetch 远程；远程分支不存在则直接 push
- **FR-8:** 须用 merge-base 检测本地与远程分叉
- **FR-9:** 分叉时须先 pull --rebase；成功则 push
- **FR-10:** rebase 失败时须按 conflict_strategy 处理：local_wins（备份远程到 backup/remote-<ts></ts> 分支后 force push）/ remote_wins（reset --hard + clean）/ abort（中止并通知）
- **FR-11:** 日常成功同步须静默（仅日志）；冲突处理与失败须发系统通知
- **FR-12:** 须维护 .gitignore，确保 ignore 列表条目存在
- **FR-13:** 须写日志文件，含时间戳/级别/消息与 git 命令摘要
- **FR-14:** 所有 git 命令须设置 GIT_TERMINAL_PROMPT=0、GIT_MERGE_AUTOEDIT=no，避免交互阻塞
- **FR-15:** install/uninstall 须在 Windows 通过 schtasks 注册/移除定时任务，调用 sync 子命令
- **FR-16:** 跨平台抽象层须隔离通知与调度实现，使 macOS/Linux 可编译（功能可后续补齐）

## 6. 设计考量 (Design Considerations)

### 命令结构（CLI）

```
autosync                # 等同于 sync
autosync sync           # 执行单次同步，退出
autosync install        # 注册系统定时任务
autosync uninstall      # 移除定时任务
autosync status         # 显示配置与上次同步状态
```

### 调度模型（关键设计决策）

MVP 采用**"一次性命令 + OS 调度器"**模型，而非内置长驻守护进程。

- 更简洁：二进制只做一件事（同步一次），调度交给 OS（Windows Task Scheduler / launchd / cron）
- 更健壮：崩溃不影响下次调度；OS 负责重启
- 更无感：`install` 命令封装平台差异，用户无需手配调度器

代价：调度精度受 OS 限制（Windows Task Scheduler 最小 1 分钟）。对"分钟级轮询"目标可接受。
替代方案（内置 daemon + 内部 ticker）列为后续增强。

### 通知策略（无感核心）

| 场景           | 行为              |
| -------------- | ----------------- |
| 成功同步       | 静默，仅日志      |
| 冲突已自动处理 | 通知 + 备份分支名 |
| 同步失败       | 通知 + 错误摘要   |
| 首次初始化     | 通知一次          |

### 配置文件示例

```yaml
repo_dir: "D:\\MySyncFolder"
remote: "origin"
remote_url: "git@github.com:yourname/your-repo.git"
branch: "main"
interval: "5m"
conflict_strategy: "local_wins"   # local_wins | remote_wins | abort
commit_msg_format: "auto sync: {{.Timestamp}}"
log_file: "autosync.log"
show_console: false
ignore:
  - "*.tmp"
  - "Thumbs.db"
  - "desktop.ini"
  - ".DS_Store"
  - "autosync.log"
  - "config.yaml"
```

### 单次同步流程

```
开始
 ├─ 首次运行？→ git init + 首次推送 → 通知 → 结束
 ├─ 有本地变更？→ git add -A + commit
 ├─ git fetch
 ├─ 远程分支不存在？→ git push → 结束
 ├─ 有分叉？
 │    ├─ 否 → git push → 结束
 │    └─ 是 → git pull --rebase
 │              ├─ 成功 → git push → 结束
 │              └─ 冲突 → 按策略处理
 │                         ├─ local_wins:  备份远程 → force push → 通知
 │                         ├─ remote_wins: reset --hard 到远程 → 通知
 │                         └─ abort:       中止 → 通知
 └─ 日志记录全程
```

## 7. 技术考量 (Technical Considerations)

### 依赖

- Go 1.21+（单二进制，跨平台交叉编译）
- YAML 解析：gopkg.in/yaml.v3
- 系统通知：gen2brain/beeep（Win/macOS/Linux）或等价跨平台库
- 外部依赖：系统须已安装 git 并在 PATH 中

### 认证前置条件（假设）

- 依赖系统已配置的 git 凭证（SSH key 或 git credential helper）
- 程序不存储/管理任何凭证；GIT_TERMINAL_PROMPT=0 防止交互式凭证提示挂起
- 首次使用前用户须自行确保 `git push` 可无交互成功

### 跨平台架构

- 通知与调度分别定义 interface，按平台实现（构建标签隔离：notif_windows.go / notif_darwin.go / notif_linux.go）
- 路径处理统一用 filepath，不硬编码分隔符
- 不使用任何平台特定 syscall（如 user32.dll）于核心逻辑

### 已知权衡（须明示）

- **local_wins + 多设备并发编辑**：若 A、B 两设备同时有分歧改动，A 先同步会 force push 并备份远程；B 随后同步又会 force push 并备份 A 的状态。结果是"最后同步者覆盖"，旧版本散落在 backup/ 分支。数据不丢失但分支会堆积。建议真正并发编辑场景改用 abort 策略或正常 git 工作流。
- 定时轮询有分钟级延迟，非实时。
- force push 会改写远程历史；协作场景慎用。

### 错误处理

- 所有 git 命令失败须捕获 stdout/stderr，记日志并向上传递
- 网络失败（fetch/push）须明确提示"网络问题"，不执行 force push
- rebase 失败后须确保 rebase --abort 干净退出，不残留冲突状态

## 8. 成功指标 (Success Metrics)

- 配置完成后，默认 5 分钟内本地变更自动出现在远程，全程无手动操作
- 冲突时远程旧版本可从 backup/remote-* 分支完整恢复（零数据丢失）
- 单次同步（< 10k 文件）完成 < 5 秒
- 日常成功同步零通知打扰（仅日志可见）
- Windows 上完成完整 install → sync → uninstall 流程无错误
- 同一源码在 macOS/Linux 可编译（go build 通过），核心 sync 流程可运行

## 9. 开放问题 (Open Questions)

- 默认轮询间隔 5 分钟是否合适？是否需要"空闲时降频"以省电/省网络？
- backup/ 分支是否需要自动清理策略（如保留近 N 个）？长期使用会堆积。
- `status` 子命令是否需要引入状态文件以展示上次同步时间/结果？
- 多文件夹同步是否纳入路线图（多实例配置 vs 单进程多任务）？
- 是否需要 HTTPS token 认证引导（生成/存储 token）以降低 SSH 配置门槛？
- macOS/Linux 的 launchd/cron 自安装何时补齐？
- 是否需要 dry-run 模式（只展示将执行的操作，不实际改动）？
