// Package media loads every asset the client uses: one Manager on its own goroutine owns all lookup tables,
// and pools of workers do the single steps that can wait or use a lot of CPU.
package media

// Place says where a file's bytes may come from, which selects a chain of sources.
type Place uint8

// The places a load can name.
const (
	PlaceServer Place = iota
	PlacePacks
	PlaceTheme
	PlaceUserFile
	PlaceBuiltIn
	PlaceReplay
)

// Kind says what a file's bytes are, which selects a format.
type Kind uint8

// The kinds a load can name.
const (
	KindImage Kind = iota
	KindSound
	KindConfig
	KindListing
	KindRaw
	KindFont
)

// Class says which formats a file may have; ClassExact means its path already names one file.
type Class uint8

// The classes a host's extensions.json can describe.
const (
	ClassExact Class = iota
	ClassCharIcon
	ClassEmote
	ClassEmotions
	ClassBackground
	ClassMisc
	ClassSFX
	ClassBlips
)

// Urgency orders waiting work: a request for the current message goes ahead of everything else.
type Urgency uint8

// The two urgencies.
const (
	Later Urgency = iota
	Now
)

// maxAlts is how many other spellings a file can have, such as a sprite without its (a) folder prefix.
const maxAlts = 3

// Key names one thing a caller wants; Size separates an icon from the full image of the same file.
type Key struct {
	Place  Place
	Origin string
	Path   string
	Alts   [maxAlts]string
	Kind   Kind
	Class  Class
	Size   int
	// Tag is the caller's own label for this request; Quiet and Sticky are for the caller's finish handler and WithdrawIf.
	Tag    uint8
	Quiet  bool
	Sticky bool
}

// File is one exact location a source can answer for, such as a candidate URL.
type File string

// Answer is what a source knows about a file without doing any I/O.
type Answer uint8

// A source answers Unknown only when asking costs a request.
const (
	No Answer = iota
	Yes
	Unknown
)

// Contents reports how much a source or store holds.
type Contents struct {
	Name    string
	Entries int
}

// Source is one place bytes can come from.
type Source interface {
	// Has answers from memory with no I/O, on the Manager's goroutine.
	Has(File) Answer
	// Data returns the bytes on a pool worker, and must not touch any table.
	Data(File) ([]byte, error)
	Size() Contents
}

// Observer is a source with tables to update from a finished Data call, on the Manager's goroutine.
type Observer interface {
	Observe(file File, data []byte, err error)
}

// HostLimiter is a source whose files share a limited remote host.
type HostLimiter interface {
	Host(File) string
	Limit(host string) int
}

// Hit says which copy of a key's file a search found.
type Hit struct {
	File   File
	Source Source
}

// Format turns bytes into a result on a Compute worker, and holds no shared state.
type Format interface {
	Decode(key Key, hit Hit, data []byte) (any, error)
	// OnMainThread reports whether a result needs the main thread, such as a texture upload, before it is ready.
	OnMainThread() bool
}

// Measurer is a format that can read how much memory a decode will claim from a file's header.
type Measurer interface {
	Measure(data []byte) int64
}
