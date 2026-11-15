package media

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

const charRoot = testOrigin + "characters/c/"

// listing is an nginx-style folder page linking each name; a name ending in "/" is a folder.
func listing(names ...string) []byte {
	var b strings.Builder
	b.WriteString(`<pre><a href="../">../</a>`)
	for _, n := range names {
		fmt.Fprintf(&b, `<a href="%s">%s</a>`, n, n)
	}
	b.WriteString(`</pre>`)
	return []byte(b.String())
}

// mapAndWait maps root and waits until its walk has nothing out.
func mapAndWait(t *testing.T, r *remoteRig, root File) {
	t.Helper()
	r.m.Call(func() { MapCharacter(r.m, r.network, r.remote, r.builder, root) })
	waitUntil(t, func() bool {
		done := false
		r.m.Call(func() { done = r.remote.Mapped(root) })
		return done
	})
}

// askedSince is what the host was asked after the first n requests.
func askedSince(r *remoteRig, n int) []string { return r.fetch.askedFor()[n:] }

func TestMappedFolderAnswersMissingFilesWithoutRequests(t *testing.T) {
	r := newRemoteRig(t, map[string][]byte{
		charRoot:                       listing("e.png", "emotions/"),
		charRoot + "emotions/":         listing("button1_off.png"),
		charRoot + "e" + config.ExtPNG: []byte("x"),
	})
	mapAndWait(t, r, charRoot)
	before := len(r.fetch.askedFor())

	a := r.m.Get(sprite("characters/c/(a)e"), Now, 0, "characters/c/e")
	waitFinal(t, a)
	if !a.Ready() {
		t.Fatalf("state %d err %v", a.State(), a.Err())
	}
	if got := askedSince(r, before); !slices.Equal(got, []string{charRoot + "e" + config.ExtPNG}) {
		t.Errorf("asked %v, want only the listed spelling and format", got)
	}

	before = len(r.fetch.askedFor())
	b := r.m.Get(sprite("characters/c/(a)/e"), Now, 0)
	waitFinal(t, b)
	if b.State() != Missing || len(askedSince(r, before)) != 0 {
		t.Errorf("a file in a folder the listing doesn't show: state %d, asked %v", b.State(), askedSince(r, before))
	}
}

// A case-insensitive host serves "normal.png" for a listed "Normal.PNG", so that file must still be asked for.
func TestListedNamesMatchWhateverTheirCase(t *testing.T) {
	r := newRemoteRig(t, map[string][]byte{
		charRoot:                            listing("Normal.PNG"),
		charRoot + "normal" + config.ExtPNG: []byte("x"),
	})
	mapAndWait(t, r, charRoot)
	before := len(r.fetch.askedFor())
	a := r.m.Get(sprite("characters/c/normal"), Now, 0)
	waitFinal(t, a)
	if !a.Ready() || !slices.Equal(askedSince(r, before), []string{charRoot + "normal" + config.ExtPNG}) {
		t.Errorf("state %d, asked %v", a.State(), askedSince(r, before))
	}
}

// A folder answered with an index page or a 403 proves nothing: the host keeps the per-file search for the session.
func TestHostWithoutListingsKeepsSearching(t *testing.T) {
	for name, serve := range map[string]func(*remoteRig){
		"index page": func(r *remoteRig) { r.fetch.add(charRoot, []byte(`<a href="e.png">e.png</a>`)) },
		"403":        func(r *remoteRig) { r.fetch.setBroken(fmt.Errorf("%w: 403", network.ErrUnexpectedStatus)) },
	} {
		t.Run(name, func(t *testing.T) {
			r := newRemoteRig(t, map[string][]byte{charRoot + "e" + config.ExtPNG: []byte("x")})
			serve(r)
			mapAndWait(t, r, charRoot)
			r.fetch.setBroken(nil)
			a := r.m.Get(sprite("characters/c/e"), Now, 0)
			waitFinal(t, a)
			if !a.Ready() {
				t.Fatalf("state %d: an unproven folder blocked a real file", a.State())
			}
			other := File(testOrigin + "characters/d/")
			started := false
			r.m.Call(func() {
				MapCharacter(r.m, r.network, r.remote, r.builder, other)
				_, started = r.remote.walks[folderKey(string(other))]
			})
			if started {
				t.Error("another walk started on a host that showed it gives no listings")
			}
		})
	}
}

