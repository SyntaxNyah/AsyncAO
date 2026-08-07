package courtroom_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A blip is the only asset the client fetches dozens of times a message, and for
// most of this client's life exactly one spelling of it was ever probed:
// sounds/blips/<name>. The AO2 ladder has three (get_blips,
// ../AO2-Client/src/text_file_functions.cpp:515-527), and the reason the other
// two went missing is that the URL was built inline at each call site — one in
// the live funnel, one in the Studio content report, each with its own idea of
// which names were worth probing at all (the report's guard knew only "" and
// "-1", so a KFO "<id>^0" became a request there while the funnel dropped it).
//
// So the ladder has exactly ONE mint now, URLBuilder.BlipRef (blipurl.go), and
// the censuses below keep it that way in both directions: a NEW site that mints
// or tags a blip URL outside the mint fails, and DELETING a listed site fails
// too, so the mint cannot quietly lose its callers and pass by being unused.

// blipSite is one file a census knows about: where it is (repo-relative,
// slash-separated) and why it is allowed to do what it does. An exemption
// cannot be added without writing the reason next to it.
type blipSite struct {
	path string
	why  string
}

// blipTypeSites are the files allowed to name assets.AssetTypeBlip — the sites
// that hand a blip to the asset pipeline, plus the enum's own declaration. The
// live message funnel (internal/courtroom/courtroom.go) is deliberately NOT
// here: it prefetches the ref the mint returned, type and all, so it does not
// name the type at all any more. If it ever does again, this census says so.
var blipTypeSites = []blipSite{
	{"internal/assets/types.go", "the AssetType enum + its config-name table (the declaration itself)"},
	{"internal/courtroom/blipurl.go", "the ONE mint: BlipRef tags the chain it builds"},
	{"internal/ui/contentjob.go", "the content report's category mapping for an already-minted ref"},
	// Tests are listed too: a test is where a fourth production site would first
	// appear, disguised as a fixture.
	{"internal/assets/manager_test.go", "pins that the manager tags a blip fetch with the blip type"},
	{"internal/courtroom/blipurl_test.go", "pins the mint's own output"},
	{"internal/ui/contentjob_test.go", "pins that the report rides the mint"},
}

// blipURLLiterals are the files allowed to spell a blip path out of string
// literals. Only the mint and the URL builder it composes may; everything else
// asks them. Test files are exempt wholesale — asserting the exact URL a mint
// produced is the entire point of a test.
var blipURLLiterals = []blipSite{
	{"internal/courtroom/urlbuilder.go", "soundsBlips, the sounds/blips/ path constant"},
	{"internal/courtroom/blipurl.go", "legacyBlipPrefix, AO1's sfx-blip stem"},
}

// blipBuilderCallers are the NON-test files allowed to call URLBuilder.Blip or
// BlipAuthored directly instead of going through BlipRef: sites that genuinely
// want ONE spelling rather than the resolution chain.
var blipBuilderCallers = []blipSite{
	{"internal/courtroom/blipurl.go", "BlipCandidates composes the ladder out of them"},
	{"internal/ui/downloader.go", "the offline mirror writes sounds/blips/<name>.<ext> to disk and walks its own extension list instead of resolving, so it wants the single modern spelling"},
}

func TestBlipURLsHaveExactlyOneMint(t *testing.T) {
	root := blipRepoRoot(t)
	fset := token.NewFileSet()
	seenType := make([]bool, len(blipTypeSites))
	seenLiteral := make([]bool, len(blipURLLiterals))
	seenCaller := make([]bool, len(blipBuilderCallers))
	var offenders []string

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "bin", "vendor", "testdata", "refs":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			return nil // a file that does not parse is someone else's failing build
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		isTest := strings.HasSuffix(rel, "_test.go")

		ast.Inspect(f, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.Ident:
				if v.Name != "AssetTypeBlip" {
					return true
				}
				if i := blipSiteIndex(blipTypeSites, rel); i >= 0 {
					seenType[i] = true
					return true
				}
				offenders = append(offenders, rel+" tags AssetTypeBlip outside the mint")
			case *ast.BasicLit:
				if isTest || v.Kind != token.STRING {
					return true
				}
				// The weakest of the three nets: it matches the JOINED literal
				// only, so filepath.Join(base, "sounds", "blips") evades it —
				// such a site is caught by the AssetTypeBlip and Blip()-call
				// nets instead (downloader.go is the live example).
				if !strings.Contains(v.Value, "sounds/blips") && !strings.Contains(v.Value, "sfx-blip") {
					return true
				}
				if i := blipSiteIndex(blipURLLiterals, rel); i >= 0 {
					seenLiteral[i] = true
					return true
				}
				offenders = append(offenders, rel+" spells a blip path in a literal: "+v.Value)
			case *ast.CallExpr:
				sel, ok := v.Fun.(*ast.SelectorExpr)
				if !ok || isTest {
					return true
				}
				if sel.Sel.Name != "Blip" && sel.Sel.Name != "BlipAuthored" {
					return true
				}
				if i := blipSiteIndex(blipBuilderCallers, rel); i >= 0 {
					seenCaller[i] = true
					return true
				}
				offenders = append(offenders, rel+" calls URLBuilder."+sel.Sel.Name+" directly")
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(offenders) > 0 {
		t.Errorf("blip URLs minted outside URLBuilder.BlipRef:\n  %s\n\n"+
			"Every blip URL in this client comes from BlipRef (internal/courtroom/blipurl.go): it applies the "+
			"sentinel/path guard and returns AO2's whole get_blips ladder. Call it, or add the file to the "+
			"matching census with the reason it must build its own.", strings.Join(offenders, "\n  "))
	}
	// Deletion catchers: a census that silently shrinks is a census that stopped
	// covering the thing it was written for.
	checkSeen(t, "blipTypeSites", blipTypeSites, seenType)
	checkSeen(t, "blipURLLiterals", blipURLLiterals, seenLiteral)
	checkSeen(t, "blipBuilderCallers", blipBuilderCallers, seenCaller)
}

func blipSiteIndex(sites []blipSite, rel string) int {
	for i := range sites {
		if sites[i].path == rel {
			return i
		}
	}
	return -1
}

func checkSeen(t *testing.T, census string, sites []blipSite, seen []bool) {
	t.Helper()
	for i := range sites {
		if !seen[i] {
			t.Errorf("%s lists %q (%s) but nothing there matches any more — either the site moved "+
				"(point the census at it) or it is gone (drop the entry, and check its behaviour did not go with it)",
				census, sites[i].path, sites[i].why)
		}
	}
}

// blipRepoRoot walks up from this package to the directory holding go.mod.
func blipRepoRoot(t *testing.T) string {
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
