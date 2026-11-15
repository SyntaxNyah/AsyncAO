package media

import (
	"bytes"
	"compress/gzip"
	"maps"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// rememberFile is what a session would write to learned.json.
func rememberFile(t *testing.T, r *remoteRig) []byte {
	t.Helper()
	var mem Memory
	if !r.m.Call(func() { mem = Remember(r.m, r.builder, r.remote) }) {
		t.Fatal("manager stopped")
	}
	data, err := EncodeLearned(mem.Learned())
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// recallFile is what the next session does with it.
func recallFile(t *testing.T, r *remoteRig, data []byte) {
	t.Helper()
	l, err := DecodeLearned(data)
	if err != nil {
		t.Fatal(err)
	}
	mem := l.Memory()
	r.m.Call(func() { Recall(r.m, r.builder, r.remote, mem) })
}

func TestNextSessionAsksTheLearnedFormatAndWinnerFirst(t *testing.T) {
	files := map[string][]byte{
		testOrigin + "a" + config.ExtPNG: []byte("x"),
		testOrigin + "b" + config.ExtPNG: []byte("y"),
	}
	first := newRemoteRig(t, files)
	waitFinal(t, first.m.Get(sprite("a"), Now, 0))
	saved := rememberFile(t, first)

	next := newRemoteRig(t, files)
	recallFile(t, next, saved)
	waitFinal(t, next.m.Get(sprite("a"), Now, 0))
	waitFinal(t, next.m.Get(sprite("b"), Now, 0))
	want := []string{testOrigin + "a" + config.ExtPNG, testOrigin + "b" + config.ExtPNG}
	if asked := next.fetch.askedFor(); len(asked) != len(want) || asked[0] != want[0] || asked[1] != want[1] {
		t.Fatalf("asked %v, want %v", asked, want)
	}
}

func TestEveryWinnerShapeSurvivesTheFile(t *testing.T) {
	const mirror = "https://mirror.test/base/"
	under := Key{Place: PlaceServer, Path: testOrigin + "characters/c/(a)e", Kind: KindImage, Class: ClassEmote, Tag: 3}
	own := Key{Place: PlaceServer, Origin: testOrigin, Path: "characters/c/(b)e", Kind: KindImage, Class: ClassEmote}
	sized := Key{Place: PlaceServer, Path: testOrigin + "characters/c/char_icon", Kind: KindImage,
		Class: ClassCharIcon, Size: 64}
	tabbed := Key{Place: PlaceServer, Path: testOrigin + "characters/c/a\tb", Kind: KindImage, Class: ClassEmote}
	saved := map[Key]File{
		under:  File(testOrigin + "characters/c/e.webp"),    // the bare spelling won
		own:    File(testOrigin + "characters/c/(b)e.png"),  // the key's own name won
		sized:  File(mirror + "characters/c/char_icon.png"), // found on an extra host
		tabbed: File(testOrigin + "characters/c/a\tb.png"),
	}
	r := newRemoteRig(t, nil)
	r.m.Call(func() {
		r.builder.Allow(testOrigin, map[Class][]string{ClassEmote: {config.ExtWebP}})
		maps.Copy(r.m.winners, saved)
	})
	data := rememberFile(t, r)

	next := newRemoteRig(t, nil)
	next.m.Call(func() { next.builder.Allow(testOrigin, map[Class][]string{ClassEmote: {config.ExtWebP}}) })
	recallFile(t, next, data)
	var got map[Key]File
	next.m.Call(func() { got = maps.Clone(next.m.winners) })
	delete(saved, tabbed) // a line can't hold a tab, so that one is left out
	if !maps.Equal(got, saved) {
		t.Fatalf("recalled %v\nwant %v", got, saved)
	}
}

func TestRecallIgnoresAnotherVersionAndDamage(t *testing.T) {
	r := newRemoteRig(t, nil)
	formats := map[Class]map[string]int{ClassEmote: {config.ExtPNG: 1, ".exe": 1}, ClassExact: {config.ExtPNG: 1}}
	r.m.Call(func() {
		Recall(r.m, r.builder, r.remote, Learned{Version: LearnedVersion + 1, Hosts: map[string]*LearnedHost{
			testOrigin: {Formats: formats, Winners: []string{"0\t0\t2\t0\t0\t0\ta\t+.png"}},
		}}.Memory())
		Recall(r.m, r.builder, r.remote, Learned{Version: LearnedVersion, Hosts: map[string]*LearnedHost{
			testOrigin: {Formats: map[Class]map[string]int{ClassEmote: {".exe": 1}},
				Winners: []string{"short", "0\t0\t99\t0\t0\t0\ta\t+.png", "0\t0\t2\t0\t0\t9\ta\t+.png",
					"0\t0\t2\t0\t256\t0\ta\t+.png", "0\t0\t2\t-1\t0\t0\ta\t+.png"}},
			"nil.test": nil,
		}}.Memory())
	})
	var mem Memory
	r.m.Call(func() { mem = Remember(r.m, r.builder, r.remote) })
	if l := mem.Learned(); len(l.Hosts) != 0 {
		t.Fatalf("recalled %+v", l.Hosts[testOrigin])
	}
}

func TestDecodeRefusesDamagedAndOversizedFiles(t *testing.T) {
	if _, err := DecodeLearned([]byte("{}")); err == nil {
		t.Error("a file that isn't gzip decoded")
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(make([]byte, learnedMaxBytes+1)); err != nil {
		t.Fatal(err)
	}
	_ = zw.Close()
	if _, err := DecodeLearned(buf.Bytes()); err == nil {
		t.Error("a file past the size cap decoded")
	}
}

func TestCallReportsAStoppedManager(t *testing.T) {
	r := newRemoteRig(t, nil)
	r.m.Close()
	if r.m.Call(func() {}) {
		t.Fatal("Call ran on a stopped manager")
	}
}
