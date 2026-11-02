package media

// Entry pairs a source with the pool its Data runs on; a nil Pool means the bytes are already in memory.
type Entry struct {
	Source Source
	Pool   *Pool
}

// Chain is the fixed lookup order for one place.
type Chain struct {
	entries []Entry
}

// NewChain builds a chain that asks its sources in this order.
func NewChain(entries ...Entry) *Chain { return &Chain{entries: entries} }

// tried marks one source's copy of a file that failed, so a bad copy can't hide the next one.
type tried struct {
	entry int
	file  File
}

// find asks each source about every candidate before moving on, so any known copy beats a request that may miss.
func (c *Chain) find(files []File, skip map[tried]bool) (index int, file File, found bool) {
	for i, e := range c.entries {
		for _, f := range files {
			if !skip[tried{i, f}] && e.Source.Has(f) == Yes {
				return i, f, true
			}
		}
	}
	for i, e := range c.entries {
		for _, f := range files {
			if !skip[tried{i, f}] && e.Source.Has(f) == Unknown {
				return i, f, true
			}
		}
	}
	return 0, "", false
}
