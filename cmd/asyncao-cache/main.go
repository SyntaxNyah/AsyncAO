// Command asyncao-cache is the companion CLI for AsyncAO's T3 disk cache:
// inspect it, prune it, or pre-seed/warm it from a server's asset origin —
// all without launching the client. Pure Go (no SDL, no CGO): builds with
// CGO_ENABLED=0 on any platform.
//
// Usage:
//
//	asyncao-cache stats                          point-in-time size/count
//	asyncao-cache inspect [-top 20]              largest blobs + age spread
//	asyncao-cache prune -older 720h              drop blobs older than DURATION
//	asyncao-cache prune -max-bytes 1073741824    drop oldest until under BYTES
//	asyncao-cache prune -all                     clear everything
//	asyncao-cache warm -list urls.txt [-j 8]     fetch URLs into the cache
//	asyncao-cache warm -base URL -chars chars.txt  warm char icons via the
//	                                             origin's extensions.json
//
// Keys are xxhash64 of the FULL asset URL (per-server separation is
// structural), so inspect cannot map blobs back to URLs — by design.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/cache"
)

const (
	// warmBlobMax skips pathological downloads (nothing AO-shaped is
	// bigger; a 64 MiB "sprite" is a misconfigured server).
	warmBlobMax = 64 << 20
	// warmTimeout bounds one fetch.
	warmTimeout = 20 * time.Second
	// warmWorkersMax bounds -j (be polite to asset hosts).
	warmWorkersMax = 32
	// charIconStem mirrors webAO's characters/<name>/char_icon convention.
	charIconStem = "char_icon"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "stats":
		err = cmdStats()
	case "inspect":
		err = cmdInspect(os.Args[2:])
	case "prune":
		err = cmdPrune(os.Args[2:])
	case "warm":
		err = cmdWarm(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "asyncao-cache:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `asyncao-cache — AsyncAO T3 disk cache companion
  stats                              size + blob count
  inspect [-top N]                   largest blobs, age spread
  prune -older 720h | -max-bytes N | -all
  warm -list urls.txt [-j N]         fetch a URL list into the cache
  warm -base ORIGIN -chars file [-j N]
                                     warm char icons (extension from the
                                     origin's extensions.json when present)`)
}

func cacheRoot() (string, error) {
	root, err := cache.DefaultDiskRoot()
	if err != nil {
		return "", err
	}
	return root, nil
}

// walkBlobs visits every blob file under the cache root.
func walkBlobs(root string, fn func(path string, info os.FileInfo)) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil // sharding dirs; unreadable entries skipped
		}
		fn(path, info)
		return nil
	})
}

func cmdStats() error {
	root, err := cacheRoot()
	if err != nil {
		return err
	}
	var count int
	var bytes int64
	oldest, newest := time.Time{}, time.Time{}
	if err := walkBlobs(root, func(_ string, info os.FileInfo) {
		count++
		bytes += info.Size()
		mt := info.ModTime()
		if oldest.IsZero() || mt.Before(oldest) {
			oldest = mt
		}
		if mt.After(newest) {
			newest = mt
		}
	}); err != nil {
		return err
	}
	fmt.Printf("root:   %s\nblobs:  %d\nbytes:  %d (%.1f MiB)\n", root, count, bytes, float64(bytes)/(1<<20))
	if count > 0 {
		fmt.Printf("oldest: %s\nnewest: %s\n", oldest.Format(time.RFC3339), newest.Format(time.RFC3339))
	}
	return nil
}

type blobInfo struct {
	path string
	size int64
	mod  time.Time
}

func cmdInspect(args []string) error {
	fs := flag.NewFlagSet("inspect", flag.ExitOnError)
	top := fs.Int("top", 20, "show the N largest blobs")
	_ = fs.Parse(args)

	root, err := cacheRoot()
	if err != nil {
		return err
	}
	var blobs []blobInfo
	if err := walkBlobs(root, func(path string, info os.FileInfo) {
		blobs = append(blobs, blobInfo{path: path, size: info.Size(), mod: info.ModTime()})
	}); err != nil {
		return err
	}
	sort.Slice(blobs, func(i, j int) bool { return blobs[i].size > blobs[j].size })
	n := *top
	if n > len(blobs) {
		n = len(blobs)
	}
	fmt.Printf("%d blobs; %d largest (keys are xxhash64(url) — URLs are not recoverable by design):\n", len(blobs), n)
	for _, b := range blobs[:n] {
		fmt.Printf("  %10d  %s  %s\n", b.size, b.mod.Format("2006-01-02"), filepath.Base(b.path))
	}
	return nil
}