// A folder that 404s proves nothing and is not walked again until the map is old.
func TestMissingFolderIsWalkedAgainOnlyWhenOld(t *testing.T) {
	r := newRemoteRig(t, map[string][]byte{})
	mapAndWait(t, r, charRoot)
	current := func() *walk {
		var w *walk
		r.m.Call(func() {
			MapCharacter(r.m, r.network, r.remote, r.builder, charRoot)
			w = r.remote.walks[folderKey(charRoot)]
		})
		return w
	}
	first := current()
	if current() != first {
		t.Fatal("a fresh walk was repeated")
	}
	r.clock.add(MapMaxAge)
	if current() == first {
		t.Error("an old walk was not repeated")
	}
}

// Retry missing drops the maps under its prefix, so a sprite added to the server since is found.
func TestForgetMissingDropsTheMap(t *testing.T) {
	r := newRemoteRig(t, map[string][]byte{charRoot: listing("e.png")})
	mapAndWait(t, r, charRoot)
	r.fetch.add(charRoot+"new"+config.ExtPNG, []byte("x"))
	r.m.Call(func() { r.remote.ForgetMissing(testOrigin) })
	a := r.m.Get(sprite("characters/c/new"), Now, 0)
	waitFinal(t, a)
	if !a.Ready() {
		t.Errorf("state %d: the dropped map still answered", a.State())
	}
}

func TestExtraHostsAreMappedToo(t *testing.T) {
	const mirror = "http://mirror.test/base/"
	r := newRemoteRig(t, map[string][]byte{charRoot: listing("e.png")})
	r.m.Call(func() { r.builder.SetHosts(testOrigin, []string{mirror}) })
	mapAndWait(t, r, charRoot)
	mapAndWait(t, r, File(mirror+"characters/c/"))
	if !slices.Contains(r.fetch.askedFor(), mirror+"characters/c/") {
		t.Errorf("asked %v, want the mirror's folder listed", r.fetch.askedFor())
	}
}

func TestWalkStopsAtItsDepth(t *testing.T) {
	files := map[string][]byte{}
	folder := charRoot
	for range mapDepth + 2 {
		files[folder] = listing("d/")
		folder += "d/"
	}
	r := newRemoteRig(t, files)
	mapAndWait(t, r, charRoot)
	if n := len(r.fetch.askedFor()); n != mapDepth {
		t.Errorf("listed %d folders, want %d", n, mapDepth)
	}
}

func TestCharacterRoot(t *testing.T) {
	for in, want := range map[string]File{
		testOrigin + "characters/Phoenix/(a)normal":  testOrigin + "characters/Phoenix/",
		testOrigin + "characters/Phoenix/(a)/normal": testOrigin + "characters/Phoenix/",
		testOrigin + "background/court/defenseempty": "",
		testOrigin + "characters/":                   "",
	} {
		if got := CharacterRoot(in); got != want {
			t.Errorf("CharacterRoot(%q) = %q, want %q", in, got, want)
		}
	}
}

// A map kept in learned.json answers in the next session without a listing, and an old one is walked again.
func TestMapOutlivesTheSession(t *testing.T) {
	files := map[string][]byte{
		charRoot:                       listing("e.png", "emotions/"),
		charRoot + "emotions/":         listing("button1_off.png"),
		charRoot + "e" + config.ExtPNG: []byte("x"),
	}
	first := newRemoteRig(t, files)
	mapAndWait(t, first, charRoot)
	saved := rememberFile(t, first)

	next := newRemoteRig(t, files)
	recallFile(t, next, saved)
	mapped := false
	next.m.Call(func() { mapped = next.remote.Mapped(charRoot) })
	if !mapped {
		t.Fatal("the saved map was not recalled")
	}
	a := next.m.Get(sprite("characters/c/(a)e"), Now, 0, "characters/c/e")
	waitFinal(t, a)
	if got := next.fetch.askedFor(); !a.Ready() || !slices.Equal(got, []string{charRoot + "e" + config.ExtPNG}) {
		t.Errorf("state %d, asked %v: the recalled map should answer every other spelling", a.State(), got)
	}

	var before *walk
	next.m.Call(func() { before = next.remote.walks[folderKey(charRoot)] })
	next.clock.add(MapMaxAge)
	var after *walk
	next.m.Call(func() {
		MapCharacter(next.m, next.network, next.remote, next.builder, charRoot)
		after = next.remote.walks[folderKey(charRoot)]
	})
	if after == before {
		t.Error("an old saved map was not walked again on use")
	}
}
