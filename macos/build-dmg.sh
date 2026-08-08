#!/bin/bash
# build-dmg.sh 打包未签名 DMG。依赖 build-app.sh 已产出 dist/AutoSync.app。
# 用法：bash macos/build-dmg.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIST="$ROOT/dist"
# 版本号单一来源：internal/engine/engine.go 的 Version 常量（与 ready 事件、--version 一致）
VERSION="$(sed -n 's/^const Version = "\([0-9.]*\)"/\1/p' "$ROOT/internal/engine/engine.go")"

if [ ! -d "$DIST/AutoSync.app" ]; then
    echo "❌ 未找到 $DIST/AutoSync.app，先运行 bash macos/build-app.sh"
    exit 1
fi

echo "=== 打包 DMG（未签名）==="
hdiutil create -volname AutoSync -srcfolder "$DIST/AutoSync.app" \
  -ov -format UDZO "$DIST/AutoSync-$VERSION.dmg"

echo "✅ DMG：$DIST/AutoSync-$VERSION.dmg"
echo "   安装说明见 macos/README-install.md（首次启动需 xattr -cr 剥离隔离属性）"
