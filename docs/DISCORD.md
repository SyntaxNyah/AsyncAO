# Discord Rich Presence

AsyncAO can show what you're playing on your Discord profile — the
server, your character, your showname, your area — with **per-field
privacy checkboxes** and **zero build- or run-time dependency on
Discord**. The official AsyncAO application ID is **baked in**, so it
works the moment you tick it on — no developer-portal setup. This
document is the complete setup, privacy, and troubleshooting reference.

## What it looks like

> **Playing AsyncAO**
> Skrapegropen
> Nyah as Phoenix — Courtroom 1
> `[AsyncAO icon]` · elapsed time since connect

While you're in the lobby / server list (not yet in a room) it stays up as a
plain **Playing AsyncAO — In the lobby** (no server/character/timer), so your
profile shows it for the whole session, not only in-court.

Line composition in a room (all optional, see Privacy):

| Field | Source | Default when enabled |
|---|---|---|
| Server | the lobby entry / direct-connect host | shown |
| Character | the folder you're wearing (iniswaps show the swap) | shown |
| Showname | `effectiveShowname()` — the courtroom override box, falling back to the saved Settings name | shown |
| Area | the current area from the AO area list | **hidden** |
| Elapsed | time since the session handshake finished | shown with any field |

## Enabling it

Nothing to set up — the official **AsyncAO** application ID is baked
into the client.

1. Make sure the **Discord desktop app is running** (the browser client
   has no IPC pipe, so it can't show presence).
2. In AsyncAO: **Settings → Discord** — *Enable* is **already ticked** on
   a normal build, so presence is on out of the box. Untick it there if
   you'd rather it stay off, and tick/untick the per-field boxes (server,
   character, showname, area) to choose what's shown.

That's it. The app identity isn't user-editable (there's no App ID box) —
it's always the official AsyncAO app so the name and icon are consistent
for everyone. Want a *different* identity (custom name/icon)? Edit
`DefaultDiscordAppID` in `internal/config/preferences.go` and rebuild.

No Discord SDK is installed and nothing is downloaded: the client talks
to your locally running Discord app over its IPC pipe
(`\\.\pipe\discord-ipc-0..9` on Windows,
`$XDG_RUNTIME_DIR/discord-ipc-0..9` elsewhere) using the documented
handshake (`op 0`) / activity (`op 1` `SET_ACTIVITY`) frames.

## Privacy model

- **Rich Presence is ON by default on Discord-capable builds, and now
  works out of the box** — the official Application ID is baked in, so a
  fresh install with Discord running shows presence immediately (no ID to
  paste). Untick *Enable* in Settings → Discord to turn it off; that gate
  is enforced live (it clears the activity the moment you untick it).
  `-tags nodiscord` builds carry no Discord code at all, and a saved prefs
  file keeps whatever you last chose (this default only seeds fresh installs).
- When you enable it, the default field set is **server + character +
  showname**; **area is off** until you opt in (some people don't want
  their room broadcast).
- Each field has its own checkbox in Settings → Discord; unticked
  fields are omitted from the payload entirely (not blanked — absent).
- Disconnecting or returning to the lobby drops the room details back to the
  neutral "In the lobby" line; **disabling the toggle clears the activity
  entirely**, immediately.
- Nothing is sent anywhere except the local Discord client's pipe.

## Building without Discord

Covered in [BUILDING.md](../BUILDING.md#discord-never-required), short
version:

- The default build has **no Discord dependency** — `internal/presence`
  is pure standard library. With Discord not running, the worker idles
  (one paced probe per 30 s, and only while an update is pending).
- **There is no Discord DLL.** The integration is compiled straight into
  the AsyncAO binary as pure Go — there is no separate file to delete or
  manage, and nothing Discord-related to "turn off" at the filesystem
  level (the Settings toggle does that). The default build therefore
  *cannot* fail to boot because of Discord, whether or not Discord is
  installed.
  > ⚠️ The DLLs shipped next to `asyncao.exe` on Windows
  > (`SDL2.dll`, `libwebp`, `libavif`, …) are the rendering/audio engine —
  > **the client needs those to run at all.** Don't delete them expecting
  > to remove Discord; that just breaks the app. To get a binary with the
  > Discord code gone, use the `-nodiscord` build below instead.
- `go build -tags nodiscord ./cmd/asyncao` compiles the integration out
  **entirely**: `internal/presence` becomes a no-op stub, and the whole
  **Settings → Discord** section is compiled out too (it lives in a
  build-tagged file). So a Discord-free binary carries **no Discord code at
  all** — not even the settings UI — and is a touch smaller. CI publishes a
  prebuilt `asyncao-<platform>-nodiscord` artifact for every platform (in the
  **Actions** tab → latest run → **Artifacts**; downloading needs a free GitHub
  account), so you don't have to build it yourself.

## Troubleshooting

| Symptom | Cause / fix |
|---|---|
| Settings shows "off" | *Enable* is unticked in Settings → Discord — tick it (it's on by default). The app ID is baked in, so there's nothing else to set. |
| "Discord not detected (retrying)" | Discord isn't running, or it's the browser client (no IPC pipe). Start the desktop app; the worker probes pipes 0–9 every 30 s while an update is pending. |
| Presence shows but no icon | The art asset isn't named exactly `appicon`, or assets are still propagating (Discord caches; give it a few minutes / restart Discord). |
| Activity sticks after disconnect | The clear is sent on disconnect; if Discord missed it (pipe died), it expires with the Discord-side timeout — reconnecting AsyncAO overwrites it. |
| Profile shows the app name only | All field checkboxes are unticked — the activity carries no Details/State lines. |

## The baked-in application ID

The official **AsyncAO** Discord application is registered under the
project maintainer's account, and its ID ships in the client as
`config.DefaultDiscordAppID` (`internal/config/preferences.go`). The
presence worker is dialed with that constant directly in
`cmd/asyncao/main.go`, so it applies to **everyone** — including existing
installs whose saved prefs predate the bake (their old per-user `AppID`
field is ignored; the constant wins).

There is intentionally **no Settings box to change the ID**: the app
identity (name "AsyncAO", icon asset `appicon`) is meant to be consistent
for every player. A distro or fork that wants a different identity edits
the one constant and rebuilds — the `Enabled` toggle and per-field
privacy are unchanged and still key off the saved preference.
