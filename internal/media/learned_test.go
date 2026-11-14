package media

import (
	"encoding/json"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// rememberJSON is what a session would write to learned.json.
func rememberJSON(t *testing.T, r *remoteRig) []byte {
	t.Helper()
	var l Learned
	if !r.m.Call(func() { l = Remember(r.m, r.builder) }) {
		t.Fatal("manager stopped")
	}
	data, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// recallJSON is what the next session does with it.
func recallJSON(t *testing.T, r *remoteRig, data []byte) {
	t.Helper()
	var l Learned
	if err := json.Unmarshal(data, &l); err != nil {
		t.Fatal(err)
	}
	r.m.Call(func() { Recall(r.m, r.builder, l) })
}

func TestNextSessionAsksTheLearnedFormatAndWinnerFirst(t *testing.T) {
	files := map[string][]byte{
		testOrigin + "a" + config.ExtPNG: []byte("x"),
		testOrigin + "b" + config.ExtPNG: []byte("y"),
	}
	first := newRemoteRig(t, files)
	waitFinal(t, first.m.Get(sprite("a"), Now, 0))
	saved := rememberJSON(t, first)

	next := newRemoteRig(t, files)
	recallJSON(t, next, saved)
	waitFinal(t, next.m.Get(sprite("a"), Now, 0))
	waitFinal(t, next.m.Get(sprite("b"), Now, 0))
	want := []string{testOrigin + "a" + config.ExtPNG, testOrigin + "b" + config.ExtPNG}
	if asked := next.fetch.askedFor(); len(asked) != len(want) || asked[0] != want[0] || asked[1] != want[1] {
		t.Fatalf("asked %v, want %v", asked, want)
	}
}

func TestRecallIgnoresAnotherVersionAndUnknownFormats(t *testing.T) {
	r := newRemoteRig(t, nil)
	recallJSON(t, r, []byte(`{"version":999,"formats":{"`+testOrigin+`":{"2":{".png":50}}}}`))
	recallJSON(t, r, []byte(`{"version":1,"formats":{"`+testOrigin+`":{"2":{".exe":50}}}}`))
	var l Learned
	r.m.Call(func() { l = Remember(r.m, r.builder) })
	if len(l.Formats) != 0 {
		t.Fatalf("recalled %v", l.Formats)
	}
}

func TestCallReportsAStoppedManager(t *testing.T) {
	r := newRemoteRig(t, nil)
	r.m.Close()
	if r.m.Call(func() {}) {
		t.Fatal("Call ran on a stopped manager")
	}
}
