package courtroom

import "testing"

// FuzzParseCharINI drives the char.ini parser (charini.go) with hostile server
// bytes: a downloaded characters/<x>/char.ini is fully attacker-controlled. The
// contract is ROBUSTNESS — no panic, no hang, no unbounded allocation (CLAUDE.md
// rules #4, #7) — not any particular parse. The [Emotions] "number=" count is the
// dangerous field: a huge value once drove an unbounded emote scan (fixed with
// charEmoteScanCap), and the regression seed below pins that it no longer hangs.
func FuzzParseCharINI(f *testing.F) {
	seeds := []string{
		"[Options]\nname = Dorothy\nblips = female\nchat = dorothybox\n[Emotions]\nnumber = 0\n",
		"[Options]\nname = X\n[Emotions]\nnumber = 2\n1 = normal#leap#normal#1\n2 = plain#-#plain#0\n",
		"[Emotions]\nnumber = 1\n1 = a#b#c#5#3\n[SoundN]\n1 = whip\n[SoundT]\n1 = 4\n[SoundL]\n1 = 1\n[Blips]\n1 = deep\n",
		"[Emotions]\nnumber = 1\n1 = normal#leap#normal#1\n[leap_FrameScreenshake]\n3 = 1\n[(b)normal_FrameSFX]\n5 = whip\n2 = slap\n",
		"[Shouts]\ncustom_name = MyShout\ngotcha_name = Gotcha!\nholdit_name = Hold It!\n",
		// Regression seed for the unbounded-scan hang (charEmoteScanCap): a
		// hostile count must clamp, not loop ~2e9 times allocating keys.
		"[Emotions]\nnumber = 2000000000\n",
		// Rows that fall short of the emote field count are skipped, not indexed.
		"[Emotions]\nnumber = 3\n1 = tooshort\n2 = a#b#c\n3 = ok#p#a#1\n",
		// Degenerate payloads: empty, a truncated "[" header, a key-less line.
		"",
		"[",
		"= noval",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		ini, err := ParseCharINI(data)
		if err != nil {
			return // a parse error is a valid outcome for malformed bytes
		}
		// Touch the parsed shape's accessors so a corrupt slice surfaces here.
		_ = ini.Showname
		for i := range ini.Emotes {
			_ = ini.Emotes[i].FrameSFX
		}
		for i := range ini.CustomShouts {
			_ = ini.CustomShouts[i].Name
		}
	})
}
