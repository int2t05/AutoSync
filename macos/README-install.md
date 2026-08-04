# AutoSync for macOS 安装

## 安装

1. 下载 `AutoSync-1.2.0.dmg`，双击挂载。
2. 把 `AutoSync.app` 拖到「应用程序」。
3. 首次启动需剥离隔离属性（当前未签名分发）：
   ```bash
   xattr -cr /Applications/AutoSync.app
   ```
4. 双击 `AutoSync.app`，菜单栏出现同步图标。

> macOS 15 Sequoia 起未签名 app 首次启动会被 Gatekeeper 拦截，`xattr -cr` 后可正常打开。后续版本若取得 Developer ID 公证，此步骤将移除。

## 使用

- 菜单栏图标 → 配置… 增删同步任务（目录/远程/分支/间隔/冲突策略）。
- 任务子菜单 → 手动同步 / 暂停-恢复。
- 开机自启开关：登录后自动启动到菜单栏。
- 数据目录：`~/.autosync/`。

## 卸载

1. 菜单栏图标 → 退出 AutoSync。
2. 删除 `/Applications/AutoSync.app`。
3. （可选）删除数据目录：`rm -rf ~/Library/Application\ Support/AutoSync`。

## 系统要求

- macOS 13 Ventura 或更高。
- 系统已安装 git（命令行工具或 Git for Mac）并配置凭证（SSH key 或 credential helper）。
