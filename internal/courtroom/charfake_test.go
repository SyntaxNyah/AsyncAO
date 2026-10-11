package courtroom

import "github.com/SyntaxNyah/AsyncAO/internal/theme"

// charFake is a test double for CharResolver with independently settable lookups,
// so a test can set only the lookup it exercises and leave the rest at their AO
// defaults.
type charFake struct {
	skin  func(char string) string
	ink   func(char string) (theme.RGB, bool, theme.RGB, bool)
	name  func(char string) (string, bool)
	realz func(char string) string
}

func (f charFake) ChatSkinFor(char string) string {
	if f.skin != nil {
		return f.skin(char)
	}
	return ""
}

func (f charFake) ChatInkFor(char string) (theme.RGB, bool, theme.RGB, bool) {
	if f.ink != nil {
		return f.ink(char)
	}
	return theme.RGB{}, false, theme.RGB{}, false
}

func (f charFake) IniShownameFor(char string) (string, bool) {
	if f.name != nil {
		return f.name(char)
	}
	return "", false
}

func (f charFake) CustomRealizationFor(char string) string {
	if f.realz != nil {
		return f.realz(char)
	}
	return ""
}
