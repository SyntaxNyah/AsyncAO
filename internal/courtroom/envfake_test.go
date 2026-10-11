package courtroom

// envFake is a test double for EnvResolver with independently settable lookups, so a
// test can set only the one it exercises and leave the rest at their nil defaults.
type envFake struct {
	side  func() string
	emote func(stem string) (string, bool)
	end   func(reason PreanimEndReason)
}

func (f envFake) LocalSide() string {
	if f.side != nil {
		return f.side()
	}
	return ""
}

func (f envFake) InlineEmote(stem string) (string, bool) {
	if f.emote != nil {
		return f.emote(stem)
	}
	return "", false
}

func (f envFake) OnPreanimEnd(reason PreanimEndReason) {
	if f.end != nil {
		f.end(reason)
	}
}
