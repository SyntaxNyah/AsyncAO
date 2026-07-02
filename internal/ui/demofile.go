package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// AO2 .demo backwards compatibility ("it's backwards COMPATIBILITY baby"):
// read the demo files AO2's built-in recorder writes — play them in the replay
// player, edit them in the Scene Maker, export them to GIF/WebP/Video/Comic —
// and write our recordings BACK out as .demo so AO2 users can watch them.
//
// The format (canonical: ../AO2-Client/src/demoserver.cpp): a text file of raw
// SERVER→client packets, one per line (a packet may span lines — the loader
// joins until the line ends with '%'), with "wait#<ms>#%" packets carrying the
// timing and usually an "SC#…#%" char list first. Pre-2.9.1 files have the
// wait-desync bug (waits recorded one slot late — AO2 PR #496); AO2 detects
// those by "starts with SC, ENDS with wait" and shifts every wait one slot
// earlier, and we mirror that exactly (in memory only — the file is untouched).
const (
	demoExt = ".demo"
	// demoMaxWaitMs caps one imported gap, in the spirit of the demo server's
	// /max_wait: an hour of AFK between two lines shouldn't become an hour of
	// timeline. Only OffsetMs metadata (timeline/exports) — replay itself is
	// feed-on-idle and never sleeps on these.
	demoMaxWaitMs = 3000
)

// parseDemoRecords splits a .demo into packet records, joining continuation
// lines until a record ends with '%' (multi-line message text) — the exact
// loop demoserver.cpp::load_demo runs. CRLF is tolerated; blank tails drop.
func parseDemoRecords(data []byte) []string {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	var out []string
	cur := ""
	for _, ln := range lines {
		if cur == "" {
			cur = ln
		} else {
			cur += "\n" + ln // a packet spanning lines keeps its literal newline
		}
		if strings.HasSuffix(cur, "%") {
			out = append(out, cur)
			cur = ""
		}
	}
	if strings.TrimSpace(cur) != "" { // unterminated tail: keep it, ParsePacket will reject
		out = append(out, cur)
	}
	return out
}

// fixDemoWaitDesync applies AO2's pre-2.9.1 repair when the file shows the
// desync signature (SC first AND wait last): every wait packet moves one slot
// earlier (insert at max(1, len-1) — the same queue walk demoserver.cpp runs).
func fixDemoWaitDesync(records []string) []string {
	if len(records) < 2 || !strings.HasPrefix(records[0], "SC#") || !strings.HasPrefix(records[len(records)-1], "wait#") {
		return records
	}
	out := make([]string, 0, len(records))
	for _, r := range records {
		if !strings.HasPrefix(r, "SC#") && strings.HasPrefix(r, "wait#") {
			at := len(out) - 1
			if at < 1 {
				at = 1
			}
			if at > len(out) {
				at = len(out)
			}
			out = append(out, "")
			copy(out[at+1:], out[at:])
			out[at] = r
			continue
		}
		out = append(out, r)
	}
	return out
}

// demoToRecording converts a .demo into our replay model: MS → message events,
// BN → background, MC → music, waits → cumulative OffsetMs (each gap capped at
// demoMaxWaitMs). Every other packet (SC/CT/HP/TI/LE/…) is counted and skipped
// — the scene model deliberately covers what the stage shows. origin is the
// asset host to stream from (demos don't store one; AO2 plays them against a
// local base folder).
func demoToRecording(data []byte, origin string) (*sceneRecording, int, error) {
	records := fixDemoWaitDesync(parseDemoRecords(data))
	if len(records) == 0 {
		return nil, 0, fmt.Errorf("empty demo file")
	}
	// Demos are recorded by 2.8+ clients from servers with the full feature set
	// (the demo server itself advertises everything), so extended fields parse.
	features := protocol.ParseFeatures([]string{protocol.FeatureCCCCIC})
	rec := &sceneRecording{Version: recordingVersion, Origin: origin}
	skipped := 0
	cum := 0
	for _, raw := range records {
		pkt, err := protocol.ParsePacket(strings.TrimSuffix(raw, "\n"))
		if err != nil {
			skipped++
			continue
		}
		switch pkt.Header {
		case "wait":
			d := atoiClamped(pkt.Field(0), 0, demoMaxWaitMs)
			cum += d
		case "MS":
			msg, err := protocol.ParseMS(pkt.Fields, features, 0)
			if err != nil {
				skipped++
				continue
			}
			rec.Events = append(rec.Events, recEvent{OffsetMs: cum, Kind: int(courtroom.EventMessage), Message: msg})
		case "BN":
			bg := pkt.Field(0)
			if bg == "" {
				skipped++
				continue
			}
			if rec.StartBg == "" && len(rec.Events) == 0 {
				rec.StartBg = bg // opening state: seed the stage like our recorder does
			}
			rec.Events = append(rec.Events, recEvent{OffsetMs: cum, Kind: int(courtroom.EventBackground), Text: bg})
		case "MC":
			if song := pkt.Field(0); song != "" {
				rec.Events = append(rec.Events, recEvent{OffsetMs: cum, Kind: int(courtroom.EventMusic), Text: song})
			} else {
				skipped++
			}
		default:
			skipped++
		}
	}
	if len(rec.Events) == 0 {
		return nil, skipped, fmt.Errorf("no playable events (MS/BN/MC) in the demo")
	}
	rec.RecordedAt = time.Now().Format(time.RFC3339)
	return rec, skipped, nil
}

