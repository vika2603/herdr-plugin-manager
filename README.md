# hpm

A plugin manager for [Herdr](https://herdr.dev): browse the marketplace,
preview what a plugin runs, and install, enable, disable and update plugins,
from a popup inside Herdr or from the command line.

English | [简体中文](README.zh-CN.md)

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

## Install

Requires herdr 0.9.1 or newer on macOS or Linux.

```bash
herdr plugin install vika2603/herdr-plugin-manager
```

Bind a key to the popup in `~/.config/herdr/config.toml`:

```toml
[[keys.command]]
key = "prefix+shift+p"
type = "plugin_action"
command = "vika2603.plugin-manager.open"
description = "Manage plugins"
```

For the command line alone, download the archive for your platform from the
[releases](https://github.com/vika2603/herdr-plugin-manager/releases), or run
`go install github.com/vika2603/herdr-plugin-manager/cmd/hpm@latest`.

## Usage

Open the popup, or run `hpm` with no arguments for the same interface in the
terminal. Press `?` for every key; the mouse scrolls, and a click selects,
then opens. `i` installs from the preview, which shows what the plugin runs.
An install takes the latest release of a plugin at the root of its
repository, else the default branch; `v` in the preview chooses another
version and shows its release notes.

`hpm keys` lists the keys and the config file that changes them:

```toml
[keys]
install = ["I"]
```

| Command | Does |
| --- | --- |
| `hpm list` | list installed plugins |
| `hpm search <query>` | search the marketplace |
| `hpm info <id \| owner/repo>` | show a plugin |
| `hpm install <owner/repo>` | preview and install |
| `hpm uninstall <id>` | uninstall |
| `hpm enable <id>` / `hpm disable <id>` | enable or disable |
| `hpm outdated` / `hpm update [id...]` | check for and apply updates |
| `hpm logs <id>` | recent commands herdr ran for a plugin |

The marketplace is not reviewed: read the preview before installing.

## Development

```bash
just          # test and lint
just build    # build bin/hpm
just link     # build and register this working tree with herdr
```

To release, bump the version in `herdr-plugin.toml` and `internal/app/app.go`,
then push a `v<version>` tag.
