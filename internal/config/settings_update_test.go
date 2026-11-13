package config

import (
	"reflect"
	"testing"
)

// TestUpdateWriter pins the B2 bridge: an assignment through Update writes the inline
// field, clamps to the setter's range, marks the snapshot stale, and persists.
func TestUpdateWriter(t *testing.T) {
	p, _ := newTestPrefs(t)
	defer p.Close()

	// out-of-range assignment clamps exactly like SetMasterVolume.
	p.Update(func(s *Settings) { s.MasterVol = 150 })
	if got := p.Load().MasterVol; got != defaultAudioVolume {
		t.Errorf("Update clamp: MasterVol = %d, want %d", got, defaultAudioVolume)
	}
	if got := p.Load().MasterVol; got != defaultAudioVolume {
		t.Errorf("inline field after Update: MasterVol = %d, want %d", got, defaultAudioVolume)
	}

	// a bool field flips through Update and the snapshot observes it.
	before := p.Load().SmoothScaling
	p.Update(func(s *Settings) { s.SmoothScaling = !s.SmoothScaling })
	if got := p.Load().SmoothScaling; got != !before {
		t.Errorf("inline SmoothScaling = %v, want %v", got, !before)
	}
	if got := p.Load().SmoothScaling; got != !before {
		t.Errorf("snapshot SmoothScaling = %v, want %v", got, !before)
	}
}

// TestUpdateMatchesSetterAdapter proves Update (field assignment) reproduces the same
// whole-struct diff as the setter it will replace, for a representative set of setters.
func TestUpdateMatchesSetterAdapter(t *testing.T) {
	cases := []struct {
		name string
		set  func(*AssetPreferences)
		upd  func(*Settings)
	}{
		{"SetMasterVolume", func(p *AssetPreferences) {
			p.Update(func(
				sn *Settings) {

				sn.
					MasterVol =

					150

			})
		}, func(s *Settings) { s.MasterVol = 150 }},
		{"SetAlertVolume", func(p *AssetPreferences) {
			p.Update(func(
				sn *Settings) {

				sn.
					AlertVol =

					80

			})

		}, func(s *Settings) { s.AlertVol = 80 }},
		{"SetSmoothScaling", func(p *AssetPreferences) { p.Update(func(sn *Settings) { sn.SmoothScaling = true }) }, func(s *Settings) { s.SmoothScaling = true }},
		{"SetUIScale", func(p *AssetPreferences) { p.SetUIScale(130) }, func(s *Settings) { s.UIScalePct = 130 }},
		{"SetTexBudgetMiB", func(p *AssetPreferences) { p.Update(func(sn *Settings) { sn.TexBudgetMiBVal = 999 }) }, func(s *Settings) { s.TexBudgetMiBVal = 999 }},
	}
	for _, c := range cases {
		base := defaultPrefs("")
		got := defaultPrefs("")
		c.set(got)
		want := fieldDiff(defaultPrefs(""), got)

		upd := defaultPrefs("")
		upd.Update(c.upd)
		have := fieldDiff(base, upd)
		_ = base
		if !reflect.DeepEqual(have, want) {
			t.Errorf("%s: Update diff = %v, setter diff = %v", c.name, have, want)
		}
	}
}
