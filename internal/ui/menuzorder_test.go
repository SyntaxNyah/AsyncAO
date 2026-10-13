package ui

// F3 — the performance overlay painted OVER an open dropdown menu.
//
// The kit is single-pass: the last thing painted is the thing on top. drawMenuBar
// used to paint the strip AND the open pane, and App.Frame calls it before the two
// diagnostics overlays — so the F3 graph landed on the menu the user had just
// opened. The kit's OWN dropdown lists never had this problem: they paint in
// Ctx.FinishFrame, which runs last.
//
// The fix is a z-layer split, so these gates are about ORDER and PLACEMENT. That is
// not observable from a return value in a single-pass immediate-mode kit, so they
// read App.Frame's source (see srcgate_test.go for why that is a deletion-catcher
// rather than a mirror).
