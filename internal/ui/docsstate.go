package ui

// docsState owns docs state.
type docsState struct {
	env         *env
	aboutScroll int32 // About screen scroll offset (the page outgrew small windows)
	// About: the prose is reflowed to the current column width with WrapText and
	// cached here (keyed by width), so the wrap runs only on a resize, never per
	// frame (the page is off the hot path, but this repo keeps UI draws alloc-free).
	aboutFlatW int32           // content width the cache was built for (0 = none)
	aboutFlat  []aboutFlatLine // flattened, wrapped render lines
	// Changelog ("What's New" version history): same width-keyed reflow cache as
	// About — the embedded CHANGELOG.md is wrapped only on a resize, never per frame.
	changelogScroll int32
	changelogFlatW  int32
	changelogFlat   []aboutFlatLine
	// Help screen (glossary + privacy explainer): the same width-keyed reflow cache.
	helpTab       int
	helpDocScroll int32
	helpFlatW     int32
	helpFlat      []aboutFlatLine
}
