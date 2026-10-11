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

// blipSite is one declaration a census knows about: the NAME of the enclosing function
// (or const/var) and why it is allowed to do what it does. Keyed by NAME, not file, so a
// file split that moves the declaration keeps the census green. An exemption cannot be
// added without writing the reason next to it.
type blipSite struct {
	name string
	why  string
}

// blipTypeSites are the declarations allowed to name assets.AssetTypeBlip — the sites
// that hand a blip to the asset pipeline, plus the enum's own declaration. The live
// message funnel (internal/courtroom/courtroom.go) is deliberately NOT here: it
// prefetches the ref the mint returned, type and all, so it does not name the type at
// all any more. If it ever does again, this census says so.
var blipTypeSites = []blipSite{
	{"AssetTypeBlip", "the AssetType enum value (the declaration itself)"},
	{"typeNames", "the enum's config-name table"},
	{"IsAudio", "the audio-type predicate that names the blip type"},
	{"BlipRef", "the ONE mint: BlipRef tags the chain it builds"},
	{"categoryForType", "the content report's category mapping for an already-minted ref"},
	{"drainWarnings", "routes a conclusively-404'd blip base to the strict-sequencing gate's shared missing set (tags the type, mints no URL)"},
	{"manifestSeedTargets", "manifest seeding maps blips_extensions onto the Blip type for format learning (tags the type, mints no URL)"},
	// Tests are listed too: a test is where a fourth production site would first
	// appear, disguised as a fixture.
	{"TestManagerAudioBypassesDecode", "pins that the manager tags a blip fetch with the blip type"},
	{"TestBlipRefIdentityIsUnchanged", "pins the mint's own output"},
	{"TestReportBlipsRideTheOneMint", "pins that the report rides the mint"},
}

// blipURLLiterals are the files allowed to spell a blip path out of string
// literals. Only the mint and the URL builder it composes may; everything else
// asks them. Test files are exempt wholesale — asserting the exact URL a mint
// produced is the entire point of a test.
var blipURLLiterals = []blipSite{
	{"soundsBlips", "soundsBlips, the sounds/blips/ path constant"},
	{"legacyBlipPrefix", "legacyBlipPrefix, AO1's sfx-blip stem"},
}

// blipBuilderCallers are the NON-test files allowed to call URLBuilder.Blip or
// BlipAuthored directly instead of going through BlipRef: sites that genuinely
// want ONE spelling rather than the resolution chain.
var blipBuilderCallers = []blipSite{
	{"BlipCandidates", "BlipCandidates composes the ladder out of them"},
	// internal/ui/downloader.go used to be listed here ("wants the single modern
	// spelling") — W7b upgraded its offline mirror to walk BlipCandidates, the
	// sanctioned ladder, so it no longer calls the raw builders at all and an
	// offline pack now reproduces the live ladder's answer by construction
	// (downloader.go fetchBlip). BlipCandidates callers are not policed by this
	// net: the ladder itself is the guarded surface, and its one sentinel gap
	// (a char.ini author writing blips = -1 enumerates a doomed rung offline)
	// costs a 404, not a defect.
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

		for _, d := range f.Decls {
			switch v := d.(type) {
			case *ast.FuncDecl:
				blipInspect(v.Name.Name, isTest, v.Body, seenType, seenLiteral, seenCaller, &offenders)
			case *ast.GenDecl:
				for _, spec := range v.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok || len(vs.Names) == 0 {
						continue
					}
					blipInspect(vs.Names[0].Name, isTest, vs, seenType, seenLiteral, seenCaller, &offenders)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(offenders) > 0 {
		t.Errorf("blip URLs minted outside URLBuilder.BlipRef:\n  %s\n\n"+
			"Every blip URL in this client comes from BlipRef (internal/courtroom/blipurl.go): it applies the "+
			"sentinel/path guard and returns AO2's whole get_blips ladder. Call it, or add the declaration to the "+
			"matching census with the reason it must build its own.", strings.Join(offenders, "\n  "))
	}
	// Deletion catchers: a census that silently shrinks is a census that stopped
	// covering the thing it was written for.
	checkSeen(t, "blipTypeSites", blipTypeSites, seenType)
	checkSeen(t, "blipURLLiterals", blipURLLiterals, seenLiteral)
	checkSeen(t, "blipBuilderCallers", blipBuilderCallers, seenCaller)
}

// blipInspect scans one declaration (a function body or a const/var spec) for blip
// mint/tag sites, keying each match by the DECLARATION NAME rather than the file it
// lives in, so a file split that moves the declaration keeps the census green.
func blipInspect(name string, isTest bool, n ast.Node, seenType, seenLiteral, seenCaller []bool, offenders *[]string) {
	ast.Inspect(n, func(node ast.Node) bool {
		switch v := node.(type) {
		case *ast.Ident:
			if v.Name != "AssetTypeBlip" {
				return true
			}
			if i := blipSiteIndex(blipTypeSites, name); i >= 0 {
				seenType[i] = true
				return true
			}
			*offenders = append(*offenders, name+" tags AssetTypeBlip outside the mint")
		case *ast.BasicLit:
			if isTest || v.Kind != token.STRING {
				return true
			}
			// The weakest of the three nets: it matches the JOINED literal
			// only, so filepath.Join(base, "sounds", "blips") would evade it —
			// a site shaped that way must surface in the AssetTypeBlip or
			// Blip()-call nets instead. (downloader.go was the live example
			// until W7b moved it onto BlipCandidates; none ships today.)
			if !strings.Contains(v.Value, "sounds/blips") && !strings.Contains(v.Value, "sfx-blip") {
				return true
			}
			if i := blipSiteIndex(blipURLLiterals, name); i >= 0 {
				seenLiteral[i] = true
				return true
			}
			*offenders = append(*offenders, name+" spells a blip path in a literal: "+v.Value)
		case *ast.CallExpr:
			sel, ok := v.Fun.(*ast.SelectorExpr)
			if !ok || isTest {
				return true
			}
			if sel.Sel.Name != "Blip" && sel.Sel.Name != "BlipAuthored" {
				return true
			}
			if i := blipSiteIndex(blipBuilderCallers, name); i >= 0 {
				seenCaller[i] = true
				return true
			}
			*offenders = append(*offenders, name+" calls URLBuilder."+sel.Sel.Name+" directly")
		}
		return true
	})
}

func blipSiteIndex(sites []blipSite, name string) int {
	for i := range sites {
		if sites[i].name == name {
			return i
		}
	}
	return -1
}

func checkSeen(t *testing.T, census string, sites []blipSite, seen []bool) {
	t.Helper()
	for i := range sites {
		if !seen[i] {
			t.Errorf("%s lists %q (%s) but nothing of that name matches any more — a rename or removal "+
				"broke it (the lookup is by declaration name); re-point the entry or drop it, and check its behaviour did not go with it",
				census, sites[i].name, sites[i].why)
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
