package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

func TestResolveConfigBase(t *testing.T) {
	const (
		exe = "/app"
		os_ = "/home/u/.config"
	)
	portable := filepath.Join(exe, PortableDirName)
	classic := filepath.Join(os_, PrefsDirName)
	portableFile := filepath.Join(portable, PrefsFileName)
	classicFile := filepath.Join(classic, PrefsFileName)

	// set builds an exists() over a fixed set of present paths.
	set := func(present ...string) func(string) bool {
		m := map[string]bool{}
		for _, p := range present {
			m[p] = true
		}
		return func(p string) bool { return m[p] }
	}
	yes := func(string) bool { return true }
	no := func(string) bool { return false }

	cases := []struct {
		name          string
		exeDir, osDir string
		exists        func(string) bool
		writable      func(string) bool
		wantDir       string
		wantPortable  bool
	}{
		{
			name:   "existing portable wins even when classic also exists",
			exeDir: exe, osDir: os_, exists: set(portableFile, classicFile), writable: yes,
			wantDir: portable, wantPortable: true,
		},
		{
			name:   "existing classic keeps existing users in place",
			exeDir: exe, osDir: os_, exists: set(classicFile), writable: yes,
			wantDir: classic, wantPortable: false,
		},
		{
			name:   "fresh + writable exe dir => portable",
			exeDir: exe, osDir: os_, exists: no, writable: yes,
			wantDir: portable, wantPortable: true,
		},
		{
			name:   "fresh + read-only exe dir => classic fallback",
			exeDir: exe, osDir: os_, exists: no, writable: no,
			wantDir: classic, wantPortable: false,
		},
		{
			name: "an empty config/ dir must NOT hijack an existing classic user",
			// portable dir exists but holds no prefs file: still classic.
			exeDir: exe, osDir: os_, exists: set(classicFile, portable), writable: yes,
			wantDir: classic, wantPortable: false,
		},
		{
			name:   "no exe path (go test/install w/o exe) => classic",
			exeDir: "", osDir: os_, exists: no, writable: yes,
			wantDir: classic, wantPortable: false,
		},
		{
			name:   "no OS dir => portable is the only option",
			exeDir: exe, osDir: "", exists: no, writable: yes,
			wantDir: portable, wantPortable: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, portable := resolveConfigBase(tc.exeDir, tc.osDir, tc.exists, tc.writable)
			if dir != tc.wantDir {
				t.Errorf("dir = %q, want %q", dir, tc.wantDir)
			}
			if portable != tc.wantPortable {
				t.Errorf("portable = %v, want %v", portable, tc.wantPortable)
			}
		})
	}
}

// TestUserThemesDirFollowsPortable pins the W2 write root. The property is not
// "it returns a path" but that it follows the SAME answer ConfigBaseDir already
// memoized: a portable install writes themes beside the exe (where the drop
// convention already looks), an AppData install writes them beside the config
// (so "Make portable" can carry them), and neither ever costs a fresh
// writability probe — which is why the policy is pure and takes the answer.
func TestUserThemesDirFollowsPortable(t *testing.T) {
	const exe, base = "/app", "/home/u/.config/AsyncAO"

	if got, want := userThemesDir(true, exe, "/app/config"), filepath.Join(exe, ThemesDirName); got != want {
		t.Errorf("portable themes dir = %q, want %q (beside the exe, NOT inside config/)", got, want)
	}
	if got, want := userThemesDir(false, exe, base), filepath.Join(base, ThemesDirName); got != want {
		t.Errorf("classic themes dir = %q, want %q (beside the config file)", got, want)
	}
	// Portable but no resolvable exe path (go test, an odd host): fall back to
	// the config base rather than inventing a relative "themes".
	if got, want := userThemesDir(true, "", base), filepath.Join(base, ThemesDirName); got != want {
		t.Errorf("portable-with-no-exe themes dir = %q, want %q", got, want)
	}
	if got := userThemesDir(false, "", ""); got != "" {
		t.Errorf("unresolvable location = %q, want \"\" (callers read that as 'no write root')", got)
	}
	// The name is the shared constant, not a second spelling.
	if ThemesDirName != theme.ThemesDirName {
		t.Errorf("ThemesDirName = %q but theme.ThemesDirName = %q — the alias drifted", ThemesDirName, theme.ThemesDirName)
	}
	// And the live call agrees with the live portable answer.
	if live := UserThemesDir(); live != "" && filepath.Base(live) != ThemesDirName {
		t.Errorf("UserThemesDir() = %q, want it to end in %q", live, ThemesDirName)
	}
}

