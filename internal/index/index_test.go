package index

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"forge.axonibyte.com/axonibyte/cairn/internal/blob"
	"forge.axonibyte.com/axonibyte/cairn/internal/canon"
	"forge.axonibyte.com/axonibyte/cairn/internal/contract"
	"forge.axonibyte.com/axonibyte/cairn/internal/log"
	"forge.axonibyte.com/axonibyte/cairn/internal/store"
)

// fixture is a store with a small world in it, and the seqs the arms refer
// to by meaning rather than by number.
type fixture struct {
	s                 *store.Store
	alpha, beta       string // concepts
	b1, b2, b3        string // blobs
	isAboutB3         uint64 // the is-about edge later retracted
	supersedesB2B1    uint64 // b2 supersedes b1
	retractsIsAboutB3 uint64
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	s, err := store.Init(t.TempDir(), 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{s: s}
	must := func(v uint64, err error) uint64 {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	f.alpha, _, err = s.NewConcept("alpha")
	if err != nil {
		t.Fatal(err)
	}
	f.beta, _, err = s.NewConcept("beta")
	if err != nil {
		t.Fatal(err)
	}
	put := func(text string) string {
		t.Helper()
		id, _, err := s.Put(strings.NewReader(text))
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	f.b1, f.b2, f.b3 = put("first draft"), put("second draft"), put("about beta")
	must(s.Edge("is-about", f.b1, f.alpha, nil))
	must(s.Edge("is-about", f.b2, f.alpha, nil))
	f.isAboutB3 = must(s.Edge("is-about", f.b3, f.beta, nil))
	must(s.Edge("cites", f.b2, f.b1, nil))
	f.supersedesB2B1 = must(s.Edge("supersedes", f.b2, f.b1, nil))
	ref := fmt.Sprintf("edge:%d", f.isAboutB3)
	f.retractsIsAboutB3 = must(s.Edge("retracts", ref, ref, nil))
	return f
}

// queries is the fixed set P-3 compares across a rebuild. Every relation
// the fixture uses appears, so a dropped edge of any kind moves an answer.
func (f *fixture) queries() []contract.Query {
	term := func(rel, o string) contract.Query {
		return contract.Query{Terms: []contract.Term{{Rel: rel, Object: o}}}
	}
	return []contract.Query{
		term("is-about", f.alpha),
		term("is-about", f.beta),
		term("cites", f.b1),
		term(contract.NameRel, "alpha"),
		term(contract.NameRel, "beta"),
		term("supersedes", f.b1),
		term("wrote", f.b1),
		{Terms: []contract.Term{{Rel: "is-about", Object: f.alpha}, {Rel: "cites", Object: f.b1}}},
		term("retracts", fmt.Sprintf("edge:%d", f.isAboutB3)),
	}
}

func readAll(s *store.Store) func(fn func(log.Verified) error) error {
	return func(fn func(log.Verified) error) error { return s.ReadLog(1, fn) }
}

func answers(t *testing.T, ix contract.Index, qs []contract.Query) [][]string {
	t.Helper()
	out := make([][]string, len(qs))
	for i, q := range qs {
		a, err := ix.Query(q)
		if err != nil {
			t.Fatalf("query %d: %v", i, err)
		}
		out[i] = a
	}
	return out
}

// TestRebuildCompares is §10.3's required test: an index built by applying
// edges as they are read and an index rebuilt from ReadLog(1) answer every
// query of the fixed set identically at the same At(). The CONTROL below
// proves the comparison can see one edge: the same stream with one edge's
// relation changed yields an index that answers differently, so a rebuild
// that silently lost an edge would not pass here.
func TestRebuildCompares(t *testing.T) {
	f := newFixture(t)
	live := New()
	if err := f.s.ReadLog(1, live.Apply); err != nil {
		t.Fatal(err)
	}
	rebuilt := New()
	if err := rebuilt.Rebuild(readAll(f.s)); err != nil {
		t.Fatal(err)
	}
	if live.At() != rebuilt.At() || live.At() != f.s.Head() {
		t.Fatalf("At: live %d rebuilt %d head %d", live.At(), rebuilt.At(), f.s.Head())
	}
	qs := f.queries()
	a, b := answers(t, live, qs), answers(t, rebuilt, qs)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("rebuild answers differ:\nlive    %v\nrebuilt %v", a, b)
	}
	// the answers are not all empty: an index that returned nothing for
	// everything would also compare equal
	nonEmpty := 0
	for _, x := range a {
		if len(x) > 0 {
			nonEmpty++
		}
	}
	if nonEmpty < 5 {
		t.Fatalf("only %d of %d queries answered anything; the comparison is vacuous", nonEmpty, len(a))
	}

	// CONTROL (P-3's mutant, driven rather than planted): the stream with the
	// cites edge turned into an instance relation the fixture never queries.
	// The rebuilt index must then DIFFER on at least one answer, or this test
	// could not have seen a lost edge.
	lossy := New()
	err := lossy.Rebuild(func(fn func(log.Verified) error) error {
		return f.s.ReadLog(1, func(v log.Verified) error {
			if v.Edge.Rel == "cites" {
				v.Edge.Rel = "test.dropped"
			}
			return fn(v)
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(a, answers(t, lossy, qs)) {
		t.Fatal("control: a rebuild missing the cites edge answered identically; the comparison cannot see a lost edge")
	}
}

// TestApplyRefusesGap: an index with a gap answers from a log that never
// was, so Apply refuses a seq that is not At()+1, by name, and At() stays.
func TestApplyRefusesGap(t *testing.T) {
	f := newFixture(t)
	var kept []log.Verified
	if err := f.s.ReadLog(1, func(v log.Verified) error { kept = append(kept, v); return nil }); err != nil {
		t.Fatal(err)
	}
	ix := New()
	if err := ix.Apply(kept[0]); err != nil {
		t.Fatal(err)
	}
	err := ix.Apply(kept[2])
	if !errors.Is(err, contract.ErrGap) {
		t.Fatalf("apply of seq %d at At() 1: %v, want ErrGap", kept[2].Edge.Seq, err)
	}
	if ix.At() != 1 {
		t.Fatalf("At() moved to %d on a refused edge", ix.At())
	}
	if err := ix.Apply(kept[1]); err != nil {
		t.Fatalf("the contiguous edge after a refusal: %v", err)
	}
}

// TestRetractedAbsentPresentInHistory is §7.1: the retracted is-about edge
// is absent from the query and readable as history with who withdrew it.
func TestRetractedAbsentPresentInHistory(t *testing.T) {
	f := newFixture(t)
	ix := New()
	if err := ix.Rebuild(readAll(f.s)); err != nil {
		t.Fatal(err)
	}
	got, err := ix.Query(contract.Query{Terms: []contract.Term{{Rel: "is-about", Object: f.beta}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("is-about beta after retraction: %v, want nothing", got)
	}
	rec, ok := ix.Edge(f.isAboutB3)
	if !ok || rec.Edge.S != f.b3 || rec.Edge.O != f.beta {
		t.Fatalf("history lost the retracted edge: %+v %v", rec, ok)
	}
	if rec.RetractedBy != f.retractsIsAboutB3 {
		t.Fatalf("retracted by %d, want %d", rec.RetractedBy, f.retractsIsAboutB3)
	}
	// the control: the un-retracted sibling is still answered
	got, _ = ix.Query(contract.Query{Terms: []contract.Term{{Rel: "is-about", Object: f.alpha}}})
	if len(got) == 0 {
		t.Fatal("control: is-about alpha answered nothing, so the arm above proves nothing")
	}
}

// TestCurrencyPerSupersedesChain is §5.4: b2 supersedes b1, so b1 is not
// current and is-about alpha answers b2 alone; retract the supersedes and
// b1 is current again. Both directions, so the arm cannot pass by never
// returning b1.
func TestCurrencyPerSupersedesChain(t *testing.T) {
	f := newFixture(t)
	ix := New()
	if err := ix.Rebuild(readAll(f.s)); err != nil {
		t.Fatal(err)
	}
	q := contract.Query{Terms: []contract.Term{{Rel: "is-about", Object: f.alpha}}}
	got, _ := ix.Query(q)
	if !reflect.DeepEqual(got, []string{f.b2}) {
		t.Fatalf("current under supersedes: %v, want [%s]", got, f.b2)
	}
	ref := fmt.Sprintf("edge:%d", f.supersedesB2B1)
	if _, err := f.s.Edge("retracts", ref, ref, nil); err != nil {
		t.Fatal(err)
	}
	if err := ix.Rebuild(readAll(f.s)); err != nil {
		t.Fatal(err)
	}
	got, _ = ix.Query(q)
	want := []string{f.b1, f.b2}
	if f.b2 < f.b1 {
		want = []string{f.b2, f.b1}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("after retracting the supersedes: %v, want %v", got, want)
	}
}

// TestStaleRefused is §10.2: asked for a consistent answer at a head the
// index has not reached, it refuses by name instead of answering from the
// past; at the head it answers.
func TestStaleRefused(t *testing.T) {
	f := newFixture(t)
	ix := New()
	if err := ix.Rebuild(readAll(f.s)); err != nil {
		t.Fatal(err)
	}
	q := contract.Query{Terms: []contract.Term{{Rel: "is-about", Object: f.alpha}}}
	if _, err := ix.QueryAt(q, f.s.Head()); err != nil {
		t.Fatalf("at the head: %v", err)
	}
	if _, err := f.s.Edge("is-about", f.b3, f.alpha, nil); err != nil {
		t.Fatal(err)
	}
	_, err := ix.QueryAt(q, f.s.Head())
	if !errors.Is(err, contract.ErrStale) {
		t.Fatalf("one edge behind: %v, want ErrStale", err)
	}
	if _, err := ix.Query(q); err != nil {
		t.Fatalf("Query without a head still answers from At(): %v", err)
	}
}

// TestQueryRefusals: no term is not a query; a dangling retracts is a
// defect in the stream, refused.
func TestQueryRefusals(t *testing.T) {
	ix := New()
	if _, err := ix.Query(contract.Query{}); !errors.Is(err, contract.ErrEmptyQuery) {
		t.Fatalf("empty query: %v", err)
	}
	e := canon.Edge{Seq: 1, Rel: "retracts", S: "edge:7", O: "edge:7"}
	if err := ix.Apply(log.Verified{Edge: e}); !errors.Is(err, contract.ErrDangling) {
		t.Fatalf("dangling retracts: %v", err)
	}
}

// TestStoreContract drives the file backend through contract.Store: a blob
// round trip with the hash verified on the way back, Append refusing a
// non-contiguous seq and a forged signature by name, ReadLog streaming from
// a seq, Head agreeing with the log.
func TestStoreContract(t *testing.T) {
	f := newFixture(t)
	var cs contract.Store = f.s
	id, err := cs.PutBlob(bytes.NewReader([]byte("contract bytes")))
	if err != nil {
		t.Fatal(err)
	}
	again, err := cs.PutBlob(bytes.NewReader([]byte("contract bytes")))
	if err != nil || again != id {
		t.Fatalf("PutBlob is not idempotent: %s %s %v", id, again, err)
	}
	got, err := cs.GetBlob(id)
	if err != nil || string(got) != "contract bytes" {
		t.Fatalf("GetBlob: %q %v", got, err)
	}
	// the hash is verified on read: corrupt the bytes on disk and GetBlob refuses
	path, err := f.s.Blobs.Path(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("contract bytez"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.GetBlob(id); !errors.Is(err, blob.ErrHashMismatch) {
		t.Fatalf("GetBlob over corrupt bytes: %v, want ErrHashMismatch", err)
	}
	if filepath.Base(path) == "" {
		t.Fatal("unreachable")
	}

	head := cs.Head()
	if head != f.s.Log.LastSeq() {
		t.Fatalf("Head %d, log %d", head, f.s.Log.LastSeq())
	}
	// a signed edge with the wrong seq is refused, and nothing is appended
	var last log.Verified
	if err := cs.ReadLog(head, func(v log.Verified) error { last = v; return nil }); err != nil {
		t.Fatal(err)
	}
	if last.Edge.Seq != head {
		t.Fatalf("ReadLog(head) streamed seq %d, want %d", last.Edge.Seq, head)
	}
	forged := last.Edge
	forged.Seq = head + 2
	if _, err := cs.Append(forged); err == nil {
		t.Fatal("Append of a non-contiguous seq was accepted")
	}
	replay := last.Edge // a valid edge replayed at the next seq carries a signature over the wrong form
	replay.Seq = head + 1
	replay.Prev = last.Hash
	if _, err := cs.Append(replay); err == nil {
		t.Fatal("Append of an edge whose signature does not cover its form was accepted")
	}
	if cs.Head() != head {
		t.Fatalf("a refused Append moved the head to %d", cs.Head())
	}
}
