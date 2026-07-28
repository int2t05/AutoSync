# PLAN · V1.1 开发计划：托盘守护

> 目标：把 V1.0 CLI 一次性工具升级为托盘常驻守护应用（方案 A）。引擎（Syncer / gitop / notify / state / lock）进程内复用。需求见 [prd.md](prd.md) V1.1，架构见 [tech.md](tech.md) V1.1。

## 里程碑总览

```mermaid
flowchart LR
  M1[M1 多任务配置基座] --> M2[M2 托盘守护核心]
  M2 --> M3[M3 配置窗口与托盘菜单]
  M3 --> M4[M4 自启与收尾]
```

| 里程碑 | 范围 | 映射 US |
|--------|------|---------|
| M1 | ConfigStore 多任务配置 + 向后兼容 + 每任务 state/lock | US-12 |
| M2 | Fyne 托盘守护骨架 + TaskScheduler + TaskRunner 复用 Syncer + 单实例锁 | US-11, US-13 |
| M3 | 配置窗口任务 CRUD + 托盘菜单（手动/暂停/状态/自启/退出） | US-12, US-14 |
| M4 | 注册表自启 + install/uninstall 新语义 + CLI 共存 + 测试冒烟 | US-15 |

## M1 · 多任务配置基座

**范围**：`internal/configstore` 多任务配置加载/校验/持久化；每任务独立 state 与 lock；旧单配置向后兼容。

**新增包**
- `internal/configstore`：`Task`（name + V1.0 Config 字段）、`Store`（加载 `autosync.conf.yaml` 的 `tasks: []`，CRUD，持久化）。无 `tasks` 键的单配置视为单任务。

**改动**
- `internal/state`：按任务名解析状态文件（`autosync.state-<name>.json`）。
- `internal/lock`：按任务名解析锁文件（`autosync.lock-<name>`）。

**验收**
- [ ] 多任务配置加载/校验/保存（单测，真实文件）
- [ ] 旧单配置（无 `tasks`）向后兼容为单任务（单测）
- [ ] 每任务 state/lock 互不干扰（单测）
- [ ] `go build` / `vet` / `test` 全绿

## M2 · 托盘守护核心

**范围**：无参数启动 = 托盘守护；Fyne 托盘图标 + 骨架窗口；TaskScheduler 每任务 ticker；TaskRunner 调 Syncer；守护级单实例锁。

**新增包**
- `internal/tasksched`：`TaskScheduler`（每任务 `time.Ticker`，到点调 TaskRunner）、`TaskRunner`（构造 Syncer 执行，捕获结果写 state + 通知）。
- `internal/tray`：`TrayApp`（Fyne 应用 + 托盘图标，M3 补菜单与窗口）。

**改动**
- `cmd/autosync/main.go`：无参数 → 托盘守护；`sync`/`status` 保留 CLI。

**验收**
- [ ] TaskScheduler 按各任务 interval 触发（单测，短 interval + 真实 git 仓库）
- [ ] TaskRunner 复用 Syncer 跑通 V1.0 同步状态机（集成测，真实 git）
- [ ] 守护级单实例锁：第二个无参数实例退出（单测）
- [ ] 无参数启动出现托盘图标（手动）
- [ ] `go build` / `vet` / `test` 全绿

## M3 · 配置窗口与托盘菜单

**范围**：Fyne 配置窗口（任务列表 CRUD）；托盘右键菜单（各任务手动同步/暂停、开机自启开关、打开配置、退出）。

**改动**
- `internal/tray`：配置窗口（表格 + 增删改表单）、托盘菜单、状态 tooltip。
- `internal/configstore`：CRUD 接口供窗口调用，保存后通知 TaskScheduler 热重载。

**验收**
- [ ] 窗口内增删改任务并持久化到 `autosync.conf.yaml`（手动 + 配置文件断言）
- [ ] 右键手动同步指定任务立即触发（手动 + 远程断言）
- [ ] 暂停/恢复任务生效（手动）
- [ ] 配置变更后 ticker 热重载，无需重启（手动）
- [ ] `go build` / `vet` / `test` 全绿

## M4 · 自启与收尾

