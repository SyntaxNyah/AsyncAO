package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCustomErrorSpriteSwitch: a file from before the switch keeps using its saved path, and a saved switch wins.
func TestCustomErrorSpriteSwitch(t *testing.T) {
	const fileMode = 0o600
	for _, tc := range []struct {
		name, file, want string
	}{
		{"path saved by an older build", `{"errorSpritePath": "C:\\x.png"}`, `C:\x.png`},
		{"switched off", `{"errorSpritePath": "C:\\x.png", "errorSpriteCustom": false}`, ""},
		{"switched on, path padded", `{"errorSpritePath": " C:\\x.png ", "errorSpriteCustom": true}`, `C:\x.png`},
		{"never set", `{}`, ""},
	} {
		path := filepath.Join(t.TempDir(), PrefsFileName)
		if err := os.WriteFile(path, []byte(tc.file), fileMode); err != nil {
			t.Fatal(err)
		}
		p, err := load(path)
		if err != nil {
			t.Fatalf("%s: load: %v", tc.name, err)
		}
		if got := p.Load().CustomErrorSprite(); got != tc.want {
			t.Errorf("%s: CustomErrorSprite() = %q, want %q", tc.name, got, tc.want)
		}
	}
}
