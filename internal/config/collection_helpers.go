package config

import "strings"

// Generic collection helpers. Each owns ONE rule (case-insensitive dedupe, exact
// dedupe, cap, toggle, promote-to-front) so the collection mutators share it instead
// of re-deriving it at every call site. They operate on a *Settings field's slice and
// return the new slice (plus a "changed" flag where the caller needs it); the caller
// wraps the result in p.Update(...) and applies any caller-specific side effects
// (folder cleanup, generation bump) after the flag.

// addFoldUnique appends v to list unless a case-insensitive duplicate is already
// present, bounded by cap (at cap the list is unchanged); reports whether it grew.
// Mutation: change EqualFold to == and TestCollectionHelpersAddFoldUnique fails.
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

// addExactUnique appends v to list unless it is already present (exact), bounded by
// cap; the caller pre-normalizes (lowercase/trim) so exact match is the right rule.
// Mutation: change == to EqualFold and TestCollectionHelpersAddExactUnique fails.
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

// removeFold removes the first element that equals v case-insensitively; reports
// whether anything was removed.
// Mutation: change EqualFold to == and TestCollectionHelpersRemoveFold fails.
func removeFold(list []string, v string) ([]string, bool) {
	for i, have := range list {
		if strings.EqualFold(have, v) {
			return append(list[:i], list[i+1:]...), true
		}
	}
	return list, false
}

// removeExact removes the first element that equals v exactly; reports whether
// anything was removed.
// Mutation: change == to EqualFold and TestCollectionHelpersRemoveExact fails.
func removeExact(list []string, v string) ([]string, bool) {
	for i, have := range list {
		if have == v {
			return append(list[:i], list[i+1:]...), true
		}
	}
	return list, false
}

// containsFold reports whether list holds a case-insensitive match for v.
// Mutation: change EqualFold to == and TestCollectionHelpersContainsFold fails.
func containsFold(list []string, v string) bool {
	for _, have := range list {
		if strings.EqualFold(have, v) {
			return true
		}
	}
	return false
}

// containsInt reports whether list holds v.
// Mutation: change == to != and TestCollectionHelpersContainsInt fails.
func containsInt(list []int, v int) bool {
	for _, have := range list {
		if have == v {
			return true
		}
	}
	return false
}

// toggleInt flips v's membership in list: remove it if present, append it otherwise;
// it reports the NEW state (true = now present). The caller applies any cap around it.
// Mutation: swap the returned bool and TestCollectionHelpersToggleInt fails.
func toggleInt(list []int, v int) ([]int, bool) {
	for i, have := range list {
		if have == v {
			return append(list[:i], list[i+1:]...), false
		}
	}
	return append(list, v), true
}

// promoteFront moves ext to the front of list, deduped (exact) and bounded by cap, so
// the most-recently-seen entry leads the preference order (RecordLearned's
// promote-not-overwrite rule).
// Mutation: change the len(next) == cap guard to > and TestCollectionHelpersPromoteFront fails.
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
