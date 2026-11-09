# The old Wardrobe (removed)

The Wardrobe was removed whole so it can be redesigned from a confirmed list of requirements. This is everything it
showed, so nothing is forgotten. The last commit with it is `0c6fe0dc`; read any of its files with
`git show 0c6fe0dc:<path>`, for example `git show 0c6fe0dc:internal/ui/screenswardrobe.go`.

The saved data was kept on purpose: `config` still stores each server's favourites (`ServerWarm.Wardrobe`), their
folders (`ServerWarm.WardrobeFolder`), character key binds (`ServerWarm.CharKeys`) and background folders
(`FavBackgroundFolder`), so the redesign can pick up what players already saved.

Iniswapping itself stays: `setIniswap` / `activeCharName` still put the worn folder in MS `char_name`, and the theme's
`iniswap_dropdown` / `iniswap_remove` still work, listing the player's own character and the folder worn now.

## Ways in

- Courtroom **Wardrobe** button, and the theme-placeable `ctrl.wardrobe` button.
- Menu bar **Extras → Wardrobe / iniswap…** (courtroom only).
- The **Iniswap chip** in the menu bar, dropped when the bar was too narrow.
- The **Ctrl+6** hotkey ("Menu: Wardrobe") and the Extras box **Wardrobe** entry.

## The window, four tabs

### Characters (saved favourites)

- "Wearing: X" with a **Take off** button.
- Clicking a card **switched character** (took the slot); if it was taken or not on the server it was worn as an
  iniswap, with a message saying why.
- An **"Iniswap instead of switching character"** checkbox, for the session only, made clicks iniswap.
- **Search** across every folder.
- An **Add** box: a typed folder name, or `folder/char` to file it at the same time.
- The `iniswap.txt` fetch status and entry count.
- **Folders**: folder icons at the top level, click to open, "‹ All folders" to go back, a **New folder** field that
  made a temporary drop target, a hover **×** to delete a folder with a "delete with members / keep members" bar;
  an empty folder disappeared on its own.
- **Filing**: drag a card onto a folder (or onto "‹ All folders" to unfile), a **right-click menu** (move to a
  folder, unfile, new folder, remove from wardrobe), and **number keys 1–9** (0 unfiled the hovered card).
- **Card corners**: a folder tag (top-left), a **★** to add or remove (top-right), a **key badge** (bottom-left).
- **Try-before-wear preview** on hover, with `<` `>` buttons and the arrow keys to cycle the character's emotes;
  hovering also preloaded the char.ini so wearing was instant.

### Backgrounds

- The backgrounds starred in the Background picker, in folders of their own (same drag, open, delete as Characters).
- Clicking one sent `/bg`; its ★ removed it from favourites.

### Iniswaps

- A grid of the server's `<asset origin>/iniswap.txt` (one folder per line, at most 4096, fetched once per server on
  a goroutine, background folder names filtered out using the `background/` listing).
- A second source, **From your base**: the server's character roster plus the `characters/` folders in the player's
  local mounts, scanned on a goroutine, shown as name buttons without icons. Picked automatically when the server
  published no list.
- Typing a folder name and pressing **Enter** wore it even when it was not listed.
- Clicking a card always iniswapped; its ★ added it to Characters.
- With no `iniswap.txt`, a nine-line guide for server owners and players.

### Jukebox

- The music playlist library. It now has its own window (Extras box **Jukebox**, the Jukebox hotkey, and
  **Extras → Jukebox…** in the menu bar).

## Outside the window

- **Character Select → Wardrobe tab**: the favourites in the char-select grid; a click switched character, or took
  the first free slot and wore the favourite as an iniswap once the server confirmed.
- **★ on every Character Select cell** to add or remove a favourite.
- **Quick-swap** (Ctrl+J): wore the next favourite as an iniswap, starting from the one worn, wrapping around.
- **Character key binds**: per server, set from a card's key badge, fired on Alt+key with no text field focused and
  wore that character as an iniswap; listed under "Character keys" in the hotkey sheet. A bound digit took priority
  over the Alt+1–9 emote keys.
- The theme's `iniswap_dropdown` also listed the saved favourites, and `iniswap_remove` deleted the worn one from
  them.
- The old wardrobe shared by every server moved once to the first server joined (`ClaimLegacyWardrobe`).
