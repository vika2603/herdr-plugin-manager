# hpm

[Herdr](https://herdr.dev) 的插件管理器：浏览插件市场，查看插件会运行哪些命令，
安装、启用、禁用和更新插件。可以在 Herdr 的弹窗里使用，也可以在命令行里使用。

[English](README.md) | 简体中文

```
  Installed 4    Marketplace 1389                                herdr 0.9.1
─━━━━━━━━━━━━━───────────────────────────────────────────────────────────────
  Plugins installed or linked in herdr. Press tab for the marketplace.

 / filter installed plugins                                        4 plugins

 │ Auto Title 0.5.0  update → bb74c54
 │ herdr.auto-title · kryptamine/herdr-auto-title@899ee4e

   Machine Manager 0.2.1
   herdr.machine-manager · vika2603/herdr-machine-manager@v0.2.1

 1 update available · u to review
  enter  details    tab  marketplace    space  enable/disable    ?  more
```

## 安装

需要 herdr 0.9.1 或更新版本，macOS 或 Linux。

```bash
herdr plugin install vika2603/herdr-plugin-manager
```

在 `~/.config/herdr/config.toml` 里给弹窗绑定按键：

```toml
[[keys.command]]
key = "prefix+shift+p"
type = "plugin_action"
command = "vika2603.plugin-manager.open"
description = "Manage plugins"
```

只用命令行时，从 [releases](https://github.com/vika2603/herdr-plugin-manager/releases) 下载 `hpm`，
或运行 `go install github.com/vika2603/herdr-plugin-manager/cmd/hpm@latest`。

## 使用

打开弹窗，或不带参数运行 `hpm`，在终端里打开同样的界面。按 `?` 查看所有按键。

| 命令 | 作用 |
| --- | --- |
| `hpm list` | 列出已安装的插件 |
| `hpm search <关键词>` | 搜索插件市场 |
| `hpm info <id \| owner/repo>` | 查看插件 |
| `hpm install <owner/repo>` | 预览并安装 |
| `hpm uninstall <id>` | 卸载 |
| `hpm enable <id>` / `hpm disable <id>` | 启用或禁用 |
| `hpm outdated` / `hpm update [id...]` | 检查并应用更新 |
| `hpm logs <id>` | herdr 最近为插件运行的命令 |

插件市场未经审核，安装前请先看预览。

## 开发

```bash
just          # 测试并运行 lint
just build    # 构建 bin/hpm
just link     # 构建并把当前工作目录注册到 herdr
```

发布新版本时，修改 `herdr-plugin.toml` 和 `internal/app/app.go` 里的版本号，然后推送 `v<版本号>` tag。
