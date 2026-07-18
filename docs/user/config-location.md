# Where AsyncAO stores your settings & data

Short version: open **Settings → Data**. It shows the exact path, says whether
you're **portable** or using the OS config folder, and has one-click "Open config
folder", "Open settings file", "Make portable", and Export/Import buttons. The
rest of this page documents what lives where.

## Your settings (and how to edit them)

Everything you configure — layout, favourites, hotkeys, colours, scales, volumes,
toggles, saved logins, learned asset formats — is one human-readable JSON file:

```
asset_preferences.json
```

### Portable-first

AsyncAO looks for that file in two places, **in this order**:

1. **Portable** — a `config/` folder right next to the AsyncAO program. If it's
   there, AsyncAO uses it. This is the default for a fresh install whenever the
   program's own folder is writable (unzipped to your Desktop, a Downloads
   folder, or a **USB stick**), so your settings travel with the program — just
   copy the whole AsyncAO folder.
2. **OS config folder** — the classic per-user location, used when no portable
   `config/` exists yet, or when the program folder is read-only (e.g. installed
   under `Program Files`):

   | OS | Location |
   |----|----------|
   | **Windows** | `%AppData%\AsyncAO\asset_preferences.json`  (`C:\Users\<you>\AppData\Roaming\AsyncAO\`) |
   | **Linux** | `~/.config/AsyncAO/asset_preferences.json` |
   | **macOS** | `~/Library/Application Support/AsyncAO/asset_preferences.json` |

Whichever folder is active also holds your per-server **case notebooks** and the
**jukebox** playlist library (each in its own file), so they always stay together.

It's plain JSON, so you can hand-edit it — **close AsyncAO first**, because it
autosaves (debounced) and would overwrite your edits on exit.

## Moving settings between PCs / going portable

- **Already on a stick / copied folder?** Nothing to do — the `config/` folder
  beside the program is found automatically.
- **Settings → Data → "Make portable"** copies your current settings (plus
  notebooks and jukebox) into a `config/` folder beside the program. Your old
  copy is left untouched, and the change takes effect the next time you launch.
- Or copy files by hand without going portable:
  - **Settings → Data → "Export settings"** writes a portable
    `asyncao-settings-*.json` bundle (everything except passwords).
  - **"Import settings…"** then drop that file onto the window on the other PC.

## Data that lives next to the program

These are written beside the AsyncAO executable (so they travel with a portable
copy), **not** in the config folder:

```
logs\           per-server chat transcripts (detailed logging)
logs\exports\   saved log-browser searches
recordings\     .aorec scene recordings / replays (imported AO2 .demo files land here too)
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
