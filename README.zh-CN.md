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

只用命令行时，从 [releases](https://github.com/vika2603/herdr-plugin-manager/releases) 下载对应平台的压缩包，
或运行 `go install github.com/vika2603/herdr-plugin-manager/cmd/hpm@latest`。

## 使用

打开弹窗，或不带参数运行 `hpm`，在终端里打开同样的界面。按 `?` 查看所有按键；鼠标可以滚动，单击选中，再次单击打开。
在预览里按 `i` 安装，预览会列出插件将运行的命令。安装位于仓库根目录的插件时默认装最新的 release，其他情况装默认分支；
在预览里按 `v` 可以选择其他版本，并查看它的 release notes。对已安装的插件，`v` 列出可切换的版本，也可以重装、固定或解除固定；`z` 回退最近一次变更。更新或其他变更的预览先说明变更的性质，例如新的 release，或者所跟踪分支上有新提交而版本号不变；然后展示 release notes（没有时展示提交标题），以及插件会运行的内容有哪些变化。无法读取 GitHub 时，预览会说明变化内容未知。

`hpm keys` 列出所有按键，以及用来修改按键和颜色的配置文件：

```toml
[keys]
install = ["I"]

[theme]
accent = "teal"    # indigo、teal、magenta 或 "#RRGGBB"
```

界面的配色和组件规则见 [docs/design.md](docs/design.md)。

| 命令 | 作用 |
| --- | --- |
| `hpm list` | 列出已安装的插件 |
| `hpm search <关键词>` | 搜索插件市场 |
| `hpm info <id \| owner/repo>` | 查看插件 |
| `hpm install <owner/repo>` | 预览并安装 |
| `hpm uninstall <id>` | 卸载 |
| `hpm enable <id>` / `hpm disable <id>` | 启用或禁用 |
| `hpm outdated` / `hpm update [id...]` | 检查并应用更新；任一检查失败时以错误退出 |
| `hpm rollback <id>` | 撤销本管理器对插件做的最近一次变更 |
| `hpm history [id]` | 对插件做过的变更，以及 herdr 的输出 |
| `hpm switch <id> <ref>` | 安装另一个版本：release、分支或 commit |
| `hpm pin <id>` / `hpm unpin <id> [ref]` | 固定在当前 commit，或重新跟踪某个 ref |
| `hpm reinstall <id>` | 重装当前版本 |
| `hpm logs <id>` | herdr 最近为插件运行的命令 |

插件市场列表展示每个插件的用途、star 数和最近推送时间，并标明它是否已安装、同一 id 是否装自其他来源或以本地链接存在，以及能否在当前环境运行。窗口宽度不少于 110 列时，列表旁边先显示选中插件的平台、最低 herdr 版本、版本和 topics，再显示所在仓库的 star 数、语言和最近推送时间。这些是 GitHub 对整个仓库的统计；位于子目录的插件与其他内容共用仓库，所以列表不为它显示语言。

插件市场未经审核，安装前请先看预览。

herdr 安装的插件总是注册为启用，所以对已禁用插件的更新、回退或其他重装会在之后重新禁用它；这需要正在运行的 herdr server，没有 server 时变更会在执行任何操作前被拒绝。安装失败时已安装的插件保持原样，每次变更都会报告插件之后的状态；无法读取该状态时，变更记为未确认，而不是完成。变更记录在 `$XDG_STATE_HOME/herdr-plugin-manager`（默认 `~/.local/state/herdr-plugin-manager`），供 `hpm rollback` 和 `hpm history` 读取。

## 开发

```bash
just          # 测试并运行 lint
just build    # 构建 bin/hpm
just link     # 构建并把当前工作目录注册到 herdr
```

发布新版本时，修改 `herdr-plugin.toml` 和 `internal/app/app.go` 里的版本号，然后推送 `v<版本号>` tag。