**范围**：注册表 Run 键自启；`install`/`uninstall` 新语义；CLI 与托盘共存；端到端冒烟。

**新增包**
- `internal/autostart`：`Enable`/`Disable`/`IsEnabled`，Windows 写 `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`，非 Windows stub。参数构造为纯函数可单测。

**改动**
- `cmd/autosync/main.go`：`install`/`uninstall` 改调 autostart（取代 schtasks）。
- `internal/sched`：V1.1 由托盘守护 + autostart 取代，已移除（含 `test/sched_test.go`）。

**验收**
- [x] autostart 参数构造纯函数单测（跨平台）
- [ ] `install` 写注册表 Run 键、`uninstall` 移除（Windows 手动 + 注册表断言）
- [ ] 端到端：双击 → 配置两任务 → 托盘定时同步 → 右键手动 → 重启自启（真实 GitHub 仓库冒烟）
- [ ] CLI `sync`/`status` 与托盘共存不冲突（单实例锁验证）
- [x] `go build` / `vet` / `test` / `-race` 全绿

## 收尾增补：byproduct 迁移 + 图标 + 审计修复

M4 之后的收尾改进（代码已落地）：

- **byproduct 迁移**：配置/日志/状态/锁统一到 `~/.autosync/`（`logs/`/`state/`/`locks/` 子目录），exe 位置独立。路径解析集中到 `internal/config/paths.go`，移除 `BesideExe`/`Config.Resolve*`/`LogFile`/`StateFile`。可用 `AUTOSYNC_DATA_DIR` 覆盖。
- **自有图标**：`internal/assets`（icon.svg → icon.png）嵌入供托盘/窗口；`cmd/genicon` 光栅化；`cmd/autosync/winres` + go-winres 生成 .syso 供 exe 图标。
- **审计修复**：tasksched `runners` 数据竞争（`Runners` 返回副本、`RunNow`/`SetPaused` 锁内查找）、`runTray` 先初始化日志再加载配置（静默版可诊断）、`TaskRunner.Run` 错误日志对齐、`autostart.Disable` 容错已不存在值、`configstore.Save` 原子写、托盘 `selected` 重置。
- **延后**（记入 [TODO.md](TODO.md)）：抽 `sync.Orchestrator`、gitop 超时、Reload 非阻塞、RelationTo 破坏性回退等。

## 约定

- 测试在 `test/` 目录，真实 git 临时仓库，禁止 mock。GUI/托盘/注册表项标手动验收。
- 跨平台：核心逻辑不依赖平台 syscall；Fyne 跨平台；autostart 用构建标签隔离 Windows。
- 中文注释，文件头 + 函数注释，`// TODO:` 标待优化。
- 复用 V1.0 引擎，不重写 Syncer/gitop/notify/state/lock。

## V1.2 Linux：CLI + systemd 守护

把 Linux 从"CLI 预留"升级为正式守护：`daemon` 子命令（复用 TaskScheduler，无 GUI）+ systemd user service 自启 + tarball 打包。对齐 Syncthing/Rclone，避开 Linux 托盘碎片化（GNOME 默认不支持、Wayland 不稳）。完整设计见会话 plan 文件 plan-logical-twilight.md。

| 里程碑 | 范围 |
|--------|------|
| L1 | `daemon` 子命令（setupTrayEnv + DaemonLock + TaskScheduler + SIGINT/SIGTERM 信号退出）+ dispatch_linux 默认分流 daemon + 子进程测试 |
| L2 | 拆 `autostart_other.go` → `autostart_linux.go`（systemd user service 真实现）+ `autostart_darwin.go`（stub）+ BuildRunCommand 平台化 |
| L3 | `package-linux` tarball（amd64+arm64 + install.sh + 配置模板）+ `autosync.conf.example.yaml` 多任务模板 |
| L4 | 文档（README/tech/api/TODO/CLAUDE）+ Linux CI（`.github/workflows/linux.yml`） |

**关键决策**：Linux 无 GUI（托盘碎片化）；daemon 无运行时控制 IPC（手动同步靠 `autosync sync` 单次，暂停靠编辑配置重启，列 TODO 后续加 SIGHUP Reload / socket）。
