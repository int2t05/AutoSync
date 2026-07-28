# build.ps1 — AutoSync 构建/测试脚本（Windows PowerShell）
# 用法：.\build.ps1 <test|vet|build|build-cli|build-all|package-linux|clean>
# 默认目标：build。需系统已安装 go 并在 PATH，或先 $env:PATH += ";D:\DevelopTools\go\bin"
# 托盘构建（build/build-all）需 CGO + gcc（mingw），用 -tags traygui 启用 Fyne 托盘。

param(
    [Parameter(Position = 0)]
    [ValidateSet("test", "test-race", "vet", "fmt", "tidy", "build", "build-cli", "build-all", "icons", "build-macos-engine", "build-macos-app", "build-macos-dmg", "package-linux", "clean")]
    [string]$Target = "build"
)

$ErrorActionPreference = "Stop"

function Invoke-Test      { go test ./... }
function Invoke-TestRace  { go test -race ./... }
function Invoke-Vet       { go vet ./... }
function Invoke-Fmt       { go fmt ./... }
function Invoke-Tidy      { go mod tidy }

# 构建 Windows 托盘版（单 exe，无控制台；-tags traygui 启用 Fyne 托盘，需 CGO）
# 双击出配置窗口、可关闭至托盘；-H windowsgui 去掉 cmd 黑窗。
function Invoke-Build {
    go build -tags traygui -ldflags="-s -w -H windowsgui" -o AutoSync.exe ./cmd/autosync
    Write-Host "构建完成：AutoSync.exe（无控制台，双击出窗口、可缩至托盘）"
}

# 构建 Windows CLI 版（无托盘，纯 Go，快速，供开发/脚本/CI）
function Invoke-BuildCli {
    go build -o AutoSync-CLI.exe ./cmd/autosync
    Write-Host "构建完成：AutoSync-CLI.exe（CLI，无托盘）"
}

# 三平台编译：Windows 托盘 exe + macOS 引擎（amd64/arm64）+ Linux CLI（amd64/arm64）
function Invoke-BuildAll {
    go build -tags traygui -ldflags="-s -w -H windowsgui" -o AutoSync.exe ./cmd/autosync
    $env:CGO_ENABLED = "0"
    $env:GOOS = "darwin"; $env:GOARCH = "amd64"; go build -o autosync-engine-darwin-amd64 ./cmd/autosync
    $env:GOOS = "darwin"; $env:GOARCH = "arm64"; go build -o autosync-engine-darwin-arm64 ./cmd/autosync
    $env:GOOS = "linux";  $env:GOARCH = "amd64"; go build -o autosync-linux-amd64 ./cmd/autosync
    $env:GOOS = "linux";  $env:GOARCH = "arm64"; go build -o autosync-linux-arm64 ./cmd/autosync
    $env:GOOS = $null; $env:GOARCH = $null; $env:CGO_ENABLED = $null
    Write-Host "三平台编译完成：AutoSync.exe / autosync-engine-darwin-{amd64,arm64} / autosync-linux-{amd64,arm64}"
}

function Invoke-Clean {
    Remove-Item -ErrorAction SilentlyContinue -Force AutoSync.exe, AutoSync-CLI.exe, autosync-engine-darwin-amd64, autosync-engine-darwin-arm64, autosync-linux-amd64, autosync-linux-arm64
    Remove-Item -ErrorAction SilentlyContinue -Force -Recurse dist
}

# 生成图标资源：SVG→PNG + Windows exe 图标 .syso（仅 icon.svg 改动后运行；.syso 已提交）
function Invoke-Icons {
    go run ./cmd/genicon
    Push-Location cmd/autosync
    go run github.com/tc-hib/go-winres@latest make
    Pop-Location
    Write-Host "图标资源已生成：internal/assets/icon.png + cmd/autosync/*.syso"
}

# 构建 macOS Go 引擎二进制（amd64 + arm64，纯 Go 可交叉编译；universal 合并需 macOS lipo）
function Invoke-BuildMacosEngine {
    New-Item -ItemType Directory -Force -Path dist | Out-Null
    $env:CGO_ENABLED = "0"
    $env:GOOS = "darwin"; $env:GOARCH = "amd64"; go build -o dist/autosync-engine-amd64 ./cmd/autosync
    $env:GOOS = "darwin"; $env:GOARCH = "arm64"; go build -o dist/autosync-engine-arm64 ./cmd/autosync
    $env:GOOS = $null; $env:GOARCH = $null; $env:CGO_ENABLED = $null
    Write-Host "macOS 引擎二进制：dist/autosync-engine-{amd64,arm64}（universal 合并需 macOS lipo）"
}

# 构建 macOS .app（需 macOS 主机 + xcodegen + Xcode）
function Invoke-BuildMacosApp { bash macos/build-app.sh }

# 打包 macOS DMG（需 macOS 主机）
function Invoke-BuildMacosDmg { bash macos/build-dmg.sh }

# 打包 Linux tarball（amd64 + arm64，纯 Go 交叉编译；含二进制 + 配置模板 + install.sh + README）
function Invoke-PackageLinux {
    New-Item -ItemType Directory -Force -Path dist/stage-amd64, dist/stage-arm64 | Out-Null
    $env:CGO_ENABLED = "0"
    $env:GOOS = "linux"; $env:GOARCH = "amd64"; go build -o dist/stage-amd64/autosync ./cmd/autosync
    $env:GOOS = "linux"; $env:GOARCH = "arm64"; go build -o dist/stage-arm64/autosync ./cmd/autosync
    $env:GOOS = $null; $env:GOARCH = $null; $env:CGO_ENABLED = $null
    foreach ($arch in @("amd64","arm64")) {
        Copy-Item autosync.conf.example.yaml, scripts/install-linux.sh, scripts/README-install-linux.md "dist/stage-$arch/"
        tar -czf "dist/autosync-linux-$arch.tar.gz" -C "dist/stage-$arch" .
    }
    Remove-Item -Recurse -Force dist/stage-amd64, dist/stage-arm64
    Write-Host "Linux tarball：dist/autosync-linux-{amd64,arm64}.tar.gz"
}

switch ($Target) {
    "test"      { Invoke-Test }
    "test-race" { Invoke-TestRace }
    "vet"       { Invoke-Vet }
    "fmt"       { Invoke-Fmt }
    "tidy"      { Invoke-Tidy }
    "build"     { Invoke-Build }
    "build-cli" { Invoke-BuildCli }
    "build-all" { Invoke-BuildAll }
    "icons"     { Invoke-Icons }
    "build-macos-engine" { Invoke-BuildMacosEngine }
    "build-macos-app"    { Invoke-BuildMacosApp }
    "build-macos-dmg"    { Invoke-BuildMacosDmg }
    "package-linux"      { Invoke-PackageLinux }
    "clean"     { Invoke-Clean }
}
