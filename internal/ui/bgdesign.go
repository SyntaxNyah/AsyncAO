package ui

import (
	"bytes"
	"context"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

const (
	// bgDesignCap bounds the design.ini files kept; a full table starts over.
	bgDesignCap = 64
	// bgDesignReads bounds the design.ini reads in flight.
	bgDesignReads = 4
	// bgDesignTimeout bounds one read.
	bgDesignTimeout = 10 * time.Second
)

// bgDesignRead is one finished design.ini read, posted from its goroutine.
type bgDesignRead struct {
	url    string
	design *theme.INI
}

// backgroundDesign answers from the files read and starts one read on a miss; ok is false until it lands, and a
// missing or unreadable file settles as nil, so a background is asked once. Render thread only.
func (a *App) backgroundDesign(url string) (*theme.INI, bool) {
	if design, ok := a.bgDesigns[url]; ok {
		return design, true
	}
	if a.bgDesignReading[url] || len(a.bgDesignReading) >= bgDesignReads || a.d.Manager == nil {
		return nil, false
	}
	if a.bgDesignReading == nil {
		a.bgDesignReading = make(map[string]bool, bgDesignReads)
		a.bgDesignRes = make(chan bgDesignRead, bgDesignReads)
	}
	a.bgDesignReading[url] = true
	mgr, res := a.d.Manager, a.bgDesignRes
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), bgDesignTimeout)
		defer cancel()
		var design *theme.INI
		// Layered: a pack in the user's own folders ships its design.ini beside its art.
		if data, err := mgr.FetchRaw(ctx, url); err == nil {
			design, _ = theme.ParseINI(bytes.NewReader(data))
		}
		res <- bgDesignRead{url, design}
	}()
	return nil, false
}

// pollBackgroundDesigns takes the finished design.ini reads.
func (a *App) pollBackgroundDesigns() {
	for {
		select {
		case r := <-a.bgDesignRes:
			delete(a.bgDesignReading, r.url)
			if a.bgDesigns == nil || len(a.bgDesigns) >= bgDesignCap {
				a.bgDesigns = make(map[string]*theme.INI, bgDesignCap)
			}
			a.bgDesigns[r.url] = r.design
		default:
			return
		}
	}
}
