// Package logbrowser is the pure logic + disk IO behind the log browser screen:
// scanning the logs/ tree, loading a scope off-thread, and filtering/stats over the
// loaded lines. It holds no UI state (scroll, selection, query) and never touches SDL
// or App — ui keeps those and calls this package's functions.
package logbrowser

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

const (
	// transcriptStampLayout must stay in sync with internal/ui/translog.go's
	// transcriptStampLayout: the writer names files with it and SessionLabel parses
	// them back.
	transcriptStampLayout = "2006-01-02_15-04-05"
	sessionLabelLayout    = "2006-01-02 15:04"

	maxLogServers    = 200
	maxLogSessions   = 500
	maxLogScopeFiles = 400
	maxLogScopeLines = 20000
	maxLogFileBytes  = 16 << 20
	maxLogLineRunes  = 600

	// MaxScopeLines is the scope-load line cap, exposed for the browser's
	// "truncated" indicator.
	MaxScopeLines = maxLogScopeLines
)

type Session struct {
	File  string
	Label string
}

type Line struct {
	Server  string
	Session string
	Text    string
	Lower   string
	Who     string
}

type Stat struct {
	Name  string
	Lines int
	Words int
}

type Result struct {
	Gen      int
	Servers  []string
	Sessions []Session
	Lines    []Line
}

func RootDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(exe), "logs")
}

func ScanServers(root string) []string {
	if root == "" {
		return nil
	}
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(ents))
	for _, e := range ents {
		if e.IsDir() {
			out = append(out, e.Name())
			if len(out) >= maxLogServers {
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

func ScanSessions(root, server string) []Session {
	if root == "" || server == "" {
		return nil
	}
	ents, err := os.ReadDir(filepath.Join(root, server))
	if err != nil {
		return nil
	}
	type fe struct {
		name string
		mod  time.Time
	}
	files := make([]fe, 0, len(ents))
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		mt := time.Time{}
		if info, err := e.Info(); err == nil {
			mt = info.ModTime()
		}
		files = append(files, fe{e.Name(), mt})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	if len(files) > maxLogSessions {
		files = files[:maxLogSessions]
	}
	out := make([]Session, len(files))
	for i, f := range files {
		out[i] = Session{File: f.name, Label: SessionLabel(f.name)}
	}
	return out
}

func Load(root, server, session string, gen int) Result {
	out := Result{Gen: gen}
	out.Servers = ScanServers(root)
	if server != "" {
		out.Sessions = ScanSessions(root, server)
	}
	out.Lines = readLogScope(root, server, session)
	return out
}

func FilterLines(lines []Line, query string, useRegex bool, who string) []int {
	who = strings.ToLower(strings.TrimSpace(who))
	q := strings.TrimSpace(query)
	var re *regexp.Regexp
	if useRegex && q != "" {
		re, _ = regexp.Compile("(?i)" + q)
	}
	ql := strings.ToLower(q)
	idx := make([]int, 0, 64)
	for i := range lines {
		if who != "" && strings.ToLower(lines[i].Who) != who {
			continue
		}
		switch {
		case q == "":
		case re != nil:
			if !re.MatchString(lines[i].Text) {
				continue
			}
		default:
			if !strings.Contains(lines[i].Lower, ql) {
				continue
			}
		}
		idx = append(idx, i)
	}
	return idx
}

func ValidRegex(q string) bool {
	_, err := regexp.Compile("(?i)" + strings.TrimSpace(q))
	return err == nil
}

func ComputeStats(lines []Line) (stats []Stat, totalLines, totalWords, sessions int) {
	byName := map[string]*Stat{}
	sess := map[string]bool{}
	for i := range lines {
		ln := &lines[i]
		sess[ln.Session] = true
		w := len(strings.Fields(ln.Text))
		totalLines++
		totalWords += w
		who := ln.Who
		if who == "" {
			who = "(server)"
		}
		s := byName[who]
		if s == nil {
			s = &Stat{Name: who}
			byName[who] = s
		}
		s.Lines++
		s.Words += w
	}
	stats = make([]Stat, 0, len(byName))
	for _, s := range byName {
		stats = append(stats, *s)
	}
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].Lines != stats[j].Lines {
			return stats[i].Lines > stats[j].Lines
		}
		return stats[i].Name < stats[j].Name
	})
	return stats, totalLines, totalWords, len(sess)
}

