#!/bin/bash
# install-linux.sh 安装 AutoSync 到 ~/.local/bin，配置模板到 ~/.autosync/。
# 装完运行 `autosync install` 注册 systemd 开机自启。用法：bash install-linux.sh
# 脚本与 autosync 二进制、配置模板同目录（tarball 布局），不依赖调用时 CWD。
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BIN_DIR="$HOME/.local/bin"
CONF_DIR="$HOME/.autosync"
mkdir -p "$BIN_DIR" "$CONF_DIR"

# 覆盖运行中 daemon 的二进制会使 systemd ExecStart 指向旧 inode，先提示停止
if [ -x "$BIN_DIR/autosync" ] && systemctl --user is-active autosync >/dev/null 2>&1; then
    echo "⚠️ AutoSync 守护正在运行，先停止再安装：systemctl --user stop autosync"
    exit 1
fi

cp "$SCRIPT_DIR/autosync" "$BIN_DIR/autosync"
chmod +x "$BIN_DIR/autosync"

if [ ! -f "$CONF_DIR/autosync.conf.yaml" ]; then
    cp "$SCRIPT_DIR/autosync.conf.example.yaml" "$CONF_DIR/autosync.conf.yaml"
    echo "已写入配置模板：$CONF_DIR/autosync.conf.yaml（请编辑填写同步任务）"
else
    echo "配置已存在，跳过：$CONF_DIR/autosync.conf.yaml"
fi

cat <<EOF

✅ AutoSync 已安装到 $BIN_DIR/autosync

下一步：
  1. 编辑配置：\$EDITOR $CONF_DIR/autosync.conf.yaml
  2. 前台试运行：autosync daemon
  3. 开机自启（systemd user service）：
       autosync install
       loginctl enable-linger \$USER   # 开机即启，无需登录

若 $BIN_DIR 不在 PATH，加到 ~/.bashrc：export PATH="\$HOME/.local/bin:\$PATH"
EOF
