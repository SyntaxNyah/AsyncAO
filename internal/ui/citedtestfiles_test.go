package ui

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Comments that name a TEST FILE are load-bearing documentation. "X (rosterpoll_test.go) is
// the deletion catcher" is how this codebase records where a rule is fenced, and it is the
// only pointer a reader has — so when it rots, the fence reads as gone. Nothing compiles a
// comment, so it rots silently.
//
// The roster-poll deletion shipped two at once, and they are the two distinct shapes.
// liveroster.go named a symbol "rosterPollCensus" as its deletion catcher and attributed
// it to liveroster_test.go: the file is real, the symbol was never declared anywhere in
// this repository — the actual catcher is TestOnlyUserActionsFetchTheRoster, declared in
// rosterpoll_test.go. pairclick_test.go attributed the real test
// TestNoSessionEverFiresATimedRosterPoll to that same liveroster_test.go, which does not
// declare it either.
//
// So a file-exists check alone would have caught NEITHER: both cited files exist. This
// census therefore does the check that bites — a symbol attributed to a test file must be
// declared in THAT file — and keeps the cheap file-exists one alongside it.
//
// Deliberately narrow. A "<name>_test.go" citation can only mean a file in this repository
// (nobody cites AO2-Client's or a dependency's tests), so it needs no allow-list, and an
// allow-list is the thing that rots next. Bare non-test ".go" citations get no such
// treatment: commands_registry.go, read.go and reader.go in these comments are Nyathena's,
// the websocket library's and archive/zip's, so that half cannot be decided mechanically.

// citationMarker is the substring every citation must contain, so a file whose bytes lack
// it cannot match either pattern below and never needs parsing. Kept next to them because
// the three have to move together.
const citationMarker = "_test.go"

