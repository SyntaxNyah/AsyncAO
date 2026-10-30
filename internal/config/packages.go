package config

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Packages are subfolders of packages/ beside the executable, each a full AO base folder.
// Found once at startup (hard rule 2) and mounted after manual mounts.

// PackagesDirName is the folder beside the executable holding the packages.
const PackagesDirName = "packages"

// PackagesSortName is the optional order file: one name per line, highest first; blank and '#' lines are ignored.
const PackagesSortName = "sorting.txt"

// DiscoverPackages returns sorting.txt's folders in order, then the rest lexically; nil is not an error.
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
	// Lexical, so the result is deterministic without a file.
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

// CombineMounts returns manual then auto, so an explicit folder wins first-hit search.
func CombineMounts(manual, auto []string) []string {
	if len(auto) == 0 {
		return manual
	}
	out := make([]string, 0, len(manual)+len(auto))
	out = append(out, manual...)
	out = append(out, auto...)
	return out
}

// readPackageOrder parses sorting.txt; the caller filters out names that aren't package folders.
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
