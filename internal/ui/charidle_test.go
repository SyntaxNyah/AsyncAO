package ui

// THE PARTNER PREVIEW MINTED A LITERAL: characters/<them>/(a)normal.
// The answer is the character's own char.ini: emote 1's anim
// The gates below drive the real cache seam (charMetaFor / iniIdleAnimFor) and the
// real parser, and pin the call site by census — a missing seam has no return
// value, the box simply stays empty.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestIdleAnimIsTheFirstEmotesAnim drives the parser: emote 1's anim, whatever it
// is spelled, and an ABSENT answer (never a guess) when there is nothing to read.
func TestIdleAnimIsTheFirstEmotesAnim(t *testing.T) {
	// A pack with no "normal" anywhere in it — the reported shape.
	ini, err := courtroom.ParseCharINI([]byte(
		"[Options]\nshowname = Beatrice\n\n[Emotions]\nnumber = 3\n" +
			"1 = smug#-#HSmug#1#\n2 = calm#-#SNormal#0#\n3 = rage#-#HRage#1#\n"))
	if err != nil {
		t.Fatalf("ParseCharINI: %v", err)
	}
	if got := ini.IdleAnim(); got != "HSmug" {
		t.Errorf("IdleAnim = %q, want %q — the FIRST [Emotions] entry's anim is the pose AO2 "+
			"has selected the moment this character is picked", got, "HSmug")
	}
	// The AUTHORED CASE survives. The URL builder lower-cases the path segment
	// itself; handing it a case-folded name here would be a second normalisation
	// nobody asked for, and a mount whose files are capitalised would stop matching.
	if got := ini.IdleAnim(); got == "hsmug" {
		t.Error("IdleAnim case-folded the authored name — the URL builder owns that step")
	}

	// A conventional pack answers the convention, so nothing about it changes.
	conv, err := courtroom.ParseCharINI([]byte("[Emotions]\nnumber = 2\n1 = a#-#normal#0#\n2 = b#-#cry#0#\n"))
	if err != nil {
		t.Fatalf("ParseCharINI: %v", err)
	}
	if got := conv.IdleAnim(); got != "normal" {
		t.Errorf("IdleAnim = %q for a pack whose first emote IS normal, want %q", got, "normal")
	}

	// Absent answers: a nil receiver (nothing fetched) and an ini with no emotes.
	// "" and not a guess — the caller's optimistic convention covers that window,
	// which is what keeps the common pack at one probe.
	var nilINI *courtroom.CharINI
	if got := nilINI.IdleAnim(); got != "" {
		t.Errorf("a nil char.ini answered %q, want an empty (absent) answer", got)
	}
	empty, err := courtroom.ParseCharINI([]byte("[Options]\nshowname = Nobody\n"))
	if err != nil {
		t.Fatalf("ParseCharINI: %v", err)
	}
	if got := empty.IdleAnim(); got != "" {
		t.Errorf("an ini with no [Emotions] answered %q, want an empty (absent) answer", got)
	}
}

// TestPartnerIdleComesFromTheirCharINI is the report, driven through the REAL
// cache seam rather than a mirror of it: charMetaFor answers, iniIdleAnimFor
// applies the convention-until-the-ini-disagrees rule, and the pair ghost mints
// from that.
// Both halves are asserted because either alone is passable by a wrong fix. A
// character whose first emote is NOT "normal" must preview on their own pose; a
// character who DOES have "normal" must be byte-identical to before, so the
func TestPartnerIdleComesFromTheirCharINI(t *testing.T) {
	// roomFactoryApp, not the bare settled courtroom: the "never seen" arm below
	// reaches charMetaFor's MISS path, which fires a real fetch, and that needs a
	// Manager. Driving the miss is the point — the fallback is half the rule.
	a, cleanup := roomFactoryApp(t)
	defer cleanup()

	// An ORIGIN, because charMetaFor refuses to answer without one (a cache keyed
	// by full char.ini URL is what makes per-server separation structural).
	a.link.urls = courtroom.NewURLBuilder(gateOrigin)
	// Seed the cache through pollCharMeta — the one production door into it — so
	// this drives the same store the live courtroom fills. Writing the map here
	// would be the mirror-test shape rule 11 forbids.
	a.overlays.charMetaRes = make(chan charMetaFetch, charMetaResCap)
	oddURL := a.charINIURL("Beatrice")
	convURL := a.charINIURL("Phoenix")
	a.overlays.charMetaRes <- charMetaFetch{url: oddURL, showname: "Beatrice", idle: "HSmug"}
	a.overlays.charMetaRes <- charMetaFetch{url: convURL, showname: "Phoenix", idle: "normal"}
	a.overlays.pollCharMeta()

	if got := a.iniIdleAnimFor("Beatrice", ghostFallbackEmote); got != "HSmug" {
		t.Errorf("the partner's idle = %q, want %q — the preview is still minting the "+
			"\"normal\" literal at a pack that has no such sprite", got, "HSmug")
	}
	if got := a.iniIdleAnimFor("Phoenix", ghostFallbackEmote); got != ghostFallbackEmote {
		t.Errorf("a conventional pack's idle = %q, want %q unchanged", got, ghostFallbackEmote)
	}

	// The BASE the ghost actually mints, through the real URL builder: a different
	// pose has to reach the URL, or the fix stops at a string nobody uses.
	oddBase := a.link.urls.Emote("Beatrice", a.iniIdleAnimFor("Beatrice", ghostFallbackEmote), courtroom.EmoteIdle)
	litBase := a.link.urls.Emote("Beatrice", ghostFallbackEmote, courtroom.EmoteIdle)
	if oddBase == litBase {
		t.Errorf("the partner ghost still resolves %q — the char.ini answer never reached the URL", oddBase)
	}
	// …and the SPELLING CHAIN comes with it (AO2-Client CharLayer::load_image
	// probes (a)X and bare X). Asserted by CONTENT, not by length: this used to
	// check only `len(alts) == 0`, under which rewriting EmoteAlts to return the
	// PREFIXED spelling twice — the bare one, the one that makes a bare-named pack
	// resolve at all, simply deleted — kept the chain the same length and passed
	// the whole repo. The prefixed base stays the asset's identity (CLAUDE.md) and
	// the bare spelling is an ALTERNATE, so the two must both be present and must
	// not be the same URL.
	alts := a.link.urls.EmoteAlts("Beatrice", "HSmug", courtroom.EmoteIdle)
	bare := a.link.urls.EmoteBare("Beatrice", "HSmug")
	if bare == oddBase {
		t.Fatalf("stale premise: the bare spelling and the prefixed identity are both %q, so this "+
			"gate could not tell a one-spelling chain from a two-spelling one", bare)
	}
	found := false
	for _, alt := range alts {
		if alt == bare {
			found = true
		}
		if alt == oddBase {
			t.Errorf("the spelling chain repeats the prefixed base %q — one of its two probes is "+
				"spent on a URL PrefetchChain already has", oddBase)
		}
	}
	if !found {
		t.Errorf("the partner's spelling chain %v does not carry the bare spelling %q — AO2-Client "+
			"probes (a)X AND bare X (CharLayer::load_image), and a pack whose sprites are named "+
			"without the prefix still previews as an empty box", alts, bare)
	}

	// An UNFETCHED partner keeps the convention rather than drawing nothing: the
	// preview must put something in the box on the frame the dialog opens.
	if got := a.iniIdleAnimFor("NeverSeen", ghostFallbackEmote); got != ghostFallbackEmote {
		t.Errorf("an unfetched partner's idle = %q, want the optimistic convention %q",
			got, ghostFallbackEmote)
	}
}

