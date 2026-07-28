#!/bin/bash
# build-dmg.sh 打包未签名 DMG。依赖 build-app.sh 已产出 dist/AutoSync.app。
# 用法：bash macos/build-dmg.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIST="$ROOT/dist"
VERSION="1.2.0"

if [ ! -d "$DIST/AutoSync.app" ]; then
    echo "❌ 未找到 $DIST/AutoSync.app，先运行 bash macos/build-app.sh"
    exit 1
fi

echo "=== 打包 DMG（未签名）==="
hdiutil create -volname AutoSync -srcfolder "$DIST/AutoSync.app" \
  -ov -format UDZO "$DIST/AutoSync-$VERSION.dmg"

echo "✅ DMG：$DIST/AutoSync-$VERSION.dmg"
echo "   安装说明见 macos/README-install.md（首次启动需 xattr -cr 剥离隔离属性）"
