package ui

// F3 — the performance overlay painted OVER an open dropdown menu.
// The fix is a z-layer split, so these gates are about ORDER and PLACEMENT. That is
// not observable from a return value in a single-pass immediate-mode kit, so they
// read App.Frame's source (see srcgate_test.go for why that is a deletion-catcher
// rather than a mirror).
