#!/bin/bash
# build-app.sh 在 macOS 上构建 AutoSync.app（Swift 壳 + Go 引擎 universal 二进制）。
# 前置：go、xcodegen（brew install xcodegen）、Xcode 命令行工具。
# 用法：bash macos/build-app.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIST="$ROOT/dist"
mkdir -p "$DIST"

echo "=== 1. 生成图标资源（SVG→PNG）==="
( cd "$ROOT" && go run ./cmd/genicon )

echo "=== 2. 构建 Go 引擎 universal 二进制（amd64 + arm64 + lipo）==="
( cd "$ROOT" && \
  GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -o "$DIST/autosync-engine-amd64" ./cmd/autosync && \
  GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o "$DIST/autosync-engine-arm64" ./cmd/autosync && \
  lipo -create -output "$DIST/autosync-engine" "$DIST/autosync-engine-amd64" "$DIST/autosync-engine-arm64" && \
  rm -f "$DIST/autosync-engine-amd64" "$DIST/autosync-engine-arm64" )

echo "=== 3. 生成 Xcode 工程（xcodegen）==="
( cd "$ROOT/macos" && xcodegen generate )

echo "=== 4. xcodebuild 构建 .app ===="
xcodebuild -project "$ROOT/macos/AutoSync.xcodeproj" -scheme AutoSync \
  -configuration Release -derivedDataPath "$DIST/DerivedData" build >/dev/null

APP="$DIST/DerivedData/Build/Products/Release/AutoSync.app"
echo "=== 5. 拷贝引擎进 .app/Contents/MacOS/ ===="
cp "$DIST/autosync-engine" "$APP/Contents/MacOS/autosync-engine"
rm -rf "$DIST/AutoSync.app"
mv "$APP" "$DIST/AutoSync.app"
rm -rf "$DIST/DerivedData"

echo "✅ 构建完成：$DIST/AutoSync.app"
echo "   双击运行，或打包 DMG：bash macos/build-dmg.sh"