var (
	// citedTestFileRe matches a bare test-file citation. The name class includes upper
	// case because these comments use the source spelling (icColorWheel_test.go), and the
	// leading guard rejects a path-qualified citation into a reference repo.
	citedTestFileRe = regexp.MustCompile(`(^|[^/\\A-Za-z0-9_.])([A-Za-z0-9_]+_test\.go)`)

	// citedSymbolRe matches this codebase's attribution shape: an identifier, then the
	// citing test file's name in parentheses. The optional "see "/"in " lead-ins are the
	// other spellings in the tree.
	citedSymbolRe = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s+\((?:see |in )?([A-Za-z0-9_]+_test\.go)`)
)

// looksLikeGoSymbol keeps ordinary prose out of the symbol check: "the census
// (rosterpoll_test.go)" attributes a role, not an identifier. Everything this codebase
// actually cites is camelCase or PascalCase, so an interior capital is the discriminator —
// deliberately conservative, because a census that cries wolf gets deleted.
func looksLikeGoSymbol(s string) bool {
	for _, r := range s[1:] {
		if r >= 'A' && r <= 'Z' {
			return true
		}
	}
	return false
}

// TestEveryCitedTestFileExists walks every Go source in the module and checks each test
// file named in a COMMENT: the file must exist, and a Go-looking symbol attributed to it
// must be declared there. Comments only — source strings legitimately hold suffixes
// ("_test.go") and synthetic parse names.
func TestEveryCitedTestFileExists(t *testing.T) {
	root := citedFilesRepoRoot(t)

	var sources []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// The reference repos are siblings, not children, so the only skips needed
			// are build output and VCS metadata.
			switch info.Name() {
			case ".git", "bin", "dist", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(info.Name(), ".go") {
			sources = append(sources, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}

	// declaredIn[base] is the set of package-scope names that test file declares. Keyed by
	// base name because that is how the comments cite. Two packages may legitimately hold a
	// same-named test file (notify_test.go does), and a base-name citation cannot tell them
	// apart, so their name sets MERGE: the census then only fires when the symbol is in
	// neither, which is the conservative side of the ambiguity.
	//
	// TWO PASSES, one file's AST alive at a time. Parsing the whole module up front and
	// holding every *ast.File until the check phase is the natural way to write this and
	// the wrong one: it is an unbounded retention (hard rule 4) proportional to the
	// repository, in the same package as this suite's zero-allocation draw gates, and it
	// made this the slowest test here by an order of magnitude. Pass 1 needs declarations,
	// which only test files can supply; pass 2 needs comments, and drops each file before
	// opening the next.
	declaredIn := map[string]map[string]bool{}
	fset := token.NewFileSet()
	for _, path := range sources {
		base := filepath.Base(path)
		if !strings.HasSuffix(base, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			t.Fatalf("parse %s: %v", path, perr)
		}
		if declaredIn[base] == nil {
			declaredIn[base] = map[string]bool{}
		}
		for name := range topLevelNames(f) {
			declaredIn[base][name] = true
		}
	}
	if len(declaredIn) == 0 {
		t.Fatal("census found no test files at all — the walk is pointed at the wrong tree")
	}

	cited, checked := 0, 0
	missingFile := map[string][]string{}
	var wrongFile []string
	for _, path := range sources {
		src, rerr := os.ReadFile(path)
		if rerr != nil {
			t.Fatalf("read %s: %v", path, rerr)
		}
		// Both patterns require the literal "_test.go", so a file without those bytes
		// cannot carry a citation and does not need parsing. Exact, not a heuristic —
		// and it is what keeps a whole-module walk cheap enough to belong in the gate.
		if !bytes.Contains(src, []byte(citationMarker)) {
			continue
		}
		f, perr := parser.ParseFile(fset, path, src, parser.ParseComments|parser.SkipObjectResolution)
		if perr != nil {
			t.Fatalf("parse %s: %v", path, perr)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)
		for _, group := range f.Comments {
			for _, c := range group.List {
				at := rel + ":" + strconv.Itoa(fset.Position(c.Pos()).Line)
				for _, m := range citedTestFileRe.FindAllStringSubmatch(c.Text, -1) {
					cited++
					if declaredIn[m[2]] == nil {
						missingFile[m[2]] = append(missingFile[m[2]], at)
					}
				}
				for _, m := range citedSymbolRe.FindAllStringSubmatch(c.Text, -1) {
					sym, file := m[1], m[2]
					names := declaredIn[file]
					if names == nil || !looksLikeGoSymbol(sym) {
						continue // an unknown file is the other check's business; prose is not a symbol
					}
					checked++
					if !names[sym] {
						wrongFile = append(wrongFile, at+": "+sym+" is not declared in "+file)
					}
				}
			}
		}
	}
	if cited == 0 {
		t.Fatal("census matched no citations at all — the pattern stopped working, not the codebase")
	}
	if checked == 0 {
		t.Fatal("census resolved no cited SYMBOLS — the attribution pattern stopped matching, which " +
			"would leave only the file-exists half running, and that half caught neither real defect")
	}
	if len(missingFile) > 0 {
		names := make([]string, 0, len(missingFile))
		for name := range missingFile {
			names = append(names, name)
		}
		sort.Strings(names)
		var lines []string
		for _, name := range names {
			lines = append(lines, name+" — cited at "+strings.Join(missingFile[name], ", "))
		}
		t.Errorf("comments cite test files that do not exist:\n  %s", strings.Join(lines, "\n  "))
	}
	if len(wrongFile) > 0 {
		sort.Strings(wrongFile)
		t.Errorf("comments attribute a symbol to the wrong test file:\n  %s\n\n"+
			"An attribution is a reader's only route from a rule to the test that fences it. Pointing it "+
			"at a file that does not declare the symbol reads exactly like a deleted fence — which is how "+
			"the deleted roster poll came to be documented as live machinery.", strings.Join(wrongFile, "\n  "))
	}
}

// topLevelNames collects every name a file declares at package scope: funcs (the usual
// citation target) plus types/vars/consts, because census tables and fixtures get cited by
// name too.
func topLevelNames(f *ast.File) map[string]bool {
	names := map[string]bool{}
	for _, d := range f.Decls {
		switch v := d.(type) {
		case *ast.FuncDecl:
			names[v.Name.Name] = true
		case *ast.GenDecl:
			for _, spec := range v.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					names[s.Name.Name] = true
				case *ast.ValueSpec:
					for _, id := range s.Names {
						names[id.Name] = true
					}
				}
			}
		}
	}
	return names
}

// citedFilesRepoRoot walks up from this package to the directory holding go.mod.
func citedFilesRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test's working directory")
		}
		dir = parent
	}
}
