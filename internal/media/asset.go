package media

import (
	"errors"
	"sync/atomic"
	"time"
)

// AssetState is how far a request has got.
type AssetState int32

// The states an Asset moves through; everything from Ready on is final.
const (
	Searching AssetState = iota
	Decoding
	Ready
	Missing
	Failed
	Refused
	Withdrawn
)

// ErrMissing reports that no source has the file.
var ErrMissing = errors.New("media: file not found in any source")

// ErrRefused reports that the request table was full; ask again later.
var ErrRefused = errors.New("media: too many requests")

// ErrWithdrawn reports that the request was withdrawn before it finished.
var ErrWithdrawn = errors.New("media: request withdrawn")

// Asset is what a caller holds, like a QFuture: reading it never blocks and never takes a lock.
type Asset struct {
	key      Key
	state    atomic.Int32
	result   atomic.Pointer[any]
	err      atomic.Pointer[error]
	claim    atomic.Int64
	urgent   atomic.Bool
	finished atomic.Int64 // unix nanoseconds, for expiring Missing
	done     chan struct{}
}

func newAsset(key Key, u Urgency) *Asset {
	a := &Asset{key: key, done: make(chan struct{})}
	a.urgent.Store(u == Now)
	return a
}

// Key returns what this Asset was asked for.
func (a *Asset) Key() Key { return a.key }

// State returns how far the request has got.
func (a *Asset) State() AssetState { return AssetState(a.state.Load()) }

// Ready reports whether the result can be used now.
func (a *Asset) Ready() bool { return a.State() == Ready }

// Result returns the finished result, or nil until it is ready.
func (a *Asset) Result() any {
	if r := a.result.Load(); r != nil {
		return *r
	}
	return nil
}

// Err returns why the Asset can't be ready, or nil.
func (a *Asset) Err() error {
	if e := a.err.Load(); e != nil {
		return *e
	}
	return nil
}

func (a *Asset) finish(state AssetState, result any, err error, at time.Time) {
	if result != nil {
		a.result.Store(&result)
	}
	if err != nil {
		a.err.Store(&err)
	}
	a.finished.Store(at.UnixNano())
	a.state.Store(int32(state))
	close(a.done)
}
