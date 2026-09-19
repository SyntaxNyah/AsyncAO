package config

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Auto-mounted asset packages (issue #128): a `packages/` folder beside the
// executable whose subfolders are each a full AO base folder
// (characters/, background/, sounds/, ...). They are discovered once at startup
// and appended AFTER the user's manual asset-source mounts, so an explicit mount
// always wins the first-hit-wins search. DiscoverPackages is startup-only disk
// I/O — never called on a render/decode path (hard rule §2).

// PackagesDirName is the folder beside the executable that holds the auto-mounted
// packages.
const PackagesDirName = "packages"

// PackagesSortName is the optional ordering file inside PackagesDirName: a plain
// list of package folder names, one per line, highest priority first. Packages
// named in it mount in that order; any not named follow in lexical order. Blank
// lines and lines starting with '#' are ignored.
const PackagesSortName = "sorting.txt"

// DiscoverPackages returns the ordered auto-mount list from <exeDir>/packages/:
// subfolders named in packages/sorting.txt first (in listed order), then the
// remaining subfolders in lexical order. A missing or unreadable packages/
// folder, or an empty exeDir, returns nil — "no packages" is not an error.
func DiscoverPackages(exeDir string) []string {
	if exeDir == "" {
		return nil
	}
	root := filepath.Join(exeDir, PackagesDirName)
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	byName := make(map[string]string)
	var names []string
	for _, e := range entries {
		if !e.IsDir() { // only subfolders are packages; stray files are ignored
			continue
		}
		name := e.Name()
		byName[name] = filepath.Join(root, name)
		names = append(names, name)
	}
	if len(names) == 0 {
		return nil
	}
	order := readPackageOrder(filepath.Join(root, PackagesSortName))
	seen := make(map[string]bool, len(names))
	out := make([]string, 0, len(names))
	for _, name := range order {
		if p, ok := byName[name]; ok && !seen[name] {
			out = append(out, p)
			seen[name] = true
		}
	}
	// The rest, lexical, so the result is deterministic even without a file.
	var rest []string
	for _, name := range names {
		if !seen[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	for _, name := range rest {
		out = append(out, byName[name])
	}
	return out
}

// CombineMounts returns manual followed by auto. Manual first, so a user's
// explicit folder always wins over an auto-mounted package. When auto is empty
// the manual list is returned unchanged (no allocation).
func CombineMounts(manual, auto []string) []string {
	if len(auto) == 0 {
		return manual
	}
	out := make([]string, 0, len(manual)+len(auto))
	out = append(out, manual...)
	out = append(out, auto...)
	return out
}

// readPackageOrder parses packages/sorting.txt into the listed package names,
// highest priority first. Unreadable files and malformed/blank/comment lines are
// skipped; a listed name that is not actually a package folder is filtered out by
// the caller.
func readPackageOrder(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}
