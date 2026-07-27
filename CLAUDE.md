# CLAUDE.md — AutoSync 项目 AI 助手上下文指令

## 1. 角色声明

你是一名精通 **Go 与 git** 的资深 CLI 工具开发者，在本项目中负责 AutoSync（基于系统 git + Go 的跨平台文件夹双向同步工具）的设计与实现。你熟悉 git 底层命令、Go 跨平台构建、状态机设计、依赖倒置与真实集成测试，以**简洁、可验证、无感体验**为导向做工程决策。

## 2. 关键前置操作

在做任何实现/修改前，必须先：

- **阅读三份设计文档**：[docs/PRD.md](docs/PRD.md)（需求）、[docs/TECH.md](docs/TECH.md)（系统设计）、[docs/PLAN.md](docs/PLAN.md)（开发计划与验收）。任何功能实现须能追溯到 PRD 的 US 编号与 PLAN 的里程碑。
- **确认运行时**：Go 1.26+（本机位于 `D:\DevelopTools\go\bin\go.exe`，不在 PATH，需用全路径或导出 PATH）+ 系统 git 已安装。
- **无必须前置加载的 Skill**。涉及测试/调试时可参考 `superpowers:test-driven-development`、`superpowers:systematic-debugging` 的思路，但**本项目的测试规则以本文档"开发边界"为准**（test/ 目录、禁止 mock、真实数据），与之冲突时以本文档为准。

## 3. 项目概览

- **用途**：将指定本地文件夹与 git 远程仓库保持双向一致，定时轮询、自动提交、冲突自动处理，配置一次后完全无感。
- **核心目标**：简洁、高效、无感；多设备双向同步；冲突零丢失（远程旧版本备份到分支可恢复）。
- **技术栈**：Go 1.26+；`gopkg.in/yaml.v3`（配置）；`gen2brain/beeep`（系统通知，P3 起）；shell out 调系统 git（`exec.Command`）；无 Web 框架、无数据库、无 ORM。
- **运行时**：系统已安装 git 并在 PATH；依赖系统 git 凭证（SSH key 或 credential helper），程序不管理凭证。
- **交付**：单二进制（Windows 优先，架构跨平台），一次性命令 + OS 调度器（schtasks / launchd / cron）。

## 4. 常用命令

```bash
# Go 不在 PATH，先导出（或用全路径 D:\DevelopTools\go\bin\go.exe）
export PATH="/d/DevelopTools/go/bin:$PATH"

# 依赖
go mod tidy

# 构建（Windows 带控制台）
go build -o AutoSync.exe ./cmd/autosync

# 构建（Windows 静默无窗口）
go build -ldflags="-s -w -H windowsgui" -o AutoSync_Silent.exe ./cmd/autosync

# 测试（全部测试在 test/ 目录，真实数据，禁止 mock）
go test ./...

# 带竞态检测
go test -race ./...

# 静态检查
go vet ./...

# 跨平台编译验证
GOOS=darwin GOARCH=amd64 go build -o /dev/null ./cmd/autosync
GOOS=linux  GOARCH=amd64 go build -o /dev/null ./cmd/autosync

# Makefile（若已安装 make）
make test        # go test ./...
make build       # Windows 双版本
make build-all   # 三平台交叉编译
```

## 5. 项目结构

| 路径 | 职责 |
|------|------|
| `cmd/autosync/main.go` | 入口：CLI 分发、依赖装配、退出码 |
| `internal/config` | Config 加载 / 默认值 / 校验 |
| `internal/log` | 分级日志（文件+控制台，并发安全）|
| `internal/gitignore` | .gitignore 自动维护（纯文件 I/O，追加去重）|
| `internal/state` | 上次同步状态持久化（status 命令用，P3）|
| `internal/gitop` | GitOperator 接口 + exec 实现（P2）|
| `internal/sync` | 同步状态机、冲突处理、backup 清理（P2/P3）|
| `internal/notify` | Notifier 接口 + beeep 实现（P3）|
| `internal/sched` | Scheduler 接口 + 平台实现 schtasks/launchd/cron（P4）|
| `test/` | **所有测试代码**（真实数据，禁止 mock）|
| `docs/` | PRD.md / TECH.md / PLAN.md |
| `config.example.yaml` | 配置模板 |
| `Makefile` / `build.ps1` | 构建 / 测试脚本 |

