# Testing guide — since v1.98.1

Run `bin\asyncao.exe`. Each item below is two sentences: do the first, check the
second.

---

## #122 — Immediate flag ships only with a preanim
Join the same room with a second client, keep **Immediate** on, turn **Pre** off, and send a message. Send again with **Pre** on and the preanim can't be interrupted; turn **Pre** back off and it can again.
- [ ] done

## #123 — Inline colour shows in the IC log
Type a message with a colour code in the middle (select text and pick a colour, or type `\cp`). Send it and check the IC log shows that part coloured, with no raw code, matching the chatbox.
- [ ] done

## #128 — Auto-mount the packages/ folder
Create a `packages/` folder next to `asyncao.exe`, drop a content pack subfolder inside, and relaunch. Its sprites/sounds should resolve with no manual mount, a `packages/sorting.txt` (one folder name per line, first = highest) should set the order, manual mounts should still win, and it should show read-only in Settings → Assets → Advanced.
- [ ] done

## #129 — Log search (Ctrl+F)
In court, press **Ctrl+F** and confirm the log search field takes focus. Type a word and check matches highlight in the IC log, case-insensitively.
- [ ] done

## #130 — Mid-text emotes
On one client, type `hello :emote: world` and send it. Your chatbox/log shows clean text while a second AsyncAO client plays the emote inline, and unknown `:tokens:`, URLs, and `12:30` stay literal.
- [ ] done

## #131 — Text-FX inserts at the caret
Open the FX picker, put the caret mid-sentence, and click Slow down, Speed up, Shake, Flash, Pause, Bold, and Italic one at a time. Each code should insert at the caret (not the end) and send/render correctly.
- [ ] done

## Caret drops focus when the window loses it
Click into the IC or OOC box so the caret blinks, then click away to another window. The caret should stop blinking and typing should go to the other app; click back and the box refocuses.
- [ ] done

## Disable the Alt+1..9 emote number row
Toggle "Disable the Alt+1..9 emote number row" on in Settings and press Alt+1..9 in court. They should type digits; toggle it back off and Alt+1..9 selects on-screen emotes again.
- [ ] done

## Torn-off tabs bring to front
Pop two or more tabs out into floating panels, then click a different one. It should rise above the rest and stay there (the Players tab shouldn't always draw on top).
- [ ] done

## Evidence — stream from server
Open Evidence and click **"Stream from server"** right under the icon-size slider. Images should switch between local folders and the server's `evidence/` on each click, and fall back to streaming when no local mounts exist.
- [ ] done

## Evidence — server picker
With streaming on, click **"Server…"** next to the Image-file field and wait for the index. Scroll with the wheel (a full row per notch) and drag the scrollbar, then click a thumbnail to fill the field and close; with streaming off the button reads "Browse…".
- [ ] done

## Changelog note
Open **What's New / Changelog** and read the "Evidence images: local by default, one click to stream" entry. It should say the toggle is right under the icon-size slider and local mode shows no server images until you flip it.
- [ ] done
