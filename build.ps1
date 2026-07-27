# build.ps1 — AutoSync 构建/测试脚本（Windows PowerShell）
# 用法：.\build.ps1 <test|vet|build|build-all|clean>
# 默认目标：build。需系统已安装 go 并在 PATH，或先 $env:PATH += ";D:\DevelopTools\go\bin"

param(
    [Parameter(Position = 0)]
    [ValidateSet("test", "test-race", "vet", "fmt", "tidy", "build", "build-all", "clean")]
    [string]$Target = "build"
)

$ErrorActionPreference = "Stop"

function Invoke-Test      { go test ./... }
function Invoke-TestRace  { go test -race ./... }
function Invoke-Vet       { go vet ./... }
function Invoke-Fmt       { go fmt ./... }
function Invoke-Tidy      { go mod tidy }

# 构建 Windows 双版本（带控制台 + 静默无窗口）
function Invoke-Build {
    go build -ldflags="-s -w" -o AutoSync.exe ./cmd/autosync
    go build -ldflags="-s -w -H windowsgui" -o AutoSync_Silent.exe ./cmd/autosync
    Write-Host "构建完成：AutoSync.exe（控制台）+ AutoSync_Silent.exe（静默）"
}

# 三平台交叉编译（4 目标：Windows 控制台+静默 / macOS / Linux）
function Invoke-BuildAll {
    go build -ldflags="-s -w" -o AutoSync.exe ./cmd/autosync
    go build -ldflags="-s -w -H windowsgui" -o AutoSync_Silent.exe ./cmd/autosync
    $env:GOOS = "darwin"; $env:GOARCH = "amd64"; go build -o autosync-darwin ./cmd/autosync
    $env:GOOS = "linux";  $env:GOARCH = "amd64"; go build -o autosync-linux ./cmd/autosync
    $env:GOOS = $null; $env:GOARCH = $null
    Write-Host "交叉编译完成：AutoSync.exe / AutoSync_Silent.exe / autosync-darwin / autosync-linux"
}

function Invoke-Clean {
    Remove-Item -ErrorAction SilentlyContinue -Force AutoSync.exe, AutoSync_Silent.exe, autosync-darwin, autosync-linux
}

switch ($Target) {
    "test"      { Invoke-Test }
    "test-race" { Invoke-TestRace }
    "vet"       { Invoke-Vet }
    "fmt"       { Invoke-Fmt }
    "tidy"      { Invoke-Tidy }
    "build"     { Invoke-Build }
    "build-all" { Invoke-BuildAll }
    "clean"     { Invoke-Clean }
}
