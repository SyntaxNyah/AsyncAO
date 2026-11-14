package media

import "testing"

func FuzzParseManifest(f *testing.F) {
	seeds := []string{
		`{"charicon_extensions":[".png"],"emote_extensions":[".webp.static",".webp.animated",".webp"]}`,
		`{"background_extensions":[".gif",".GIF",".gif",".png",".jpg",".avif",".apng",".png",".bmp"]}`,
		`{"emotions_extensions":["  .PNG  ",".exe",".webp.static"]}`,
		`{}`, `[]`, `null`, `{"emote_extensions":null}`, ``, `{`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		lists, err := ParseManifest(data)
		if err != nil {
			return
		}
		for class, list := range lists {
			if len(list) == 0 || len(list) > manifestExtCap {
				t.Fatalf("class %d has %d formats: %v", class, len(list), list)
			}
			for _, ext := range list {
				if !knownExt(ext) {
					t.Fatalf("class %d kept unknown format %q", class, ext)
				}
			}
		}
	})
}
