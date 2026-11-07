package media

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// MemorySource keeps recently used bytes; its Data needs no pool, as nothing can wait.
type MemorySource struct {
	lru *cache.ByteBudgetLRU[File, []byte]
}

const memoryMaxEntries = cache.DefaultMaxEntries

// NewMemorySource holds up to budget bytes.
func NewMemorySource(budget int64) (*MemorySource, error) {
	lru, err := cache.NewByteBudgetLRU[File, []byte](memoryMaxEntries, budget, nil)
	if err != nil {
		return nil, fmt.Errorf("media: memory source: %w", err)
	}
	return &MemorySource{lru: lru}, nil
}

// Keep stores bytes; it matches Config.KeepBytes.
func (s *MemorySource) Keep(f File, data []byte) { s.lru.Add(f, data, int64(len(data))) }

// Has reports whether the bytes are held.
func (s *MemorySource) Has(f File) Answer {
	if s.lru.Contains(f) {
		return Yes
	}
	return No
}

// Data returns the held bytes.
func (s *MemorySource) Data(f File) ([]byte, error) {
	if d, ok := s.lru.Get(f); ok {
		return d, nil
	}
	return nil, ErrAbsent
}

// Size reports the held entries.
func (s *MemorySource) Size() Contents { return Contents{Name: "memory", Entries: s.lru.Len()} }

// DiskSource answers from an index of the cache's file names, so a miss never touches the disk.
type DiskSource struct {
	disk  *cache.DiskCache
	index map[string]struct{}
}

// NewDiskSource starts with an empty index; Adopt fills it from ScanDiskIndex.
func NewDiskSource(disk *cache.DiskCache) *DiskSource {
	return &DiskSource{disk: disk, index: map[string]struct{}{}}
}

// ScanDiskIndex lists a cache's entries by walking its folders, so it belongs on a long job.
func ScanDiskIndex(root string) ([]string, error) {
	var keys []string
	err := filepath.WalkDir(root, func(_ string, e fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if !e.IsDir() {
			keys = append(keys, e.Name())
		}
		return nil
	})
	return keys, err
}

// Adopt adds scanned entries to the index, on the Manager's goroutine.
func (s *DiskSource) Adopt(keys []string) {
	for _, k := range keys {
		s.add(k)
	}
}

// Keep queues bytes for the disk writer and indexes them; a dropped write heals through a failed read.
func (s *DiskSource) Keep(f File, data []byte) {
	s.disk.Put(string(f), data)
	s.add(cache.Key(string(f)))
}

// add indexes one entry; past diskIndexCap the index starts over, which only costs repeat requests.
func (s *DiskSource) add(key string) {
	if len(s.index) >= diskIndexCap {
		clear(s.index)
	}
	s.index[key] = struct{}{}
}

// Has answers from the index.
func (s *DiskSource) Has(f File) Answer {
	if _, ok := s.index[cache.Key(string(f))]; ok {
		return Yes
	}
	return No
}

// Data reads the file on a Disk worker.
func (s *DiskSource) Data(f File) ([]byte, error) {
	if d, ok := s.disk.Get(string(f)); ok {
		return d, nil
	}
	return nil, ErrAbsent
}

// Observe drops an entry whose read failed, so a stale index heals itself.
func (s *DiskSource) Observe(f File, _ []byte, err error) {
	if err != nil {
		delete(s.index, cache.Key(string(f)))
	}
}

// Size reports the indexed entries.
func (s *DiskSource) Size() Contents { return Contents{Name: "disk", Entries: len(s.index)} }

// Fetcher is what a RemoteSource needs from an HTTP client.
type Fetcher interface {
	Fetch(ctx context.Context, url string) ([]byte, error)
}

const (
	// HostStartWorkers is what a host gets until it answers, so its first request tests it.
	HostStartWorkers = 1
	// HostAnsweredWorkers is what a host gets once it has answered at all; it matches the HTTP client's connection limit.
	HostAnsweredWorkers = 16
	// remoteIndexCap bounds each table; past it the table starts over, which only costs repeat requests.
	remoteIndexCap = 1 << 20
	// diskIndexCap bounds the disk index, which never shrinks when the disk cache evicts.
	diskIndexCap = 1 << 20
)

// RemoteSource is the remote index and the query behind it: a table of files known to exist and known to be missing.
type RemoteSource struct {
	fetch      Fetcher
	ctx        context.Context
	clock      func() time.Time
	missingTTL time.Duration
	exists     map[File]struct{}
	missing    map[File]time.Time
	answered   map[string]bool
}

// NewRemoteSource asks through fetch.
func NewRemoteSource(ctx context.Context, fetch Fetcher, missingTTL time.Duration, clock func() time.Time) *RemoteSource {
	return &RemoteSource{fetch: fetch, ctx: ctx, clock: clock, missingTTL: missingTTL,
		exists: map[File]struct{}{}, missing: map[File]time.Time{}, answered: map[string]bool{}}
}

// Has answers from the tables; a file never asked about is Unknown, and only that costs a request.
func (s *RemoteSource) Has(f File) Answer {
	if _, ok := s.exists[f]; ok {
		return Yes
	}
	if at, ok := s.missing[f]; ok {
		if s.clock().Sub(at) < s.missingTTL {
			return No
		}
		delete(s.missing, f)
	}
	return Unknown
}

// Data asks the host on a Network worker.
func (s *RemoteSource) Data(f File) ([]byte, error) {
	data, err := s.fetch.Fetch(s.ctx, string(f))
	if errors.Is(err, network.ErrAssetNotFound) {
		return nil, fmt.Errorf("%w: %w", ErrAbsent, err)
	}
	return data, err
}

// Observe records the answer in the tables and what it says about the host.
func (s *RemoteSource) Observe(f File, _ []byte, err error) {
	host := hostOf(f)
	switch {
	case err == nil:
		if len(s.exists) >= remoteIndexCap {
			s.exists = map[File]struct{}{}
		}
		s.exists[f] = struct{}{}
		delete(s.missing, f)
		s.answered[host] = true
	case IsAbsent(err):
		if len(s.missing) >= remoteIndexCap {
			s.missing = map[File]time.Time{}
		}
		s.missing[f] = s.clock()
		delete(s.exists, f)
		s.answered[host] = true
	case errors.Is(err, network.ErrUnexpectedStatus):
		// It affects that file only, but the host did answer.
		s.answered[host] = true
	default:
		s.answered[host] = false
	}
}

// ForgetMissing drops known-missing answers for files under prefix, so they are asked about again.
func (s *RemoteSource) ForgetMissing(prefix string) {
	for f := range s.missing {
		if strings.HasPrefix(string(f), prefix) {
			delete(s.missing, f)
		}
	}
}

// Confirm records that a host answered something else, normally extensions.json at join.
func (s *RemoteSource) Confirm(host string) { s.answered[host] = true }

// Host returns the host a file lives on.
func (s *RemoteSource) Host(f File) string { return hostOf(f) }

// Limit returns how many workers a host may use now.
func (s *RemoteSource) Limit(host string) int {
	if s.answered[host] {
		return HostAnsweredWorkers
	}
	return HostStartWorkers
}

// Size reports both tables.
func (s *RemoteSource) Size() Contents {
	return Contents{Name: "remote index", Entries: len(s.exists) + len(s.missing)}
}

func hostOf(f File) string {
	u, err := url.Parse(string(f))
	if err != nil {
		return ""
	}
	return u.Host
}