// atoiClamped parses n with AO tolerance (garbage = lo) and clamps to [lo, hi].
func atoiClamped(s string, lo, hi int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}

// recordingToDemo serializes a scene back into the AO2 .demo shape: a synthetic
// SC# from every character folder the scene uses (message speakers + pair
// partners, in appearance order), wait#<delta># between events from OffsetMs,
// and full server-shape MS lines (protocol.BuildServerMS) with char ids
// REMAPPED onto the synthetic SC — a demo is self-consistent, so AO2's RC
// handshake serves the right list.
func recordingToDemo(rec *sceneRecording) ([]byte, error) {
	if rec == nil || len(rec.Events) == 0 {
		return nil, fmt.Errorf("nothing to export")
	}
	scIdx := map[string]int{}
	var scList []string
	adopt := func(folder string) int {
		if folder == "" {
			return 0
		}
		if i, ok := scIdx[folder]; ok {
			return i
		}
		scIdx[folder] = len(scList)
		scList = append(scList, folder)
		return scIdx[folder]
	}
	for _, e := range rec.Events {
		if courtroom.EventKind(e.Kind) == courtroom.EventMessage && e.Message != nil {
			adopt(e.Message.CharName)
			if e.Message.Pair.Name != "" {
				adopt(e.Message.Pair.Name)
			}
		}
	}
	if len(scList) == 0 {
		scList = []string{"Narrator"} // a bg/music-only scene still needs a non-empty SC
	}

	var b strings.Builder
	b.WriteString(protocol.NewPacket("SC", scList...).String())
	if rec.StartBg != "" {
		b.WriteString("\n")
		b.WriteString(protocol.NewPacket("BN", rec.StartBg).String())
	}
	prev := 0
	for _, e := range rec.Events {
		if d := e.OffsetMs - prev; d > 0 {
			b.WriteString("\n")
			b.WriteString(protocol.NewPacket("wait", strconv.Itoa(d)).String())
		}
		if e.OffsetMs > prev {
			prev = e.OffsetMs
		}
		switch courtroom.EventKind(e.Kind) {
		case courtroom.EventMessage:
			if e.Message == nil {
				continue
			}
			m := *e.Message // remap ids on a copy; the scene stays untouched
			m.CharID = adopt(m.CharName)
			if m.Pair.Name != "" {
				m.Pair.CharID = adopt(m.Pair.Name)
			}
			b.WriteString("\n")
			b.WriteString(protocol.BuildServerMS(&m).String())
		case courtroom.EventBackground:
			b.WriteString("\n")
			b.WriteString(protocol.NewPacket("BN", e.Text).String())
		case courtroom.EventMusic:
			b.WriteString("\n")
			b.WriteString(protocol.NewPacket("MC", e.Text, "0").String())
		}
	}
	b.WriteString("\n")
	return []byte(b.String()), nil
}

// demoDefaultOrigin picks the asset host an imported demo streams from: the
// current URL builder's origin (the live session's when connected; "" offline
// is fine — set one in the Scene Maker afterwards, like a new scene).
func (a *App) demoDefaultOrigin() string {
	return a.urls.Origin()
}

// loadRecordingAny opens a recording by extension: our .aorec JSON, or an AO2
// .demo (converted on the fly — same model, so Play/Edit/Export all work).
func (a *App) loadRecordingAny(path string) (*sceneRecording, error) {
	if !strings.EqualFold(filepath.Ext(path), demoExt) {
		return loadRecording(path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	rec, skipped, err := demoToRecording(data, a.demoDefaultOrigin())
	if err != nil {
		return nil, err
	}
	if skipped > 0 {
		a.pushDebug(fmt.Sprintf("demo import: %s — %d non-scene packets skipped (SC/CT/HP/…)", filepath.Base(path), skipped))
	}
	return rec, nil
}

// makerExportDemo writes the maker's scene as recordings\<stem>.demo — the AO2
// interchange shape (makerSave's .demo sibling; same never-overwrite policy,
// same off-thread write).
func (a *App) makerExportDemo() {
	if a.makerScene == nil || len(a.makerScene.Events) == 0 {
		a.warnLine = "Nothing to export yet — add a line first."
		a.warnAt = time.Now()
		return
	}
	data, err := recordingToDemo(a.makerScene)
	if err != nil {
		a.warnLine = "Demo export failed: " + err.Error()
		a.warnAt = time.Now()
		return
	}
	name := sanitizeStem(a.makerName) + "-" + time.Now().Format("20060102-150405") + demoExt
	go func() {
		dir := recordingsDir()
		if dir == "" {
			return
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return
		}
		_ = os.WriteFile(filepath.Join(dir, name), data, 0o644)
	}()
	a.warnLine = "AO2 demo saved: recordings\\" + name + " — plays in AO2's demo player too."
	a.warnAt = time.Now()
}