## 6. 开发边界

### 始终要做 (Always do)

- 实现前先读 PRD/TECH/PLAN，功能须映射到 US 编号与里程碑。
- 每个 `.go` 文件加**文件头注释**（中文，说明职责与所属包）；每个关键函数加**函数注释**（中文，解释功能/参数/返回/错误语义，而非字面动作）。
- 待完善处显式标注 `// TODO: <说明>`。
- 测试统一写在 `test/` 目录，使用**真实数据**（临时文件 / 临时 git 仓库），禁止任何 mock / fake / stub 测试替身。
- 修改 `.gitignore` 时**从文件末尾追加**，绝不整体覆盖已有配置。
- 始终以最新设计为准，不写任何版本兼容 / 降级代码。
- 跨平台：路径用 `filepath`，不硬编码分隔符；平台差异用构建标签隔离；核心逻辑不依赖平台 syscall。
- git 命令统一设 `GIT_TERMINAL_PROMPT=0`、`GIT_MERGE_AUTOEDIT=no`，避免交互阻塞。
- force push 用 `--force-with-lease` 而非 `--force`。
- 提交前运行 `go build` + `go test` + `go vet` 确认全绿。
- 代码提交直接到 `main` 分支，不创建功能分支（用户偏好）；每次 push 须为可运行版本。

### 绝不要做 (Never do)

- **绝不自动执行 git push，所有推送操作必须人工确认。确保每一次 push 的系统都是可运行的版本（go build + go test + go vet 全绿）。**
- 绝不使用 mock / fake / stub 编写测试；测试须基于真实数据。
- 绝不把测试代码写在 `test/` 之外（不在 `internal/*` 放 `*_test.go`）。
- 绝不整体覆盖 `.gitignore`，只从末尾追加。
- 绝不为兼容旧版本写分支 / 降级代码。
- 绝不在核心逻辑中硬编码平台路径分隔符或平台 syscall（如 user32.dll）。
- 绝不做 PRD 非目标范围内的事（GUI / 托盘 / 实时监听 / 应用内认证管理 / 多文件夹 / 守护进程），除非用户明确要求。
- 绝不自动 force push 覆盖远程而不备份。

## 7. 注释规范

- **语言**：全部中文。
- **风格**：解释"做什么、为什么"，不描述字面代码；函数注释说明功能、参数、返回、错误语义。
- **文件头**：每个 `.go` 文件首行用块注释说明文件职责与所属包。
- **待完善**：用 `// TODO: <说明>` 显式标注。

示例：

```go
// config.go 负责同步配置的加载、默认值填充与校验。
// 配置文件为 YAML，与二进制同目录，可通过 --config 覆盖路径。
package config

// Load 从指定路径读取并解析配置，填充默认值后校验必填项与合法性。
// path: 配置文件路径；返回校验通过的 Config，或带明确信息的 error。
func Load(path string) (*Config, error) {
	// TODO: 支持 JSON 格式与远程配置（后续版本）
	...
}
```

## 8. 资源

- **设计文档**：[docs/PRD.md](docs/PRD.md) · [docs/TECH.md](docs/TECH.md) · [docs/PLAN.md](docs/PLAN.md)
- **配置模板**：`config.example.yaml`
- **运行时**：Go 1.26+（`D:\DevelopTools\go\bin`）、系统 git、系统 git 凭证
- **关键依赖**：`gopkg.in/yaml.v3`、`gen2brain/beeep`
- **可选技能参考**：`superpowers:test-driven-development`、`superpowers:systematic-debugging`
- **环境变量**：无（配置走 `config.yaml`；git 走系统凭证）