func DistinctChars(lines []Line) []string {
	seen := map[string]bool{}
	out := make([]string, 0, 32)
	for i := range lines {
		w := lines[i].Who
		if w == "" || seen[strings.ToLower(w)] {
			continue
		}
		seen[strings.ToLower(w)] = true
		out = append(out, w)
		if len(out) >= 200 {
			break
		}
	}
	sort.Strings(out)
	return out
}

func CycleChar(chars []string, cur string) string {
	if cur == "" {
		if len(chars) > 0 {
			return chars[0]
		}
		return ""
	}
	for i, ch := range chars {
		if ch == cur {
			if i+1 < len(chars) {
				return chars[i+1]
			}
			return ""
		}
	}
	return ""
}

func SessionLabel(file string) string {
	name := strings.TrimSuffix(file, ".log")
	if t, err := time.Parse(transcriptStampLayout, name); err == nil {
		return t.Format(sessionLabelLayout)
	}
	if i := strings.LastIndexByte(name, '-'); i > 0 {
		if seq, err := strconv.Atoi(name[i+1:]); err == nil && seq > 1 {
			if t, err := time.Parse(transcriptStampLayout, name[:i]); err == nil {
				return t.Format(sessionLabelLayout) + " (tab " + strconv.Itoa(seq) + ")"
			}
		}
	}
	return name
}

func TruncateRunes(s string, max int) string {
	if max <= 0 {
		return s
	}
	count := 0
	for i := range s {
		if count == max {
			return s[:i] + "…"
		}
		count++
	}
	return s
}

// ParseWho extracts the speaker from a transcript line "[ts] who: message".
// Exported so internal/ui's transcript writer can cross-check that what it
// writes is still recoverable by the reader.
func ParseWho(line string) string {
	i := strings.Index(line, "] ")
	if i < 0 {
		return ""
	}
	rest := line[i+2:]
	j := strings.Index(rest, ": ")
	if j < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:j])
}

func readLogScope(root, server, session string) []Line {
	if root == "" {
		return nil
	}
	lines := make([]Line, 0, 256)
	files := 0
	addFile := func(srv, file string) bool {
		if files >= maxLogScopeFiles || len(lines) >= maxLogScopeLines {
			return false
		}
		files++
		path := filepath.Join(root, srv, file)
		info, err := os.Stat(path)
		if err != nil || info.Size() > maxLogFileBytes {
			return true
		}
		f, err := os.Open(path)
		if err != nil {
			return true
		}
		defer f.Close()
		label := SessionLabel(file)
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			if len(lines) >= maxLogScopeLines {
				return false
			}
			t := courtroom.StripSpriteStyle(strings.TrimRight(sc.Text(), "\r"))
			if strings.TrimSpace(t) == "" {
				continue
			}
			t = TruncateRunes(t, maxLogLineRunes)
			lines = append(lines, Line{Server: srv, Session: label, Text: t, Lower: strings.ToLower(t), Who: ParseWho(t)})
		}
		return true
	}

	switch {
	case server != "" && session != "":
		addFile(server, session)
	case server != "":
		for _, s := range ScanSessions(root, server) {
			if !addFile(server, s.File) {
				break
			}
		}
	default:
		for _, srv := range ScanServers(root) {
			stop := false
			for _, s := range ScanSessions(root, srv) {
				if !addFile(srv, s.File) {
					stop = true
					break
				}
			}
			if stop {
				break
			}
		}
	}
	return lines
}