// charINIGatePoll / charINIGateTimeout pace the wait for the ONE async char.ini
// fetch the gate below fires. The fetch is a local-mount file read on a pool
// worker, so it lands in microseconds; the timeout is generous only so a loaded
// CI box reports "never landed" rather than flaking, and the poll interval keeps
// the spin off a whole core while it waits.
const (
	charINIGatePoll    = 2 * time.Millisecond
	charINIGateTimeout = 10 * time.Second
)

// TestACharINIOnTheWireReachesTheIdleCache is the END-TO-END gate for the fetch,
// and it exists because every other gate in this file starts downstream of it.
func TestACharINIOnTheWireReachesTheIdleCache(t *testing.T) {
	a, mount, cleanup := roomFactoryAppMounted(t)
	defer cleanup()

	// A pack with no "normal" anywhere in it — the reported shape — written where
	// the URL builder will look for it. The origin is the mount set's own, so
	// charINIURL mints a URL this fetcher actually serves.
	dir := filepath.Join(mount, "characters", "beatrice")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const ini = "[Options]\nshowname = The Golden Witch\nblips = male\n\n" +
		"[Emotions]\nnumber = 2\n1 = smug#-#HSmug#1#\n2 = calm#-#SNormal#0#\n"
	if err := os.WriteFile(filepath.Join(dir, charINIFileName), []byte(ini), 0o644); err != nil {
		t.Fatal(err)
	}
	a.link.urls = courtroom.NewURLBuilder(assets.LocalOriginFor([]string{mount}))

	// The MISS is the trigger: charMetaFor plants the in-flight marker and fires
	// exactly one fetch. Nothing is known yet, which is the window the seam's
	// optimistic fallback covers.
	if m := a.charMetaFor("Beatrice"); m.done {
		t.Fatal("the cache answered `done` before any fetch ran — the miss path never fired")
	}
	if got := a.iniIdleAnimFor("Beatrice", ghostFallbackEmote); got != ghostFallbackEmote {
		t.Errorf("an in-flight partner's idle = %q, want the optimistic convention %q", got, ghostFallbackEmote)
	}

	// …and the answer lands through pollCharMeta, the one production door.
	deadline := time.Now().Add(charINIGateTimeout)
	for !a.charMetaFor("Beatrice").done {
		if time.Now().After(deadline) {
			t.Fatalf("the char.ini fetch never landed in %v — nothing downstream of this can be trusted", charINIGateTimeout)
		}
		a.overlays.pollCharMeta()
		time.Sleep(charINIGatePoll)
	}

	// THE LINK. The pose the parser read is the pose the seam answers.
	if got := a.iniIdleAnimFor("Beatrice", ghostFallbackEmote); got != "HSmug" {
		t.Errorf("the fetched partner's idle = %q, want %q — the char.ini was downloaded and parsed "+
			"and its answer never reached the cache the preview reads", got, "HSmug")
	}
	// The same mapping, one field over: the showname had the identical hole.
	if got, known := a.remoteIniShownameFor("Beatrice"); !known || got != "The Golden Witch" {
		t.Errorf("the fetched showname = %q (known=%v), want %q — the parse→cache mapping is "+
			"dropping fields on the floor", got, known, "The Golden Witch")
	}
}
