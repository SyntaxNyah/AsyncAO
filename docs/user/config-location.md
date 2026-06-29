# Where AsyncAO stores your settings & data

Short version: **Settings → Account/Hotkeys → "Open config folder"** jumps straight
to it, and the exact path is shown right under that button. The rest of this page
documents what lives where.

## Your settings (and how to edit them)

Everything you configure — layout, favourites, hotkeys, colours, scales, volumes,
toggles, saved logins, learned asset formats — is one human-readable JSON file:

```
asset_preferences.json
```

It lives in your OS config directory under an `AsyncAO/` folder:

| OS | Location |
|----|----------|
| **Windows** | `%AppData%\AsyncAO\asset_preferences.json`  (`C:\Users\<you>\AppData\Roaming\AsyncAO\`) |
| **Linux** | `~/.config/AsyncAO/asset_preferences.json` |
| **macOS** | `~/Library/Application Support/AsyncAO/asset_preferences.json` |

It's plain JSON, so you can hand-edit it — **close AsyncAO first**, because it
autosaves (debounced) and would overwrite your edits on exit.

The same `AsyncAO/` config folder also holds your per-server **case notebooks** and
the **jukebox** playlist library (each in its own file).

## Moving settings between PCs

You don't have to copy files by hand:

- **Settings → Account/Hotkeys → "Export settings"** writes a portable
  `asyncao-settings-*.json` bundle (everything except passwords).
- **"Import settings…"** then drop that file onto the window on the other PC.

## Data that lives next to the program

These are written beside the AsyncAO executable (so they travel with a portable
copy), **not** in the config folder:

```
logs\           per-server chat transcripts (detailed logging)
logs\exports\   saved log-browser searches
recordings\     .aorec scene recordings / replays
screenshots\    Ctrl+S / Extras → Screenshot PNGs
```

## Asset disk cache

Streamed sprites/backgrounds are cached in your OS cache directory (separate from
config, safe to delete — it just re-downloads):

| OS | Location |
|----|----------|
| **Windows** | `%LocalAppData%\AsyncAO\assets` |
| **Linux** | `~/.cache/AsyncAO/assets` |
| **macOS** | `~/Library/Caches/AsyncAO/assets` |

**Settings → Assets → Cache browser** shows live stats and has its own "open in
Explorer" button, plus clear buttons.