// TestMigrateToPortableCarriesThemesToTheWriteRoot pins the themes step: the
// tree copy must NOT drop themes into config/ (a folder nothing reads themes
// from after the move), and the write root the next launch will use must have
// them.
func TestMigrateToPortableCarriesThemesToTheWriteRoot(t *testing.T) {
	src := t.TempDir()
	exeDir := t.TempDir()
	dest := filepath.Join(exeDir, PortableDirName)

	write := func(base, rel, body string) {
		full := filepath.Join(base, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(src, PrefsFileName, `{"a":1}`)
	write(src, filepath.Join(ThemesDirName, "nocturne", "courtroom_design.ini"), "chatbox = 1, 2, 3, 4\n")

	if err := copyTreeExcept(src, dest, map[string]bool{ThemesDirName: true}); err != nil {
		t.Fatalf("copyTreeExcept: %v", err)
	}
	if err := migrateThemesToPortable(src, exeDir); err != nil {
		t.Fatalf("migrateThemesToPortable: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dest, PrefsFileName)); err != nil {
		t.Errorf("preferences did not migrate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, ThemesDirName)); err == nil {
		t.Errorf("themes were copied into %s — nothing reads themes from there after the move", dest)
	}
	moved := filepath.Join(exeDir, ThemesDirName, "nocturne", "courtroom_design.ini")
	if _, err := os.Stat(moved); err != nil {
		t.Errorf("theme did not reach the portable write root (%s): %v", moved, err)
	}
	// The source is untouched: migration is a copy, never a move.
	if _, err := os.Stat(filepath.Join(src, ThemesDirName, "nocturne", "courtroom_design.ini")); err != nil {
		t.Errorf("source theme vanished after copy: %v", err)
	}
	// Idempotent, and a no-op when the two roots have collapsed.
	if err := migrateThemesToPortable(exeDir, exeDir); err != nil {
		t.Errorf("collapsed-roots migration should be a no-op, got %v", err)
	}
	if err := migrateThemesToPortable(t.TempDir(), exeDir); err != nil {
		t.Errorf("no themes to carry should be a no-op, got %v", err)
	}
}

// TestCopyTreeExceptFoldsTheSkipName — the skip set names a FOLDER, and on the
// two platforms this client ships on a folder name folds case: a user whose
// themes directory is spelled "Themes" holds the very directory the skip set
// exists to leave behind. An exact-name match copied it into config/, which is
// the split MigrateToPortable's separate themes step exists to prevent.
func TestCopyTreeExceptFoldsTheSkipName(t *testing.T) {
	src, dst := t.TempDir(), filepath.Join(t.TempDir(), "config")
	odd := strings.ToUpper(ThemesDirName[:1]) + ThemesDirName[1:] // "Themes"
	if err := os.MkdirAll(filepath.Join(src, odd, "nocturne"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, odd, "nocturne", theme.DesignFileName), []byte("x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, PrefsFileName), []byte(`{"a":1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := copyTreeExcept(src, dst, map[string]bool{ThemesDirName: true}); err != nil {
		t.Fatalf("copyTreeExcept: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, PrefsFileName)); err != nil {
		t.Errorf("the tree copy skipped the preferences it exists to move: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, odd)); err == nil {
		t.Errorf("%q was copied into %s — the skip set must fold case, or the themes collection splits in two", odd, dst)
	}
	// The exact spelling is of course still skipped.
	if !skipEntry(map[string]bool{ThemesDirName: true}, ThemesDirName) {
		t.Error("the exact name stopped matching")
	}
	// And an empty/nil set skips nothing (copyTree's own call).
	if skipEntry(nil, ThemesDirName) {
		t.Error("a nil skip set must skip nothing")
	}
}

// TestMigrateToPortableReportsThemesAsAPartialFailure — the themes step is the
// one part of the migration that can fail on its own, and when it does the
// settings HAVE moved. The caller has to be able to say so; a bare "make portable
// failed" would send the user looking for settings that are already there.
func TestMigrateToPortableReportsThemesAsAPartialFailure(t *testing.T) {
	src, exeDir := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, ThemesDirName, "nocturne"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, ThemesDirName, "nocturne", theme.DesignFileName), []byte("x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A plain FILE where the themes folder must be created: MkdirAll fails, which
	// is the shape of every real cause (read-only mount, permissions, full disk).
	if err := os.WriteFile(filepath.Join(exeDir, ThemesDirName), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := migrateThemesToPortable(src, exeDir)
	if err == nil {
		t.Fatal("copying themes onto a file must fail")
	}
	if !errors.Is(err, ErrThemesNotMigrated) {
		t.Errorf("error %v does not wrap ErrThemesNotMigrated — the caller cannot tell a partial success from a total one", err)
	}
}

func TestCopyTree(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "config") // dst should be created

	write := func(rel, body string) {
		full := filepath.Join(src, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(PrefsFileName, `{"a":1}`)
	write(JukeboxFileName, `{"playlists":[]}`)
	write(filepath.Join(NotebookDirName, "miku-pizza-deadbeef.json"), `{"lines":["x"]}`)
	write(writeProbeName, "junk") // must be skipped

	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree: %v", err)
	}

	for _, rel := range []string{PrefsFileName, JukeboxFileName, filepath.Join(NotebookDirName, "miku-pizza-deadbeef.json")} {
		if _, err := os.Stat(filepath.Join(dst, rel)); err != nil {
			t.Errorf("missing copied %s: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dst, writeProbeName)); err == nil {
		t.Errorf("write-probe %q should not have been copied", writeProbeName)
	}
	// Source is left intact (copy, never move).
	if _, err := os.Stat(filepath.Join(src, PrefsFileName)); err != nil {
		t.Errorf("source prefs vanished after copy: %v", err)
	}
}
