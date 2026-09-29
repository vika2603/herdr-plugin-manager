# Interface design

The popup and the terminal manager share one interface, drawn by
`internal/ui`. These rules keep its screens consistent; `theme.go` and
`components.go` implement them.

## Principles

- **One accent, for focus only.** The selection bar, the active tab and its
  underline, the screen's main key, the search prompt, search matches and the
  dialog border. Nothing else uses it.
- **State colours for state only.** `ok` for enabled, installed and done;
  `warn` for what needs attention, such as an update or commands that run
  during install; `danger` for errors and what blocks an install.
- **Grey levels for everything else.** Three text levels, a rule and a
  surface. Headings are set apart by weight and case, not by a colour.
- **Glyphs and alignment carry structure.** A state is a glyph and a word,
  never a filled block. Label and value rows share one label column.

## Colour roles

Every colour is named by what it is for. Each role has a value for a dark
and a light terminal background; `mode = "auto"` picks between them from the
background the terminal reports, and a herdr popup that reports none is
drawn dark.

| Role | Used for | Dark | Light |
| --- | --- | --- | --- |
| `accent` | focus, as above | indigo `#8B93FF` | indigo `#4B55D6` |
| `fg` | body text, names, commands | `#E6E6EA` | `#1C1C22` |
| `fg2` | descriptions, key descriptions, intros | `#A3A3AE` | `#5C5C68` |
| `fg3` | versions, counts, labels, breadcrumbs, hints | `#6C6C78` | `#8E8E9A` |
| `rule` | rules and the thin part of tab underlines | `#33333D` | `#DCDCE3` |
| `surface` | background of secondary key chips | `#2B2B34` | `#ECECF1` |
| `ok` | enabled, installed, done | `#3FB68B` | `#1E8E63` |
| `warn` | updates, commands that run, warnings | `#E8B04B` | `#A86A00` |
| `danger` | errors, problems | `#F0616D` | `#C8323F` |
| `on_accent` | text on the main key chip | `#17171C` | `#FFFFFF` |

A selected item's description is drawn in a mix of `accent` and `fg2`, so it
follows the accent. The accent presets are `indigo` (the default), `teal`
(`#3CC8B4` / `#0F8C7D`) and `magenta` (`#F25D94` / `#D6336C`). On a
256-colour terminal lipgloss picks the nearest colours; the three text levels
stay distinct.

## Components

| Component | Drawn as |
| --- | --- |
| Tab row | names with counts, the active one bold in the accent over a thick accent underline |
| List item | two lines and a gap: name, version in `fg3`, marks; then the description in `fg2`. The selected item has `▌` in column 2, a bold name and an accent-tinted description. A marketplace item's marks always fit: the stars go first, then the version and the name are cut short. Its description comes before the language and last push in `fg3`, which show only when the whole description fits beside them; a plugin in a subdirectory shows no language, and its push is called the repository's |
| Listing | on a screen 110 columns wide or more, the selected marketplace plugin beside the list, after a `│` in `rule`: the plugin's own field rows, a status row only when it is installed or another install has its id, then after a blank row GitHub's figures for its repository in the same label column, one per row. A listing taller than the screen ends with a line saying enter shows the rest |
| Mark | glyph and word in its role's colour: `● enabled` `○ disabled` `↑ v1.1.0` `◆ local` `✓ installed` `✕ check failed` `▲ 2 warnings` `◇ pre-release` |
| Title line | bold name, then `version · id` in `fg3`, then a mark |
| Field rows | upper-case labels in `fg3` in one column as wide as the longest, values after two spaces, wrapped values aligned under the value column |
| Section | an upper-case label in `fg3`, an optional note after it (in `warn` when it says what runs), then its lines indented to column 5 |
| Key chips | the screen's main action on an accent chip, the others on `surface`, descriptions in `fg2` |
| Status line | `✓` in `ok` then the message, `✕` and the message in `danger`, or the spinner and the work in progress in `fg2`. A message clears itself after 5 seconds, an error after 10 |
| Dialog | a rounded accent border, the question in bold, the confirm key as a main chip |

## Layout

- Content starts in column 3; what belongs to a section starts in column 5.
  List items start in column 4, since column 2 holds the selection bar.
- One blank line between sections; list items are two lines and a gap.
- Glyphs are all one cell wide, so columns line up in any terminal: `▌`
  selection, `›` breadcrumb, `●` `○` enabled state, `↑` update, `◆` local,
  `✓` `✕` result, `▲` warning, `◇` pre-release, `★` stars, `·` separator.
  No emoji.

## Configuration

The `[theme]` table of the config file (`hpm keys` prints its path) picks
the accent and the mode, and can replace any role on either background:

```toml
[theme]
accent = "teal"    # indigo, teal, magenta or "#RRGGBB"
mode = "auto"      # auto, dark or light

[theme.dark]
fg = "#DADADA"
```

An unknown role, a value that is not `#RRGGBB` or an unknown accent is
reported on the status line, and the default colours are used.
