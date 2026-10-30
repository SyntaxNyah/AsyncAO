package config

import "strings"

// Collection helpers return the new slice and a changed flag; the caller wraps them in p.Update.

// addFoldUnique appends v unless a case-insensitive duplicate exists or the list is at cap; reports whether it grew.
func addFoldUnique(list []string, v string, cap int) ([]string, bool) {
	if len(list) >= cap {
		return list, false
	}
	for _, have := range list {
		if strings.EqualFold(have, v) {
			return list, false
		}
	}
	return append(list, v), true
}

// addExactUnique appends v unless present or at cap; the caller pre-normalizes, so exact match is the right rule.
func addExactUnique(list []string, v string, cap int) ([]string, bool) {
	if len(list) >= cap {
		return list, false
	}
	for _, have := range list {
		if have == v {
			return list, false
		}
	}
	return append(list, v), true
}

// removeFold removes the first case-insensitive match; reports whether anything was removed.
func removeFold(list []string, v string) ([]string, bool) {
	for i, have := range list {
		if strings.EqualFold(have, v) {
			return append(list[:i], list[i+1:]...), true
		}
	}
	return list, false
}

// removeExact removes the first exact match; reports whether anything was removed.
func removeExact(list []string, v string) ([]string, bool) {
	for i, have := range list {
		if have == v {
			return append(list[:i], list[i+1:]...), true
		}
	}
	return list, false
}

// containsFold reports whether list holds a case-insensitive match for v.
func containsFold(list []string, v string) bool {
	for _, have := range list {
		if strings.EqualFold(have, v) {
			return true
		}
	}
	return false
}

// containsInt reports whether list holds v.
func containsInt(list []int, v int) bool {
	for _, have := range list {
		if have == v {
			return true
		}
	}
	return false
}

// toggleInt flips v's membership and reports the new state; the caller applies any cap.
func toggleInt(list []int, v int) ([]int, bool) {
	for i, have := range list {
		if have == v {
			return append(list[:i], list[i+1:]...), false
		}
	}
	return append(list, v), true
}

// promoteFront moves ext to the front, deduped and capped, so the most recently seen entry leads the preference order.
func promoteFront(list []string, ext string, cap int) []string {
	next := make([]string, 0, len(list)+1)
	next = append(next, ext)
	for _, e := range list {
		if e == ext || len(next) == cap {
			continue
		}
		next = append(next, e)
	}
	return next
}
