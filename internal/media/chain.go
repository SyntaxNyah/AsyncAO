package media

// Entry pairs a source with the pool its Data runs on; a nil Pool means the bytes are already in memory.
type Entry struct {
	Source Source
	Pool   *Pool
	// AllFormats marks the player's own files: the source is asked for every format, not the server's list, and what
	// it finds teaches the server's order nothing.
	AllFormats bool
}

// Chain is the fixed lookup order for one place.
type Chain struct {
	entries []Entry
}

// NewChain builds a chain that asks its sources in this order.
func NewChain(entries ...Entry) *Chain { return &Chain{entries: entries} }

// location is one source's copy of one file.
type location struct {
	entry int
	file  File
}

// nameFiles is one name's files: the server's formats, and every format for the AllFormats sources.
type nameFiles struct{ server, all []File }

// locations lists where one name's files may be, in the order to ask: copies a source has, then copies no source
// knows about yet, each in source order; a copy a source knows is absent is left out. Any copy of a name wins, as AO2's
// get_real_path asks every mount for one name (path_functions.cpp:399).
func (c *Chain) locations(name nameFiles) []location {
	var held, unknown []location
	for i, e := range c.entries {
		files := name.server
		if e.AllFormats {
			files = name.all
		}
		for _, f := range files {
			switch e.Source.Has(f) {
			case Yes:
				held = append(held, location{i, f})
			case Unknown:
				unknown = append(unknown, location{i, f})
			}
		}
	}
	return append(held, unknown...)
}
