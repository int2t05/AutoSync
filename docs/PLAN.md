# PLAN.md: AutoSync 开发计划

> 配套文档：[PRD.md](PRD.md)（需求）/ [TECH.md](TECH.md)（系统设计）。本文档给出大体里程碑与对应验收测试，并划分 v1.0 / 后续边界。

## 1. 概述

- **粒度**：5 个粗阶段（P1–P5），每阶段含多个用户故事
- **验收形式**：自动化 Go 测试（`make test`）作门槛 + 平台相关项手动验收清单；均映射到 PRD 的 US 编号
- **v1.0 边界**：P1–P5 全部完成 = Windows 完整可用；macOS/Linux 自安装、daemon 等列为后续
- **一致性**：阶段范围、术语、US 编号与 PRD/TECH 对齐

## 2. 里程碑总览

| 阶段 | 名称 | 范围 | 映射 US | 版本 |
|------|------|------|---------|------|
| P1 | 基础骨架与配置 | 项目骨架、config、log、.gitignore | US-001, US-009, US-010 | v1.0 |
| P2 | 核心同步引擎 | gitop 接口+实现+测试夹具、状态机主路径（init/commit/fetch/diverge/rebase/push） | US-002, US-003, US-004, US-005, US-007 | v1.0 |
| P3 | 冲突处理与可观测性 | 三种冲突策略+backup 清理、通知策略、state 文件+status 命令 | US-006, US-008, status | v1.0 |
| P4 | 调度与健壮性 | schtasks install/uninstall、单实例锁、重试、dry-run | US-011, dry-run | v1.0 |
| P5 | 发布与跨平台 | 交叉编译、Makefile/build.ps1、README、v1.0 发布 | — | v1.0 |

依赖顺序：P1 → P2 → P3 → P4 → P5（严格线性，每阶段验收通过方可进入下一阶段）。

## 3. 各里程碑详述

### P1 · 基础骨架与配置

**范围**：建立项目结构（`cmd/autosync` + `internal/*`）；`Config` 加载/校验/默认值；`Logger`；`.gitignore` 自动维护。

**验收测试**
- 自动化（门槛）
  - [ ] config 缺失必填项 / 非法策略 / 目录不存在 → 退出码 1（单测）
  - [ ] 可选项默认值正确（remote=origin, branch=main, interval=1m, conflict_strategy=local_wins）（单测）
  - [ ] `--config` 路径覆盖生效（单测）
  - [ ] Logger 分级写入文件+控制台、并发安全（单测）
  - [ ] `.gitignore` 追加缺失条目、不重复追加（单测）
  - [ ] `make test` 通过，`go vet` 无警告
- 手动：无
- 映射：US-001, US-009, US-010

### P2 · 核心同步引擎

**范围**：`GitOperator` 接口 + `execGit` 实现 + `fakeGit` 测试桩；`Syncer` 状态机主路径（S1→S5→S9）；临时仓库集成测试夹具。

**验收测试**
- 自动化（门槛，集成测试用临时 git 仓库 + `file://` remote）
  - [ ] 首次运行（无 `.git`）→ init + 首次 push（US-002）
  - [ ] 有本地变更 → add + commit + push；无变更 → 跳过提交（US-003）
  - [ ] fetch 后远程分支不存在 → 直接 push；存在 → merge-base 正确判定分叉（US-004）
  - [ ] 分叉 + rebase 成功 → push，记 AutoMerged（US-005）
  - [ ] 无分叉 → push，记 Pushed（US-007）
  - [ ] `make test` 通过
- 手动
  - [ ] 真实 GitHub 仓库：本地新建文件 → `autosync sync` → 远程可见
  - [ ] 远程改文件 → 本地 `autosync sync` → 本地拉到改动
- 映射：US-002, US-003, US-004, US-005, US-007

### P3 · 冲突处理与可观测性

**范围**：三种冲突策略 + backup 分支自动清理（保留 N=10）；`Notifier`（beeep）+ 通知策略映射；`StateStore` + `status` 命令。

