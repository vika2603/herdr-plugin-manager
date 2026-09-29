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
On an installed plugin, `v` lists its versions to switch to, and reinstalls,
pins or unpins it; `z` rolls back the last change. The preview of an update
or another change first says what it is, such as a new release or new
commits on the branch the plugin follows with the same version number, then
shows the release notes, or the commit titles when there are none, and what
changes in what the plugin runs. When GitHub cannot be read, the preview
says the changes are not known.
An install takes the latest release of a plugin at the root of its
repository, else the default branch; `v` in the preview chooses another
version and shows its release notes.

`hpm keys` lists the keys and the config file that changes them and the
colours:

```toml
[keys]
install = ["I"]

[theme]
accent = "teal"    # indigo, teal, magenta or "#RRGGBB"
```

[docs/design.md](docs/design.md) describes the interface's colours and
components.

| Command | Does |
| --- | --- |
| `hpm list` | list installed plugins |
| `hpm search <query>` | search the marketplace |
| `hpm info <id \| owner/repo>` | show a plugin |
| `hpm install <owner/repo>` | preview and install |
| `hpm uninstall <id>` | uninstall |
| `hpm enable <id>` / `hpm disable <id>` | enable or disable |
| `hpm outdated` / `hpm update [id...]` | check for and apply updates; exit with an error if any check fails |
| `hpm rollback <id>` | undo the last change this manager made to a plugin |
| `hpm history [id]` | changes made to plugins, with herdr's output |
| `hpm switch <id> <ref>` | install another version: a release, branch or commit |
| `hpm pin <id>` / `hpm unpin <id> [ref]` | hold a plugin at its commit, or follow a ref again |
| `hpm reinstall <id>` | reinstall the installed version |
| `hpm logs <id>` | recent commands herdr ran for a plugin |

The marketplace lists what each plugin is for, its stars, the repository's
language and last push, and whether it is installed or cannot run here. On
a screen 110 columns wide or more, the selected plugin's platforms, minimum
herdr version, topics, source and version show beside the list. The
language, stars and dates are the repository's, which for a plugin in a
subdirectory holds more than that plugin.

The marketplace is not reviewed: read the preview before installing.

herdr registers every plugin it installs as enabled, so an update, rollback
or other reinstall of a disabled plugin disables it again afterwards; that
needs a running herdr server, and without one the change is refused before
anything runs. A failed install leaves the installed plugin as it was, and
each change reports the plugin's state after it; when that cannot be read,
the change counts as unconfirmed, not done. Changes are recorded in
`$XDG_STATE_HOME/herdr-plugin-manager` (`~/.local/state/herdr-plugin-manager`
by default), which `hpm rollback` and `hpm history` read.

## Development

```bash
just          # test and lint
just build    # build bin/hpm
just link     # build and register this working tree with herdr
```

To release, bump the version in `herdr-plugin.toml` and `internal/app/app.go`,
then push a `v<version>` tag.
