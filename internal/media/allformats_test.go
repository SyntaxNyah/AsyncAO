package media

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// allFormatsRig chains the player's files (every format) ahead of a server whose list builder decides.
func allFormatsRig(t *testing.T, builder *CandidateBuilder, own, server Source) *Manager {
	t.Helper()
	pool := NewPool("disk", testWorkers, 0)
	compute := NewPool("compute", testWorkers, 1)
	m := NewManager(Config{
		Chains:     map[Place]*Chain{PlaceServer: NewChain(Entry{Source: own, Pool: pool, AllFormats: true}, Entry{Source: server, Pool: pool})},
		Formats:    map[Kind]Format{KindImage: &fakeFormat{}},
		Candidates: builder.Build,
		AllFormats: EveryFormat,
		Found:      builder.Found,
		Compute:    compute,
	})
	t.Cleanup(func() {
		m.Close()
		pool.Close()
		compute.Close()
	})
	return m
}

func TestThePlayersFilesAreSearchedInFormatsTheServerDoesNotList(t *testing.T) {
	const path = "c/e"
	png := File(testOrigin + path + config.ExtPNG)
	builder := NewCandidateBuilder()
	builder.Allow(testOrigin, map[Class][]string{ClassEmote: {config.ExtWebP}})
	own := absentSource{newFakeSource("folders")}
	own.put(png, []byte("own"))
	server := absentSource{newFakeSource("server")}
	server.answer(File(testOrigin+path+config.ExtWebP), Unknown)
	server.answer(png, Unknown)
	m := allFormatsRig(t, builder, own, server)

	a := m.Get(sprite(path), Now, 0)
	waitFinal(t, a)
	if a.Result() != "own" {
		t.Fatalf("got %v, state %d: a .png in the player's folder was not found", a.Result(), a.State())
	}

	// The folder copy is gone; the server must still be asked only for the formats it lists.
	own.answer(png, No)
	m.Forget(sprite(path))
	again := m.Get(sprite(path), Now, 0)
	waitFinal(t, again)
	if n := server.calls.Load(); n != 1 {
		t.Fatalf("the server was asked %d times, want once for its .webp: the folder's .png reached it", n)
	}
}

func TestAFormatFoundInThePlayersFilesTeachesTheServerNothing(t *testing.T) {
	builder := NewCandidateBuilder()
	own := absentSource{newFakeSource("folders")}
	own.put(File(testOrigin+"c/a"+config.ExtPNG), []byte("own"))
	m := allFormatsRig(t, builder, own, absentSource{newFakeSource("server")})

	a := m.Get(sprite("c/a"), Now, 0)
	waitFinal(t, a)
	if a.Result() != "own" {
		t.Fatalf("got %v, state %d", a.Result(), a.State())
	}
	if first := builder.Build(sprite("c/b"), "c/b")[0]; first != File(testOrigin+"c/b"+config.ExtWebP) {
		t.Fatalf("the server is asked %s first: a folder hit reordered its formats", first)
	}
}
