# AutoSync 构建/测试脚本
# 用法：make test | make build | make build-all | make vet

GO ?= go

.PHONY: test test-race vet fmt tidy build build-all icons clean

# 运行全部测试（test/ 目录，真实数据）
test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

# 静态检查
vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

tidy:
	$(GO) mod tidy

# 构建 Windows 托盘双版本（控制台 + 静默；-tags traygui 启用 Fyne 托盘，需 CGO + gcc）
build:
	$(GO) build -tags traygui -ldflags="-s -w" -o AutoSync.exe ./cmd/autosync
	$(GO) build -tags traygui -ldflags="-s -w -H windowsgui" -o AutoSync_Silent.exe ./cmd/autosync

# 构建 Windows CLI 版（无托盘，纯 Go，快速，供开发/脚本/CI）
build-cli:
	$(GO) build -o AutoSync-CLI.exe ./cmd/autosync

# 三平台交叉编译（Windows 托盘 + macOS/Linux CLI stub；非 Windows 纯 Go 可交叉编译）
build-all:
	$(GO) build -tags traygui -ldflags="-s -w" -o AutoSync.exe ./cmd/autosync
	$(GO) build -tags traygui -ldflags="-s -w -H windowsgui" -o AutoSync_Silent.exe ./cmd/autosync
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 $(GO) build -o autosync-darwin ./cmd/autosync
	GOOS=linux  GOARCH=amd64 CGO_ENABLED=0 $(GO) build -o autosync-linux ./cmd/autosync

# 生成图标资源：SVG→PNG（cmd/genicon）+ Windows exe 图标 .syso（go-winres）
# 仅在 icon.svg 改动后运行；.syso 已提交，常规构建无需执行。
icons:
	$(GO) run ./cmd/genicon
	cd cmd/autosync && $(GO) run github.com/tc-hib/go-winres@latest make

clean:
	rm -f AutoSync.exe AutoSync_Silent.exe AutoSync-CLI.exe autosync-darwin autosync-linux
