package courtroom

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"os"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
)

// The field defect these pin (2026-08-09 debug panel, credit Crystalwarrior for
// the find): "SG/Faris NyanNyan" is a NESTED character folder, and every sprite
// URL for it came out as
// "characters/sg%2Ffaris%20nyannyan/(b)suprised" — the identity escaped as ONE
// path segment.
//
// CANON. AO2-Client joins the identity verbatim:
// get_character_path returns VPath("characters/" + p_char + "/" + p_file)
// (../AO2-Client/src/path_functions.cpp:43-46), so a '/' in the name is a real
// directory. webAO agrees at the URL layer — `characters/${encodeURI(name)}/…`
// (../webAO/src/viewport/utils/preloadMessageAssets.ts:30) — and JavaScript's
// encodeURI leaves '/' LITERAL, which is exactly the convention segRaw's
// encodeURIRestores table already documents for this client.

// nestedIdentity is the field report's character, authored case included.
const nestedIdentity = "SG/Faris NyanNyan"

// TestNestedCharacterIdentityMintsRealSeparators pins the whole char-path family
// at once, with the exact URLs a streaming origin must be asked for.
func TestNestedCharacterIdentityMintsRealSeparators(t *testing.T) {
	const origin = "https://cdn/base/"
	u := NewURLBuilder(origin)
	const dir = origin + "characters/sg/faris%20nyannyan/"

	for _, tc := range []struct {
		name string
		got  string
		want string
	}{
		{"CharIcon", u.CharIcon(nestedIdentity), dir + "char_icon"},
		{"CharFolder", u.CharFolder(nestedIdentity), dir},
		{"Emote idle", u.Emote(nestedIdentity, "suprised", EmoteIdle), dir + "(a)suprised"},
		{"Emote talk", u.Emote(nestedIdentity, "suprised", EmoteTalk), dir + "(b)suprised"},
		{"EmoteBare", u.EmoteBare(nestedIdentity, "suprised"), dir + "suprised"},
		{"EmoteFolder", u.EmoteFolder(nestedIdentity, "suprised", EmoteTalk), dir + "(b)/suprised"},
		{"EmoteButton", u.EmoteButton(nestedIdentity, 3, true), dir + "emotions/button3_on"},
		{"ShoutBubble", u.ShoutBubble(nestedIdentity, "objection", false), dir + "objection_bubble"},
		{"ShoutSFX", u.ShoutSFX(nestedIdentity, "objection"), dir + "objection"},
		{"NamedCustomShout", u.NamedCustomShout(nestedIdentity, "nyan.gif"), dir + "custom_objections/nyan"},
	} {
		if strings.Contains(strings.ToLower(tc.got), "%2f") {
			t.Errorf("%s escaped the identity's separator: %q", tc.name, tc.got)
		}
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

// TestFlatCharacterIdentityIsByteIdentical is the no-churn half: the fix must be
// invisible for the ~99% of characters that do not nest, because these strings
// ARE the cache keys (T2/T3 by full URL, T1 texture keys by base, the learned
// format table's host key). A single changed byte here would cold-start every
// character asset on every server.
func TestFlatCharacterIdentityIsByteIdentical(t *testing.T) {
	const origin = "https://cdn/base/"
	u := NewURLBuilder(origin)
	const dir = origin + "characters/phoenix%20wright/"
	for _, tc := range []struct{ name, got, want string }{
		{"CharIcon", u.CharIcon("Phoenix Wright"), dir + "char_icon"},
		{"CharFolder", u.CharFolder("Phoenix Wright"), dir},
		{"Emote idle", u.Emote("Phoenix Wright", "normal", EmoteIdle), dir + "(a)normal"},
		{"Emote talk", u.Emote("Phoenix Wright", "normal", EmoteTalk), dir + "(b)normal"},
		{"EmoteBare", u.EmoteBare("Phoenix Wright", "normal"), dir + "normal"},
		{"EmoteFolder", u.EmoteFolder("Phoenix Wright", "normal", EmoteIdle), dir + "(a)/normal"},
		{"EmoteButton", u.EmoteButton("Phoenix Wright", 1, false), dir + "emotions/button1_off"},
		{"ShoutBubble", u.ShoutBubble("Phoenix Wright", "holdit", false), dir + "holdit_bubble"},
		{"ShoutSFX", u.ShoutSFX("Phoenix Wright", "holdit"), dir + "holdit"},
	} {
		if tc.got != tc.want {
			t.Errorf("flat %s changed: %q, want %q", tc.name, tc.got, tc.want)
		}
	}
	// Parentheses and the other encodeURI-literal marks still survive untouched.
	if got := u.Emote("Franziska von Karma", "(happy)", EmoteIdle); !strings.HasSuffix(got, "/(a)(happy)") {
		t.Errorf("encodeURI-literal marks changed: %q", got)
	}
}

// TestNestedCharacterIdentityHonoursCharCase pins that the casing power-user
// setting cases each FOLDER of a nested identity independently — every segment
// is a directory the server named on its own, so title-casing the whole string
// as one unit was never meaningful.
func TestNestedCharacterIdentityHonoursCharCase(t *testing.T) {
	const origin = "https://cdn/base/"
	title := NewURLBuilder(origin).WithCharCase(CharCaseTitle)
	if got, want := title.CharIcon(nestedIdentity), origin+"characters/Sg/Faris%20Nyannyan/char_icon"; got != want {
		t.Errorf("title-case nested = %q, want %q", got, want)
	}
	first := NewURLBuilder(origin).WithCharCase(CharCaseFirstCap)
	if got, want := first.CharIcon(nestedIdentity), origin+"characters/Sg/Faris%20nyannyan/char_icon"; got != want {
		t.Errorf("first-cap nested = %q, want %q", got, want)
	}
	// Flat names in either casing are untouched by the split (no cache-key churn
	// on the capitalised-folder servers either).
	if got, want := title.CharIcon("phoenix_wright"), origin+"characters/Phoenix_Wright/char_icon"; got != want {
		t.Errorf("flat title-case changed: %q, want %q", got, want)
	}
}

// TestNestedCharacterIdentityCannotClimbOut pins the traversal clamp the split
// makes necessary: interpreting '/' as a separator is what gives a ".." SEGMENT
// meaning, and the character name is SERVER-supplied (the SC list). Same clamp,
// same reasoning as Background's.
func TestNestedCharacterIdentityCannotClimbOut(t *testing.T) {
	u := NewURLBuilder("https://cdn/base/")
	got := u.CharIcon("../../etc/passwd")
	if strings.Contains(got, "..") {
		t.Fatalf("a nested identity climbed out of the origin: %q", got)
	}
	if want := "https://cdn/base/characters/etc/passwd/char_icon"; got != want {
		t.Fatalf("clamped identity = %q, want %q", got, want)
	}
}

// TestEveryCharacterPathBuilderNests is the encapsulation test for the seam:
// charSeg is the ONE place a character folder is spelled, and this proves it
// cannot be bypassed. It reflects over EVERY exported URLBuilder method, calls
// each with the nested identity as its first string argument, and fails any
// result that addresses characters/ with the separator escaped away.
//
// Deliberately not a hand-maintained list: a NEW char-path builder added
// tomorrow without going through charSeg is caught automatically, because its
// output contains "characters/" and its %2F is visible here.
func TestEveryCharacterPathBuilderNests(t *testing.T) {
	u := NewURLBuilder("https://cdn/base/")
	rv := reflect.ValueOf(u)
	rt := rv.Type()
	checked := 0
	for i := 0; i < rt.NumMethod(); i++ {
		m := rt.Method(i)
		ft := m.Type
		// Instance methods only, first argument a string (the character), and a
		// string / []string result — every char-path builder has that shape.
		if ft.NumIn() < 2 || ft.In(1).Kind() != reflect.String || ft.NumOut() != 1 {
			continue
		}
		args := []reflect.Value{rv, reflect.ValueOf(nestedIdentity)}
		for a := 2; a < ft.NumIn(); a++ {
			args = append(args, placeholderArg(ft.In(a)))
		}
		for _, out := range flattenStrings(m.Func.Call(args)[0]) {
			if !strings.Contains(out, "/characters/") {
				continue // not a character path (evidence, misc, music, ...)
			}
			checked++
			if strings.Contains(strings.ToLower(out), "%2f") {
				t.Errorf("%s bypasses charSeg — nested identity escaped as one segment: %q", m.Name, out)
			}
			if !strings.Contains(out, "/characters/sg/faris%20nyannyan") {
				t.Errorf("%s = %q, want the identity nested under characters/sg/faris%%20nyannyan", m.Name, out)
			}
		}
	}
	// A reflection walk that silently matched nothing would be a green test that
	// proves nothing; the count is the tripwire.
	if checked < 8 {
		t.Fatalf("only %d character paths reflected — the walk stopped seeing the builders", checked)
	}
}

// placeholderArg synthesizes a plausible value for a builder's remaining
// parameters, so the reflection walk can call every method.
func placeholderArg(t reflect.Type) reflect.Value {
	switch t.Kind() {
	case reflect.String:
		return reflect.ValueOf("normal").Convert(t)
	case reflect.Bool:
		return reflect.ValueOf(false)
	default: // ints: emote number, EmoteKind
		return reflect.ValueOf(1).Convert(t)
	}
}

// flattenStrings normalizes a builder's result (string or []string) to a slice.
func flattenStrings(v reflect.Value) []string {
	if v.Kind() == reflect.Slice {
		out := make([]string, 0, v.Len())
		for i := 0; i < v.Len(); i++ {
			out = append(out, v.Index(i).String())
		}
		return out
	}
	if v.Kind() == reflect.String {
		return []string{v.String()}
	}
	return nil
}

// TestNestedCharacterResolvesOnALocalMount closes the loop against the real byte
// source: the URL the builder mints for a nested identity is fetched, unmodified,
// by the production assets.LocalFetcher from a mount laid out the way AO2 lays
// one out (characters/SG/Faris NyanNyan/).
//
// It is the URL SPELLING that was broken, not the mount: LocalFetcher's second
// attempt percent-decodes each segment, and url.PathUnescape turns "%2F" back
// into a separator, so the old spelling happened to resolve HERE while 404ing on
// any origin that does not decode encoded slashes (Apache's AllowEncodedSlashes
// is Off by default) and landing on disk as an unreadable folder name once
// bundled. This pins that the corrected spelling keeps the mount working.
func TestNestedCharacterResolvesOnALocalMount(t *testing.T) {
	mount := t.TempDir()
	dir := filepath.Join(mount, "characters", "SG", "Faris NyanNyan")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "(b)suprised.png"), []byte("SPRITE"), 0o644); err != nil {
		t.Fatal(err)
	}
	lf := assets.NewLocalFetcher([]string{mount})
	u := NewURLBuilder(lf.BaseURL())

	base := u.Emote(nestedIdentity, "suprised", EmoteTalk)
	if strings.Contains(strings.ToLower(base), "%2f") {
		t.Fatalf("built base still escapes the separator: %q", base)
	}
	data, err := lf.Fetch(context.Background(), base+".png")
	if err != nil {
		t.Fatalf("nested identity must resolve on a local mount: %v", err)
	}
	if string(data) != "SPRITE" {
		t.Fatalf("mount served %q, want SPRITE", data)
	}
}
