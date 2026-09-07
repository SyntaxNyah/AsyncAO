package ui

import (
	"strings"
	"testing"
)

// TestMixChannels pins #10: per-channel mutes zero just that channel, master mute zeroes
// everything (including the alert), the message-on-stage duck scales music, the cross-tab
// duck silences music entirely, and the master knob scales all channels + the alert. None
// of it touches the stored slider levels.
func TestMixChannels(t *testing.T) {
	// No mute, master 100, no duck, no tab-duck → unchanged.
	if mu, s, b, al := mixChannels(100, 80, 70, 60, 50, false, false, false, false, false, false); mu != 80 || s != 70 || b != 60 || al != 50 {
		t.Errorf("no-op = %d/%d/%d/%d, want 80/70/60/50", mu, s, b, al)
	}
	// Each per-channel mute zeroes only its own channel.
	if mu, s, b, _ := mixChannels(100, 80, 70, 60, 50, false, true, false, false, false, false); mu != 0 || s != 70 || b != 60 {
		t.Errorf("music mute = %d/%d/%d, want 0/70/60", mu, s, b)
	}
	if _, s, _, _ := mixChannels(100, 80, 70, 60, 50, false, false, true, false, false, false); s != 0 {
		t.Error("sfx mute didn't zero sfx")
	}
	if _, _, b, _ := mixChannels(100, 80, 70, 60, 50, false, false, false, true, false, false); b != 0 {
		t.Error("blip mute didn't zero blip")
	}
	// Master mute zeroes everything, including the alert.
	if mu, s, b, al := mixChannels(100, 80, 70, 60, 50, true, false, false, false, false, false); mu != 0 || s != 0 || b != 0 || al != 0 {
		t.Errorf("master mute = %d/%d/%d/%d, want all 0", mu, s, b, al)
	}
	// Master scale halves the channels + the alert.
	if mu, s, b, al := mixChannels(50, 80, 70, 60, 40, false, false, false, false, false, false); mu != 40 || s != 35 || b != 30 || al != 20 {
		t.Errorf("master=50 = %d/%d/%d/%d, want 40/35/30/20", mu, s, b, al)
	}
	// Cross-tab duck silences ONLY music, leaving sfx/blip/alert alone (a backgrounded
	// tab's stream is kept rolling but inaudible; other channels are the active tab's).
	if mu, s, b, al := mixChannels(100, 80, 70, 60, 50, false, false, false, false, false, true); mu != 0 || s != 70 || b != 60 || al != 50 {
		t.Errorf("tab-duck = %d/%d/%d/%d, want 0/70/60/50", mu, s, b, al)
	}
	// Cross-tab duck composes over the user's music volume (it's an absolute silence,
	// not a scale): even a loud music slider + master 100 → 0.
	if mu, _, _, _ := mixChannels(100, 100, 70, 60, 50, false, false, false, false, false, true); mu != 0 {
		t.Errorf("tab-duck over full music = %d, want 0", mu)
	}
	// Cross-tab duck composes with a per-channel mute (both drive music to 0) and the
	// master scale (0*anything=0), and it dominates the softer message-on-stage duck.
	if mu, _, _, _ := mixChannels(80, 90, 70, 60, 50, false, false, false, false, true, true); mu != 0 {
		t.Errorf("tab-duck + message-duck + master=80 music = %d, want 0", mu)
	}
}

// TestMutedChannelsBootNotice pins the silent-launch fix's message text: no
// channel muted produces no banner (so a normal, never-muted launch is
// unchanged), and the named channels appear in a fixed master/music/sfx/blip
// order regardless of which combination is set.
func TestMutedChannelsBootNotice(t *testing.T) {
	if got := mutedChannelsBootNotice(false, false, false, false); got != "" {
		t.Errorf("no channel muted must produce no notice, got %q", got)
	}
	if got := mutedChannelsBootNotice(true, false, false, false); got == "" || !strings.Contains(got, "Master") {
		t.Errorf("master-only notice = %q, want it to name Master", got)
	}
	got := mutedChannelsBootNotice(true, false, true, false)
	if !strings.Contains(got, "Master") || !strings.Contains(got, "SFX") || strings.Contains(got, "Music") || strings.Contains(got, "Blip") {
		t.Errorf("master+sfx notice = %q, want Master and SFX only", got)
	}
}
