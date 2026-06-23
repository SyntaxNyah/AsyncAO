# Discord Rich Presence

AsyncAO can show what you're playing on your Discord profile — the
server, your character, your showname, your area — with **per-field
privacy checkboxes** and **zero build- or run-time dependency on
Discord**. This document is the complete setup, privacy, and
troubleshooting reference, plus the plan for shipping a default
application ID later.

## What it looks like

> **Playing AsyncAO**
> Skrapegropen
> Nyah as Phoenix — Courtroom 1
> `[AsyncAO icon]` · elapsed time since connect

Line composition (all optional, see Privacy):

| Field | Source | Default when enabled |
|---|---|---|
| Server | the lobby entry / direct-connect host | shown |
| Character | the folder you're wearing (iniswaps show the swap) | shown |
| Showname | `effectiveShowname()` — the courtroom override box, falling back to the saved Settings name | shown |
| Area | the current area from the AO area list | **hidden** |
| Elapsed | time since the session handshake finished | shown with any field |

## Enabling it

1. **Create a Discord application** (one-time, ~2 minutes):
   - Open <https://discord.com/developers/applications> → *New
     Application* → name it **AsyncAO** (the name is what profiles show
     after "Playing").
   - *Rich Presence → Art Assets* → upload the AsyncAO icon under the
     asset key **`appicon`** (exactly that string — the client
     references it as `largeImageKey`).
   - Copy the **Application ID** from *General Information*.
2. In AsyncAO: **Settings → Discord** → tick *Enable*, paste the
   Application ID into **App ID**.
3. Restart AsyncAO (the ID is read when the presence worker starts).

No Discord SDK is installed and nothing is downloaded: the client talks
to your locally running Discord app over its IPC pipe
(`\\.\pipe\discord-ipc-0..9` on Windows,
`$XDG_RUNTIME_DIR/discord-ipc-0..9` elsewhere) using the documented
handshake (`op 0`) / activity (`op 1` `SET_ACTIVITY`) frames.

## Privacy model

- **Off by default.** Fresh installs never touch the pipe.
- When you enable it, the default field set is **server + character +
  showname**; **area is off** until you opt in (some people don't want
  their room broadcast).
- Each field has its own checkbox in Settings → Discord; unticked
  fields are omitted from the payload entirely (not blanked — absent).
- Disconnecting, returning to the lobby, or disabling the toggle clears
  the activity immediately.
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
  entirely; the package becomes a no-op stub with the same API, and the
  Settings section explains it was built out. CI publishes a prebuilt
  `asyncao-<platform>-nodiscord` artifact for this — no need to build it
  yourself.

## Troubleshooting

| Symptom | Cause / fix |
|---|---|
| Settings shows "idle — no application ID" | Paste an App ID and restart; without one the worker never dials. |
| "Discord not detected (retrying)" | Discord isn't running, or it's the browser client (no IPC pipe). Start the desktop app; the worker probes pipes 0–9 every 30 s while an update is pending. |
| Presence shows but no icon | The art asset isn't named exactly `appicon`, or assets are still propagating (Discord caches; give it a few minutes / restart Discord). |
| Activity sticks after disconnect | The clear is sent on disconnect; if Discord missed it (pipe died), it expires with the Discord-side timeout — reconnecting AsyncAO overwrites it. |
| Profile shows the app name only | All field checkboxes are unticked — the activity carries no Details/State lines. |

## Plan: shipping a default application ID

Today every user creates their own Discord application because the
project doesn't ship one. The intended end state:

1. Register a single project-owned "AsyncAO" application (owner: the
   project maintainer account), upload `appicon` once.
2. Bake its ID into `defaultDiscordPrefs` in
   `internal/config/preferences.go` as the **default** `AppID` — the
   Settings field stays, so users can still substitute their own app
   (custom name/icon) and the empty-string→idle behavior is preserved
   for `nodiscord`-adjacent distro patches.
3. Document the baked ID here and drop step 1 of *Enabling it*.

Nothing in the code needs to change for this beyond the one default —
the worker, Settings UI, and per-field privacy all already key off the
preference value. It is deliberately **not** done yet so the published
ID is created under the project account rather than a contributor's
personal one.
