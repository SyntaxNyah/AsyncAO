package config

import (
	"reflect"
	"testing"
)

// Each helper's test is written to fail under the mutation named in that helper's
// "Mutation:" line — the mutation-sensitivity gate for the collection-rule class.

func TestCollectionHelpersAddFoldUnique(t *testing.T) {
	// case-insensitive dedupe
	got, added := addFoldUnique([]string{"Foo"}, "foo", 4)
	if added || !reflect.DeepEqual(got, []string{"Foo"}) {
		t.Fatalf("dup add: got %v added=%v", got, added)
	}
	// normal append
	got, added = addFoldUnique([]string{"Foo"}, "Bar", 4)
	if !added || !reflect.DeepEqual(got, []string{"Foo", "Bar"}) {
		t.Fatalf("append: got %v added=%v", got, added)
	}
	// at cap: unchanged
	got, added = addFoldUnique([]string{"a", "b"}, "c", 2)
	if added || !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("at cap: got %v added=%v", got, added)
	}
}

func TestCollectionHelpersAddExactUnique(t *testing.T) {
	// exact dedupe: "Foo" and "foo" are distinct
	got, added := addExactUnique([]string{"foo"}, "Foo", 4)
	if !added || !reflect.DeepEqual(got, []string{"foo", "Foo"}) {
		t.Fatalf("exact case distinct: got %v added=%v", got, added)
	}
	// exact dup
	got, added = addExactUnique([]string{"foo"}, "foo", 4)
	if added || !reflect.DeepEqual(got, []string{"foo"}) {
		t.Fatalf("exact dup: got %v added=%v", got, added)
	}
	// at cap
	got, added = addExactUnique([]string{"a", "b"}, "c", 2)
	if added || !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("at cap: got %v added=%v", got, added)
	}
}

func TestCollectionHelpersRemoveFold(t *testing.T) {
	got, removed := removeFold([]string{"Foo", "Bar"}, "foo")
	if !removed || !reflect.DeepEqual(got, []string{"Bar"}) {
		t.Fatalf("case-insensitive remove: got %v removed=%v", got, removed)
	}
	got, removed = removeFold([]string{"Foo", "Bar"}, "baz")
	if removed || !reflect.DeepEqual(got, []string{"Foo", "Bar"}) {
		t.Fatalf("absent: got %v removed=%v", got, removed)
	}
}

func TestCollectionHelpersRemoveExact(t *testing.T) {
	got, removed := removeExact([]string{"Foo", "Bar"}, "Foo")
	if !removed || !reflect.DeepEqual(got, []string{"Bar"}) {
		t.Fatalf("exact remove: got %v removed=%v", got, removed)
	}
	got, removed = removeExact([]string{"Foo", "Bar"}, "foo")
	if removed || !reflect.DeepEqual(got, []string{"Foo", "Bar"}) {
		t.Fatalf("case-sensitive: got %v removed=%v", got, removed)
	}
}

func TestCollectionHelpersContainsFold(t *testing.T) {
	if !containsFold([]string{"Foo"}, "foo") {
		t.Fatal("case-insensitive contains failed")
	}
	if containsFold([]string{"Foo"}, "bar") {
		t.Fatal("absent matched")
	}
}

func TestCollectionHelpersContainsInt(t *testing.T) {
	if !containsInt([]int{1, 2, 3}, 2) {
		t.Fatal("int contains failed")
	}
	if containsInt([]int{1, 2, 3}, 9) {
		t.Fatal("absent int matched")
	}
}

func TestCollectionHelpersToggleInt(t *testing.T) {
	got, now := toggleInt([]int{1, 2}, 3)
	if !now || !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Fatalf("add: got %v now=%v", got, now)
	}
	got, now = toggleInt([]int{1, 2, 3}, 2)
	if now || !reflect.DeepEqual(got, []int{1, 3}) {
		t.Fatalf("remove: got %v now=%v", got, now)
	}
}

func TestCollectionHelpersPromoteFront(t *testing.T) {
	// promote existing to front
	got := promoteFront([]string{"a", "b", "c"}, "b", 4)
	if !reflect.DeepEqual(got, []string{"b", "a", "c"}) {
		t.Fatalf("promote: got %v", got)
	}
	// new entry leads, deduped
	got = promoteFront([]string{"a", "b"}, "x", 4)
	if !reflect.DeepEqual(got, []string{"x", "a", "b"}) {
		t.Fatalf("new lead: got %v", got)
	}
	// cap bounds the result
	got = promoteFront([]string{"a", "b", "c"}, "x", 3)
	if !reflect.DeepEqual(got, []string{"x", "a", "b"}) {
		t.Fatalf("cap: got %v", got)
	}
}
