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

The install also copies `hpm` to `~/.local/bin`, or to `$HPM_BIN_DIR` when it
is set for `herdr plugin install`, so it runs from a shell; an `hpm` already
there that is not this program is left alone. herdr hides the output of a
build that succeeds, so check with `command -v hpm`: add the directory to
`PATH` if nothing is found, and note that an `hpm` earlier on `PATH`, such as
one from `go install`, runs instead. Each update of the plugin copies it
again; uninstalling the plugin does not remove it.

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
`U` reviews every available update before any runs: what each changes, and
why one cannot run here. `space` leaves an update out, `enter` shows it in
full, and `u` applies the rest.

Every install, update and version change asks herdr for the commit its
preview showed, so the build commands that run are the ones reviewed, even
when the branch or tag has moved on since. herdr records such a plugin as
pinned to that commit, as `herdr plugin list` shows; hpm keeps the ref it
follows in its state directory, and checks that ref for updates. A change
whose ref could not be kept there fails and says how the plugin is followed
instead. A plugin installed again outside hpm follows what herdr records.

The filter of the installed list, `/`, and `hpm list` take `is:` terms along
with words: `is:update`, `is:current` and `is:failed` for what the last update
check found, `is:enabled`, `is:disabled`, `is:pinned`, `is:warning`,
`is:compatible`, `is:incompatible` and `is:local`; `-is:` keeps the plugins
not in the state. The marketplace search and `hpm search` take `is:installed`,
`is:compatible` and `is:incompatible`, and rank the rest of the search as
before.

`D` shows the diagnostics `hpm doctor` prints: the herdr command and server
and whether their versions agree, herdr's config as `herdr config check`
reports it, keys bound to plugin actions no enabled plugin declares, git,
GitHub's API allowance, the marketplace index, the history, this manager's
config, and each installed plugin that cannot run here, whose directory is
gone, or that herdr warns about. It changes nothing.

An installed plugin's details, which open after an install, and `hpm info`
say how to use it: the config directory herdr gives it, the command each
action runs by with the keys bound to it in herdr's config, and a
`[[keys.command]]` binding to add for an action no key runs.

On an installed plugin, `v` lists its versions to switch to, and reinstalls,
pins or unpins it; `z` rolls back the last change. The preview of an update
or another change first says what it is, such as a new release or new
commits on the branch the plugin follows with the same version number, then
shows the release notes, or the commit titles when there are none, and what
changes in what the plugin runs. When GitHub cannot be read, the preview
says the changes are not known.

While an install, update or uninstall runs, what herdr prints shows as it
prints it, and the status line says which update of a review is running and
for how long. A build command's own output does not show: herdr 0.9.1
captures it, drops it when the command succeeds and prints only its end when
the command fails. `ctrl+c` cancels the operation and stays in the manager:
herdr and the build it runs are interrupted, the updates not yet started are
left alone, and the output ends with where each plugin stands. An
interrupted herdr leaves its temporary checkout, a `.tmp-install-*`
directory in its plugins directory; hpm does not remove it, since herdr
offers no way to tell which one was this install's. Plugins cannot be
enabled or disabled until the operation ends. After a failure or
cancellation, `r` previews what did not succeed again before it runs. `H`
lists the recorded changes; `enter` shows one with everything herdr printed,
and `r` retries a change that failed. On the command line, `ctrl+c` stops
`hpm update` the same way, and the updates left are named for trying again.

An install takes the latest release of a plugin at the root of its
repository, else the default branch; `v` in the preview chooses another
version and shows its release notes, beside the versions or, on a narrow
screen, in their place with `tab`. On every screen `?` lists the keys the
bar at the bottom has no room for.

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
| `hpm outdated` / `hpm update [id...]` | check for and apply updates; exit with an error if any check fails; `--exclude` leaves plugins out, `--dry-run` only shows them |
| `hpm rollback <id>` | undo the last change this manager made to a plugin |
| `hpm history [id]` | changes made to plugins, with herdr's output |
| `hpm switch <id> <ref>` | install another version: a release, branch or commit |
| `hpm pin <id>` / `hpm unpin <id> [ref]` | hold a plugin at its commit, or follow a ref again |
| `hpm reinstall <id>` | reinstall the installed version |
| `hpm logs <id>` | recent commands herdr ran for a plugin |
| `hpm doctor` | check herdr, its server and config, key bindings, git, GitHub and the installed plugins |
| `hpm export [-o file]` | write the installed plugins to a file |
| `hpm restore <file> [id...]` | preview, then install the plugins of an export as exported; `--exclude` and `--dry-run` as for update |

`hpm export` writes every installed plugin as JSON: its source, the ref it
follows, the commit installed and whether it is enabled. `hpm restore` reads
that file on another machine and first lists what each plugin needs there:
an install, a change from what is installed, only enabling or disabling it,
or nothing. It also lists which plugins cannot be restored and why: a
locally linked plugin, which only `herdr plugin link` can bring back; a
source hpm cannot install; the same id installed here from another source
or linked locally; a manifest at the exported commit that cannot be read or
cannot run here. Each install is then shown in full, and nothing runs until
the plan is confirmed. herdr is asked for the exported commit, and the
plugin follows the exported ref afterwards; when that ref has moved on, the
plan says an update will be available. No plugin is installed from another
source or at another commit in its place, and plugins not in the export are
left alone. Just before each plugin is changed its record is read again, and
one that changed after the plan was made is left as it is until restore is
run again. The results say, for each plugin, whether it was restored,
unchanged, failed, cancelled, unconfirmed, not started or not restored, and
the command exits with an error unless every plugin asked for is as
exported. Each change, including one that only enables or disables a
plugin, is recorded as a restore that `hpm rollback` undoes.

The marketplace lists what each plugin is for, its stars and last push, and
whether it is installed, installed from another source or linked locally
under the same id, or cannot run here. On a screen 110 columns wide or more,
the selected plugin's platforms, minimum herdr version, version and topics
show beside the list, then its repository's stars, language and last push.
Those are GitHub's figures for the whole repository, which for a plugin in a
subdirectory holds more than that plugin, so the list gives such a plugin
no language.

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
