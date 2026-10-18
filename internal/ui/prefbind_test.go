package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// TestPrefBinding pins the get/set contract: Get reads the snapshot through the accessor,
// Set writes through the existing setter and the next Get observes it.
func TestPrefBinding(t *testing.T) {
	dir := t.TempDir()
	p, err := config.New(dir + "/prefs.json")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	b := prefBool(p, func(s *config.Settings) bool { return s.SmoothScaling }, func(v bool) {
		p.Update(func(sn *config.Settings) {
			sn.SmoothScaling = v
		})
	})
	before := b.Get()
	b.Set(!before)
	if b.Get() != !before {
		t.Errorf("after Set, Get = %v, want %v", b.Get(), !before)
	}
}

// TestPrefIntBinding pins the int shape.
func TestPrefIntBinding(t *testing.T) {
	dir := t.TempDir()
	p, err := config.New(dir + "/prefs.json")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	b := prefInt(p, func(s *config.Settings) int { return s.SpriteScaling }, p.SetSpriteScalingMode)
	b.Set(2)
	if b.Get() != 2 {
		t.Errorf("after Set, Get = %d, want 2", b.Get())
	}
}
