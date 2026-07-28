#!/bin/bash
# install-linux.sh 安装 AutoSync 到 ~/.local/bin，配置模板到 ~/.config/AutoSync/。
# 装完运行 `autosync install` 注册 systemd 开机自启。用法：bash install-linux.sh
set -euo pipefail

BIN_DIR="$HOME/.local/bin"
CONF_DIR="$HOME/.config/AutoSync"
mkdir -p "$BIN_DIR" "$CONF_DIR"

cp autosync "$BIN_DIR/autosync"
chmod +x "$BIN_DIR/autosync"

if [ ! -f "$CONF_DIR/autosync.conf.yaml" ]; then
    cp autosync.conf.example.yaml "$CONF_DIR/autosync.conf.yaml"
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
