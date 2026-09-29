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

安装时还会把 `hpm` 复制到 `~/.local/bin`；为 `herdr plugin install` 设置了 `$HPM_BIN_DIR` 时则复制到该目录，
这样可以在 shell 里直接运行 `hpm`。该位置已有不属于本程序的 `hpm` 时不会覆盖。herdr 不显示成功构建的输出，
请用 `command -v hpm` 确认：找不到时把该目录加入 `PATH`；`PATH` 中更靠前的 `hpm`（例如 `go install` 安装的）会优先运行。
每次更新插件都会重新复制；卸载插件不会删除这个副本。

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
在预览里按 `v` 可以选择其他版本，并查看它的 release notes：宽屏时显示在版本列表旁边，窄屏时按 `tab` 在列表与 release notes 之间切换。在任何界面按 `?` 都能列出底部按键栏放不下的按键。

按 `U` 会在执行前审阅所有可用更新：每个更新改变了什么，以及哪些无法在当前环境运行。`space` 排除某个更新，`enter` 查看完整内容，`u` 应用其余更新。

每次安装、更新和版本变更都让 herdr 安装预览时显示的那个 commit，所以运行的构建命令就是审阅过的那些，即使分支或 tag 之后又有移动。herdr 会把这样的插件记为固定在该 commit（`herdr plugin list` 中可见）；hpm 在状态目录里记下它跟踪的 ref，并按这个 ref 检查更新；这个 ref 没能记下时，变更按失败报告，并说明插件实际的跟踪方式。在 hpm 之外重新安装的插件，以 herdr 的记录为准。已安装列表的筛选（`/`）和 `hpm list` 除关键词外还接受 `is:` 条件：`is:update`、`is:current`、`is:failed` 对应最近一次更新检查的结果，另有 `is:enabled`、`is:disabled`、`is:pinned`、`is:warning`、`is:compatible`、`is:incompatible` 和 `is:local`；`-is:` 表示排除该状态。插件市场搜索和 `hpm search` 接受 `is:installed`、`is:compatible` 和 `is:incompatible`，其余搜索词的排序方式不变。

按 `D` 显示与 `hpm doctor` 相同的诊断：herdr 命令与 server 及二者版本是否一致、`herdr config check` 报告的 herdr 配置问题、绑定到不存在或已禁用插件 action 的按键、git、GitHub API 剩余额度、插件市场索引、变更历史、本管理器的配置，以及无法在当前环境运行、目录缺失或 herdr 对其给出警告的已安装插件。诊断不做任何修改。

已安装插件的详情（安装完成后会自动打开）和 `hpm info` 会说明如何使用它：herdr 为它提供的配置目录、每个 action 的调用 id 及 herdr 配置中已绑定的按键，并为尚未绑定按键的 action 给出可加入的 `[[keys.command]]` 配置。对已安装的插件，`v` 列出可切换的版本，也可以重装、固定或解除固定；`z` 回退最近一次变更。更新或其他变更的预览先说明变更的性质，例如新的 release，或者所跟踪分支上有新提交而版本号不变；然后展示 release notes（没有时展示提交标题），以及插件会运行的内容有哪些变化。无法读取 GitHub 时，预览会说明变化内容未知。

安装、更新或卸载运行时，herdr 的输出会实时显示，状态栏会说明审阅中的哪个更新正在运行以及已运行多久。实时显示的是 herdr 自身打印的内容；构建命令自己的输出无法逐行实时看到，因为 herdr 0.9.1 会捕获它，命令成功时丢弃，失败时只打印末尾部分。`ctrl+c` 取消当前任务但不退出管理器：herdr 及其运行的构建被中断，尚未开始的更新不会执行，输出末尾说明每个插件的实际状态。被中断的 herdr 会留下临时 checkout，即其插件目录中的 `.tmp-install-*` 目录；herdr 没有提供判断哪个目录属于本次安装的方式，所以 hpm 不删除它。任务结束前不能启用或禁用插件。任务失败或被取消后，按 `r` 会重新预览未成功的部分，确认后再执行。`H` 列出记录的变更；`enter` 查看某次变更及 herdr 的全部输出，`r` 重试失败的变更。在命令行中，`ctrl+c` 以同样方式停止 `hpm update`，并列出剩余插件以便重试。

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
| `hpm outdated` / `hpm update [id...]` | 检查并应用更新；任一检查失败时以错误退出；`--exclude` 排除插件，`--dry-run` 只展示不执行 |
| `hpm rollback <id>` | 撤销本管理器对插件做的最近一次变更 |
| `hpm history [id]` | 对插件做过的变更，以及 herdr 的输出 |
| `hpm switch <id> <ref>` | 安装另一个版本：release、分支或 commit |
| `hpm pin <id>` / `hpm unpin <id> [ref]` | 固定在当前 commit，或重新跟踪某个 ref |
| `hpm reinstall <id>` | 重装当前版本 |
| `hpm logs <id>` | herdr 最近为插件运行的命令 |
| `hpm doctor` | 检查 herdr、server 与配置、按键绑定、git、GitHub 和已安装插件 |
| `hpm export [-o 文件]` | 把已安装的插件写入文件 |
| `hpm restore <文件> [id...]` | 先预览，再按导出时的状态安装导出文件中的插件；`--exclude` 和 `--dry-run` 与 update 相同 |

`hpm export` 把每个已安装的插件写成 JSON：来源、跟踪的 ref、已安装的 commit，以及是否启用。`hpm restore` 在另一台机器上读取这个文件，先列出每个插件在当前环境需要做什么：安装、从已安装的版本变更、只启用或禁用，或者无需变动；同时列出哪些插件无法恢复及原因：本地链接的插件只能用 `herdr plugin link` 重新链接；来源是 hpm 无法安装的类型；同一 id 在当前环境装自其他来源或以本地链接存在；导出 commit 处的 manifest 无法读取或无法在当前环境运行。随后逐个展示每次安装的完整内容，确认计划后才会执行。herdr 被要求安装导出时的 commit，插件之后跟踪导出时的 ref；该 ref 已经移动时，计划会说明恢复后会有可用更新。hpm 不会改用其他来源或其他 commit 代替导出的内容，也不改动导出文件之外的插件。每个插件变更前会重新读取它的记录；计划之后发生变化的插件保持原样，需要重新运行 restore 审阅。结果逐个说明插件是已恢复、无需变动、失败、已取消、未确认、未开始还是未恢复；只要有插件没有达到导出时的状态，命令就以错误退出。每次变更（包括只启用或禁用插件）都记为一次 restore，可用 `hpm rollback` 撤销。

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
