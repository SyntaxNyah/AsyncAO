package themepack

// CreateFromSidecar's gates (v1.90.0 W9) — the third way a theme arrives.
//
// It is a WRITE PATH into the user's themes folder, so what it owes is the same
// as what Extract and CopyForEditing owe: it lands inside the root it was given,
// it never overwrites what is already there, and a refusal leaves nothing behind.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// createFixture is a model with something in every section a gallery theme has.
func createFixture(t *testing.T) *theme.Sidecar {
	t.Helper()
	sc := theme.NewSidecar()
	sc.Meta.Name = "Neon Netrunner"
	sc.Meta.Author = "AsyncAO"
	sc.Meta.License = "AGPL-3.0-or-later"
	sc.Meta.StylePreset, sc.Meta.LayoutPreset = "cyberpunk", "classic_default"
	sc.Overrides = []theme.KeyRect{{Key: "viewport", Rect: theme.Rect{W: 256, H: 192}}}
	sc.Elements = []theme.Element{{
		ID: "grid", Kind: theme.ElemShape, Shape: "sharp",
		Anchor: "viewport", Fill: theme.RGBA{R: 31, G: 107, B: 69, A: 255},
	}}
	return sc
}

// TestCreateFromSidecarWritesOneThemeInsideTheRoot is the happy path and the
// containment claim in one: the folder is under the root that was passed, it holds
// exactly the native tier, and the file reads back as the model that was written.
func TestCreateFromSidecarWritesOneThemeInsideTheRoot(t *testing.T) {
	root := emptyRoot(t)
	m, err := CreateFromSidecar(root, "Neon Netrunner", createFixture(t))
	if err != nil {
		t.Fatalf("CreateFromSidecar: %v", err)
	}
	if rel, err := filepath.Rel(root, m.Dir); err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("the theme landed at %q, outside the root %q", m.Dir, root)
	}
	tree := treeOf(t, m.Dir)
	if len(tree) != 1 {
		t.Fatalf("the created theme holds %d files (%v) — a gallery theme is its native tier and nothing else",
			len(tree), tree)
	}
	body, ok := tree[sidecarName]
	if !ok {
		t.Fatalf("no %s in %v", sidecarName, tree)
	}
	// NO courtroom_design.ini, deliberately (see create.go): geometry lives in
	// [overrides] over whatever AO2 base the loader resolves, and writing one would
	// fork every gallery theme off the base on day one.
	if _, forked := tree["courtroom_design.ini"]; forked {
		t.Error("the created theme wrote a courtroom_design.ini — design §5 R3's first mitigation layer is that it never does")
	}
	back, err := theme.ParseSidecar([]byte(body))
	if err != nil {
		t.Fatalf("the file we just wrote does not parse: %v", err)
	}
	if back.Meta.Name != "Neon Netrunner" || back.Meta.StylePreset != "cyberpunk" {
		t.Errorf("the written theme does not name itself: %+v", back.Meta)
	}
	if len(back.Elements) != 1 || back.Elements[0].ID != "grid" {
		t.Errorf("the written theme lost its elements: %+v", back.Elements)
	}
}

// TestCreateFromSidecarSuffixesRatherThanOverwrites is the same promise an import
// makes. Building one pair twice is an ordinary thing to do — a gallery invites it
// — and the second one must not silently replace the first, which may already have
// been edited.
func TestCreateFromSidecarSuffixesRatherThanOverwrites(t *testing.T) {
	root := emptyRoot(t)
	first, err := CreateFromSidecar(root, "Neon Netrunner", createFixture(t))
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := CreateFromSidecar(root, "Neon Netrunner", createFixture(t))
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first.Dir == second.Dir {
		t.Fatalf("both creates landed on %q — the second overwrote the first", first.Dir)
	}
	if _, err := os.Stat(filepath.Join(first.Dir, sidecarName)); err != nil {
		t.Fatalf("the FIRST theme is gone after the second create: %v", err)
	}
}

// TestCreateFromSidecarRefusesBeforeItTouchesTheDisk pins the ordering the write
// guard depends on: a model Sidecar.Bytes would refuse must not leave a folder
// behind, and neither must a create with nothing to write.
func TestCreateFromSidecarRefusesBeforeItTouchesTheDisk(t *testing.T) {
	t.Run("no model", func(t *testing.T) {
		root := emptyRoot(t)
		if _, err := CreateFromSidecar(root, "x", nil); !errors.Is(err, ErrCreateNoModel) {
			t.Fatalf("got %v, want ErrCreateNoModel", err)
		}
		if _, err := os.Stat(root); err == nil {
			t.Fatal("the refused create created the themes root")
		}
	})
	t.Run("no name anywhere", func(t *testing.T) {
		root := emptyRoot(t)
		sc := theme.NewSidecar() // no Meta.Name either
		if _, err := CreateFromSidecar(root, "", sc); err == nil {
			t.Fatal("a nameless theme was created")
		}
	})
	t.Run("a model the writer refuses", func(t *testing.T) {
		root := emptyRoot(t)
		sc := createFixture(t)
		// An id outside the [a-z0-9_-] grammar: Validate refuses it, so the create
		// must too — and must do it before MkdirAll.
		sc.Elements[0].ID = "NOT AN ID"
		if _, err := CreateFromSidecar(root, "Bad", sc); err == nil {
			t.Fatal("a model the writer refuses was written to disk")
		}
		if _, err := os.Stat(root); err == nil {
			t.Fatal("the refused create left the themes root behind")
		}
	})
}
