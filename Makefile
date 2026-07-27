# AutoSync 构建/测试脚本
# 用法：make test | make build | make build-all | make vet

GO ?= go

.PHONY: test test-race vet fmt tidy build build-all clean

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

# 构建 Windows 双版本（带控制台 + 静默）
build:
	$(GO) build -ldflags="-s -w" -o AutoSync.exe ./cmd/autosync
	$(GO) build -ldflags="-s -w -H windowsgui" -o AutoSync_Silent.exe ./cmd/autosync

# 三平台交叉编译验证
build-all:
	$(GO) build -ldflags="-s -w" -o AutoSync.exe ./cmd/autosync
	GOOS=darwin GOARCH=amd64 $(GO) build -o autosync-darwin ./cmd/autosync
	GOOS=linux  GOARCH=amd64 $(GO) build -o autosync-linux ./cmd/autosync

clean:
	rm -f AutoSync.exe AutoSync_Silent.exe autosync-darwin autosync-linux