**验收测试**
- 自动化（门槛）
  - [ ] 构造冲突：local_wins → 备份分支 `backup/remote-*` 存在且可 checkout 恢复 + `--force-with-lease` 成功（US-006）
  - [ ] remote_wins → 本地被 reset --hard，未推送改动丢失（US-006）
  - [ ] abort → 无仓库变更 + 退出码非零（US-006）
  - [ ] backup 清理：创建 >N 个备份后仅保留最新 10 个（本地+远程）
  - [ ] 通知策略单测：各 `Outcome` → 正确通知级别（成功静默 / 冲突警告 / 失败错误）
  - [ ] `make test` 通过
- 手动
  - [ ] Windows 实测：冲突解决时弹出系统通知（含备份分支名），成功同步无通知
  - [ ] `autosync status` 输出上次同步时间/结果
- 映射：US-006, US-008, status

### P4 · 调度与健壮性

**范围**：`Scheduler`（Windows schtasks install/uninstall）；单实例锁；网络操作重试；`sync --dry-run`。

**验收测试**
- 自动化（门槛）
  - [ ] 重试单测：前 2 次失败、第 3 次成功 → 最终成功；3 次全失败 → 判定失败
  - [ ] dry-run 集成测试：输出计划（将提交/分叉/策略），仓库无任何 commit/push/分支变更
  - [ ] 锁单测：第二个实例启动 → 静默跳过（不并发执行）
  - [ ] `make test` 通过
- 手动
  - [ ] `autosync install` → `schtasks /Query /tn AutoSync` 可见，按 interval 触发
  - [ ] `autosync uninstall` → 任务移除
  - [ ] `autosync sync --dry-run` 输出计划、不改动仓库
- 映射：US-011, dry-run

### P5 · 发布与跨平台

**范围**：三平台交叉编译；`Makefile`/`build.ps1`；`README.md`；`config.example.yaml`；v1.0 发布。

**验收测试**
- 自动化（门槛）
  - [ ] `make build-all`：Windows（控制台+静默版）/ macOS / Linux 四个目标 `go build` 全部通过
  - [ ] `make test` 全绿
- 手动
  - [ ] Windows 双击 `AutoSync.exe` 跑通完整流程：install → 编辑文件自动同步 → uninstall
  - [ ] 按 README 步骤从零配置到首次同步成功
  - [ ] macOS/Linux 上 `autosync sync` 核心流程可运行（调度自安装可未实现）
- 发布产物：v1.0 二进制（Windows 双版本）+ README + config.example.yaml

## 4. v1.0 发布标准

全部满足方可标记 v1.0：

- [ ] P1–P5 所有自动化验收测试通过（`make test` 全绿）
- [ ] P2–P5 所有手动验收清单项确认通过
- [ ] Windows 完整流程（install → sync → uninstall）无错误
- [ ] 三平台 `go build` 通过，macOS/Linux 核心同步可运行
- [ ] README + config.example.yaml 齐备
- [ ] PRD 所有 v1.0 范围内 US 的验收标准满足

## 5. 后续路线（post-v1.0）

| 项 | 说明 |
|----|------|
| macOS/Linux 自安装 | launchd plist / cron 实现 `Scheduler.Install` |
| daemon 模式 | 内置 ticker 长驻，支持亚分钟级间隔 |
| 多文件夹 | 单进程多任务或多实例配置管理 |
| HTTPS token 引导 | 降低 SSH 配置门槛 |
| 连续 N 次失败降噪 | state 文件已预留 `ConsecutiveFailures` 字段 |
| backup 清理增强 | 按时间过期、按大小、跨设备协调 |
| 托盘应用 | 常驻托盘 + 状态可视化（PRD 后续增强） |

## 6. 验收测试约定

- **自动化门槛**：每个阶段列出的"自动化"项必须以 Go 测试实现并在 `make test` 中通过，否则阶段不算完成
- **手动清单**：平台相关（schtasks、系统通知、真实 GitHub、双击运行）项列出手动验证步骤，由开发者执行并勾选
- **可追溯**：每条验收测试标注映射的 PRD US 编号，确保需求→设计→计划三文档一致
- **回归**：进入新阶段后，前序阶段的自动化测试须持续通过（累积回归）
