package config

import (
	"reflect"
	"sort"
)

// diffJSON returns the dotted field paths where two decoded-JSON maps differ. Maps and
// slices are compared structurally; a key present in only one side, or a scalar with a
// different value, is one path.
func diffJSON(a, b map[string]any, prefix string) []string {
	var out []string
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	var sorted []string
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	for _, k := range sorted {
		av, aok := a[k]
		bv, bok := b[k]
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		switch {
		case !aok || !bok:
			out = append(out, path)
		case av == nil || bv == nil:
			if !reflect.DeepEqual(av, bv) {
				out = append(out, path)
			}
		default:
			am, aIsMap := av.(map[string]any)
			bm, bIsMap := bv.(map[string]any)
			if aIsMap && bIsMap {
				out = append(out, diffJSON(am, bm, path)...)
			} else if !reflect.DeepEqual(av, bv) {
				out = append(out, path)
			}
		}
	}
	return out
}
