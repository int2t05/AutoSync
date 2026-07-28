# AutoSync 构建/测试脚本
# 用法：make test | make build | make build-all | make package-linux | make vet

GO ?= go

.PHONY: test test-race vet fmt tidy build build-cli build-all icons build-macos-engine build-macos-app build-macos-dmg package-linux clean

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

# 构建 Windows 托盘版（单 exe，无控制台；-tags traygui 启用 Fyne 托盘，需 CGO + gcc）
# 双击出配置窗口、可关闭至托盘；-H windowsgui 去掉 cmd 黑窗。
build:
	$(GO) build -tags traygui -ldflags="-s -w -H windowsgui" -o AutoSync.exe ./cmd/autosync

# 构建 Windows CLI 版（无托盘，纯 Go，快速，供开发/脚本/CI）
build-cli:
	$(GO) build -o AutoSync-CLI.exe ./cmd/autosync

# 三平台编译：Windows 托盘 exe + macOS 引擎（amd64/arm64）+ Linux CLI（amd64/arm64）
# macOS 引擎纯 Go 可交叉编译；universal 二进制合并需 macOS 主机 lipo（build-macos-engine）
build-all:
	$(GO) build -tags traygui -ldflags="-s -w -H windowsgui" -o AutoSync.exe ./cmd/autosync
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 $(GO) build -o autosync-engine-darwin-amd64 ./cmd/autosync
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 $(GO) build -o autosync-engine-darwin-arm64 ./cmd/autosync
	GOOS=linux  GOARCH=amd64 CGO_ENABLED=0 $(GO) build -o autosync-linux-amd64 ./cmd/autosync
	GOOS=linux  GOARCH=arm64 CGO_ENABLED=0 $(GO) build -o autosync-linux-arm64 ./cmd/autosync

# 生成图标资源：SVG→PNG（cmd/genicon）+ Windows exe 图标 .syso（go-winres）
# 仅在 icon.svg 改动后运行；.syso 已提交，常规构建无需执行。
icons:
	$(GO) run ./cmd/genicon
	cd cmd/autosync && $(GO) run github.com/tc-hib/go-winres@latest make

# 构建 macOS Go 引擎二进制（amd64 + arm64，纯 Go 可交叉编译；universal 合并需 macOS lipo，见 build-macos-app）
build-macos-engine:
	mkdir -p dist
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 $(GO) build -o dist/autosync-engine-amd64 ./cmd/autosync
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 $(GO) build -o dist/autosync-engine-arm64 ./cmd/autosync

# 构建 macOS .app（需 macOS 主机 + xcodegen + Xcode；详见 macos/build-app.sh）
build-macos-app:
	bash macos/build-app.sh

# 打包 macOS DMG（需 macOS 主机；依赖 build-macos-app）
build-macos-dmg:
	bash macos/build-dmg.sh

# 打包 Linux tarball（amd64 + arm64，纯 Go 交叉编译；含二进制 + 配置模板 + install.sh + README）
package-linux:
	mkdir -p dist/autosync-linux-amd64 dist/autosync-linux-arm64
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -o dist/autosync-linux-amd64/autosync ./cmd/autosync
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -o dist/autosync-linux-arm64/autosync ./cmd/autosync
	cp autosync.conf.example.yaml scripts/install-linux.sh scripts/README-install-linux.md dist/autosync-linux-amd64/
	cp autosync.conf.example.yaml scripts/install-linux.sh scripts/README-install-linux.md dist/autosync-linux-arm64/
	tar -czf dist/autosync-linux-amd64.tar.gz -C dist autosync-linux-amd64
	tar -czf dist/autosync-linux-arm64.tar.gz -C dist autosync-linux-arm64
	rm -rf dist/autosync-linux-amd64 dist/autosync-linux-arm64

clean:
	rm -f AutoSync.exe AutoSync-CLI.exe autosync-engine-darwin-amd64 autosync-engine-darwin-arm64 autosync-linux-amd64 autosync-linux-arm64
	rm -rf dist
