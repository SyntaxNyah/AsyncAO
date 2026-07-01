package courtroom

import "testing"

// TestURLCharCasing pins the power-user character-folder casing: the default lowercases the folder
// (the safe, unchanged behaviour), first-cap and title-case only the CHARACTER folder, and the
// emote segment always stays lowercase regardless of the mode.
func TestURLCharCasing(t *testing.T) {
	const origin = "http://x/base/"

	// Default: lowercase everything (unchanged).
	if got := NewURLBuilder(origin).Emote("Phoenix_Wright", "Normal", EmoteIdle); got != origin+"characters/phoenix_wright/(a)normal" {
		t.Errorf("lowercase default = %q", got)
	}
	// First-cap: only the folder's first letter; the emote stays lowercase.
	first := NewURLBuilder(origin).WithCharCase(CharCaseFirstCap)
	if got := first.Emote("phoenix_wright", "Normal", EmoteIdle); got != origin+"characters/Phoenix_wright/(a)normal" {
		t.Errorf("first-cap = %q, want folder Phoenix_wright, emote lowercase", got)
	}
	// Title-case: each word of the folder.
	title := NewURLBuilder(origin).WithCharCase(CharCaseTitle)
	if got := title.CharIcon("phoenix_wright"); got != origin+"characters/Phoenix_Wright/char_icon" {
		t.Errorf("title-case = %q, want folder Phoenix_Wright", got)
	}
	// An out-of-range mode is ignored (stays lowercase) — never breaks the URL.
	if got := NewURLBuilder(origin).WithCharCase(99).CharIcon("Phoenix"); got != origin+"characters/phoenix/char_icon" {
		t.Errorf("out-of-range casing = %q, want lowercase", got)
	}
}
