package theme

import "testing"

// TestLoadWithNoThemeOnDisk records what Load actually DOES when the theme
// directory does not exist. Written as a probe during a live investigation and
// kept, because internal/ui branches on this error and the answer decides whether
// that branch is reachable at all.
func TestLoadWithNoThemeOnDisk(t *testing.T) {
	for _, name := range []string{DefaultThemeName, "AceAttorney 2x"} {
		th, err := Load(name, "", []string{`C:\this\path\does\not\exist`})
		t.Logf("Load(%q, missing-root) -> err=%v, theme==nil=%v", name, err, th == nil)
		if th != nil {
			t.Logf("    dirs=%v keys=%d", th.Dirs(), th.KeyCount())
		}
	}
	// And with no roots at all.
	th, err := Load(DefaultThemeName, "", nil)
	t.Logf("Load(default, no roots) -> err=%v, theme==nil=%v", err, th == nil)
	if th != nil {
		t.Logf("    dirs=%v keys=%d", th.Dirs(), th.KeyCount())
	}
}
