package network

// Priority says how soon a request is needed.
type Priority int

const (
	// PriorityHigh is for assets the live message needs right now; the media system never refuses it.
	PriorityHigh Priority = iota
	// PriorityLow is for speculative prefetches, refused while the media system is at its cap.
	PriorityLow
)