func cmdPrune(args []string) error {
	fs := flag.NewFlagSet("prune", flag.ExitOnError)
	older := fs.Duration("older", 0, "drop blobs not touched in this long (e.g. 720h)")
	maxBytes := fs.Int64("max-bytes", 0, "drop oldest blobs until total is under this")
	all := fs.Bool("all", false, "clear the whole cache")
	_ = fs.Parse(args)

	root, err := cacheRoot()
	if err != nil {
		return err
	}
	if *all {
		d, err := cache.NewDiskCache(root, 0) // CLI tool: no auto-prune (0 = unlimited)
		if err != nil {
			return err
		}
		defer d.Close()
		if err := d.Clear(); err != nil {
			return err
		}
		fmt.Println("cleared", root)
		return nil
	}
	if *older == 0 && *maxBytes == 0 {
		return fmt.Errorf("prune needs -older, -max-bytes, or -all")
	}

	var blobs []blobInfo
	var total int64
	if err := walkBlobs(root, func(path string, info os.FileInfo) {
		blobs = append(blobs, blobInfo{path: path, size: info.Size(), mod: info.ModTime()})
		total += info.Size()
	}); err != nil {
		return err
	}
	cutoff := time.Now().Add(-*older)
	removed, freed := 0, int64(0)
	if *older > 0 {
		for _, b := range blobs {
			if b.mod.Before(cutoff) {
				if os.Remove(b.path) == nil {
					removed++
					freed += b.size
					total -= b.size
				}
			}
		}
	}
	if *maxBytes > 0 && total > *maxBytes {
		// Oldest-first until under budget.
		sort.Slice(blobs, func(i, j int) bool { return blobs[i].mod.Before(blobs[j].mod) })
		for _, b := range blobs {
			if total <= *maxBytes {
				break
			}
			if os.Remove(b.path) == nil {
				removed++
				freed += b.size
				total -= b.size
			}
		}
	}
	fmt.Printf("pruned %d blobs (%.1f MiB freed); %.1f MiB remain\n",
		removed, float64(freed)/(1<<20), float64(total)/(1<<20))
	return nil
}

func cmdWarm(args []string) error {
	fs := flag.NewFlagSet("warm", flag.ExitOnError)
	list := fs.String("list", "", "file with one asset URL per line")
	base := fs.String("base", "", "asset origin (e.g. https://assets.example/base)")
	chars := fs.String("chars", "", "file with one character folder name per line (needs -base)")
	workers := fs.Int("j", 8, "concurrent fetches")
	_ = fs.Parse(args)

	var urls []string
	switch {
	case *list != "":
		var err error
		if urls, err = readLines(*list); err != nil {
			return err
		}
	case *base != "" && *chars != "":
		names, err := readLines(*chars)
		if err != nil {
			return err
		}
		ext := manifestIconExt(strings.TrimRight(*base, "/"))
		for _, name := range names {
			urls = append(urls, strings.TrimRight(*base, "/")+"/characters/"+name+"/"+charIconStem+ext)
		}
		fmt.Printf("warming %d char icons as %s (extension via %s)\n", len(urls), ext, assets.ManifestFileName)
	default:
		return fmt.Errorf("warm needs -list, or -base with -chars")
	}
	if len(urls) == 0 {
		return fmt.Errorf("nothing to warm")
	}
	if *workers < 1 {
		*workers = 1
	}
	if *workers > warmWorkersMax {
		*workers = warmWorkersMax
	}

	root, err := cacheRoot()
	if err != nil {
		return err
	}
	d, err := cache.NewDiskCache(root, 0) // CLI tool: no auto-prune (0 = unlimited)
	if err != nil {
		return err
	}
	defer d.Close()

	client := &http.Client{Timeout: warmTimeout}
	var okN, missN, failN atomic.Int64
	jobs := make(chan string)
	var wg sync.WaitGroup
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for u := range jobs {
				if _, have := d.Get(u); have {
					okN.Add(1) // already warm
					continue
				}
				switch data, status := fetch(client, u); {
				case status == http.StatusOK && len(data) > 0:
					d.Put(u, data)
					okN.Add(1)
				case status == http.StatusNotFound:
					missN.Add(1)
				default:
					failN.Add(1)
				}
			}
		}()
	}
	for _, u := range urls {
		jobs <- u
	}
	close(jobs)
	wg.Wait()
	d.Close() // drain the async writer before reporting
	st := d.Stats()
	fmt.Printf("warm done: %d ok, %d missing (404), %d failed; %d writes dropped (queue)\n",
		okN.Load(), missN.Load(), failN.Load(), st.Dropped)
	return nil
}

// fetch GETs one URL with the size cap; returns (body, status).
func fetch(client *http.Client, url string) ([]byte, int) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, warmBlobMax+1))
	if err != nil || len(data) == 0 || len(data) > warmBlobMax {
		return nil, 0
	}
	return data, http.StatusOK
}

// manifestIconExt asks the origin's extensions.json for the char-icon
// extension, defaulting to .png (webAO behavior without a manifest).
func manifestIconExt(base string) string {
	client := &http.Client{Timeout: warmTimeout}
	data, status := fetch(client, base+"/"+assets.ManifestFileName)
	if status != http.StatusOK {
		return ".png"
	}
	m, err := assets.ParseManifest(data)
	if err != nil || len(m.CharIcon) == 0 {
		return ".png"
	}
	return m.CharIcon[0]
}

// readLines loads a file as trimmed, non-empty lines.
func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}
	return out, sc.Err()
}
