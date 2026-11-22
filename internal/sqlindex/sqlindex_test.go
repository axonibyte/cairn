package sqlindex

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"forge.axonibyte.com/axonibyte/cairn/internal/canon"
	"forge.axonibyte.com/axonibyte/cairn/internal/contract"
	"forge.axonibyte.com/axonibyte/cairn/internal/index"
	"forge.axonibyte.com/axonibyte/cairn/internal/log"
	"forge.axonibyte.com/axonibyte/cairn/internal/query"
	"forge.axonibyte.com/axonibyte/cairn/internal/store"

	_ "modernc.org/sqlite"
)

// world is a store with every shape the two indexes must agree on: concepts
// and names, blobs, is-about, a cites chain, a supersedes chain of three, a
// retracted edge, a retracted retraction, a supersedes cycle reachable by
// cites, and a cites cycle.
type world struct {
	s                      *store.Store
	alpha, beta            string
	b1, b2, b3, x, y, z    string
	isAboutB3, supersedes2 uint64
}

func newWorld(t *testing.T) *world {
	t.Helper()
	s, err := store.Init(t.TempDir(), 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	w := &world{s: s}
	edge := func(rel, a, b string) uint64 {
		t.Helper()
		seq, err := s.Edge(rel, a, b, nil)
		if err != nil {
			t.Fatal(err)
		}
		return seq
	}
	put := func(text string) string {
		t.Helper()
		id, _, err := s.Put(strings.NewReader(text))
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	w.alpha, _, _ = s.NewConcept("alpha")
	w.beta, _, _ = s.NewConcept("beta")
	w.b1, w.b2, w.b3 = put("first"), put("second"), put("third")
	w.x, w.y, w.z = put("x"), put("y"), put("z")
	edge("is-about", w.b1, w.alpha)
	edge("is-about", w.b2, w.alpha)
	edge("is-about", w.b3, w.alpha)
	w.isAboutB3 = edge("is-about", w.b3, w.beta)
	edge("cites", w.b2, w.b1)
	edge("cites", w.b3, w.b2)
	w.supersedes2 = edge("supersedes", w.b2, w.b1)
	edge("supersedes", w.b3, w.b2)
	ref := fmt.Sprintf("edge:%d", w.isAboutB3)
	r := edge("retracts", ref, ref) // b3 is-about beta withdrawn
	rref := fmt.Sprintf("edge:%d", r)
	edge("retracts", rref, rref) // the withdrawal withdrawn: restores nothing (D-31)
	edge("supersedes", w.x, w.y)
	edge("supersedes", w.y, w.x) // a supersedes cycle
	edge("cites", w.z, w.x)
	edge("cites", w.x, w.y)
	edge("cites", w.y, w.x) // a cites cycle
	edge("is-about", w.z, w.beta)
	zy := edge("cites", w.z, w.y) // a cites edge that is then withdrawn: a walk must not take it
	zyref := fmt.Sprintf("edge:%d", zy)
	edge("retracts", zyref, zyref)
	edge("retracts", ref, ref) // b3 is-about beta withdrawn a SECOND time: RetractedBy stays the first (D-31)
	return w
}

// queries is the set both indexes answer; every relation and every traversal
// form appears, and some queries refuse, so the error identities are
// compared too.
func (w *world) queries(t *testing.T) []contract.Query {
	t.Helper()
	texts := []string{
		"is-about=" + w.alpha,
		"is-about=" + w.beta,
		"alpha", "beta", "gamma",
		"cites=" + w.b1,
		"supersedes=" + w.b1,
		"wrote=" + w.b1,
		"is-about=" + w.alpha + "/cites*1",
		"is-about=" + w.alpha + "/cites*2",
		"is-about=" + w.alpha + "/cites*64",
		"is-about=" + w.alpha + "/cites*1/supersedes^",
		"is-about=" + w.alpha + "/supersedes*2",
		"is-about=" + w.alpha + "/supersedes^",
		"is-about=" + w.beta + "/cites*64",
		"is-about=" + w.beta + "/cites*1/supersedes^",
		"is-about=" + w.beta + "/supersedes^",
		"retracts=edge:" + fmt.Sprint(w.isAboutB3),
		"is-about=" + w.beta + "/cites*1", // one hop from z: x, and NOT the withdrawn y (depth 64 reaches y through x, so only this depth sees it)
	}
	var qs []contract.Query
	for _, text := range texts {
		q, err := query.Parse(text)
		if err != nil {
			t.Fatal(err)
		}
		qs = append(qs, q)
	}
	// hand-built refusals the parser would not produce
	qs = append(qs,
		contract.Query{},
		contract.Query{Terms: []contract.Term{{Rel: "is-about", Object: w.alpha}}, Traversal: []contract.Traversal{{Rel: "cites", Depth: 0}}},
		contract.Query{Terms: []contract.Term{{Rel: "is-about", Object: w.alpha}}, Traversal: []contract.Traversal{{Rel: "is-about", Depth: 1}}},
		contract.Query{Terms: []contract.Term{{Rel: "is-about", Object: w.alpha}}, Traversal: []contract.Traversal{{Rel: "cites", Head: true}}},
	)
	return qs
}

type answer struct {
	Subjects []string
	Err      string // the sentinel's name, or ""
}

func sentinel(err error) string {
	for _, s := range []error{contract.ErrEmptyQuery, contract.ErrDepth, contract.ErrTraversalRel, contract.ErrNoHead, contract.ErrStale, contract.ErrPoisoned} {
		if errors.Is(err, s) {
			return s.Error()
		}
	}
	if err != nil {
		return "other: " + err.Error()
	}
	return ""
}

func answers(ix contract.Index, qs []contract.Query) []answer {
	out := make([]answer, len(qs))
	for i, q := range qs {
		got, err := ix.Query(q)
		out[i] = answer{Subjects: got, Err: sentinel(err)}
	}
	return out
}

func readAll(s *store.Store) func(fn func(log.Verified) error) error {
	return func(fn func(log.Verified) error) error { return s.ReadLog(1, fn) }
}

func openMem(t *testing.T) *SQLite {
	t.Helper()
	x, err := Open(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { x.Close() })
	return x
}

// TestTwoImplementationsAgree is TESTING.md's tier 2 for the index: the
// memory index and the SQLite index, rebuilt from the same log, answer every
// query of the set identically -- subjects and error identity -- and hold
// the same history. The CONTROL: an answer set with one edge's relation
// changed in the SQLite stream must differ, so the comparison can see one
// edge. The non-vacuity check names which queries must answer something.
func TestTwoImplementationsAgree(t *testing.T) {
	w := newWorld(t)
	mem := index.New()
	if err := mem.Rebuild(readAll(w.s)); err != nil {
		t.Fatal(err)
	}
	sq := openMem(t)
	if err := sq.Rebuild(readAll(w.s)); err != nil {
		t.Fatal(err)
	}
	if mem.At() != sq.At() || sq.At() != w.s.Head() {
		t.Fatalf("At: memory %d sqlite %d head %d", mem.At(), sq.At(), w.s.Head())
	}
	qs := w.queries(t)
	a, b := answers(mem, qs), answers(sq, qs)
	for i := range qs {
		if !reflect.DeepEqual(a[i], b[i]) {
			t.Errorf("query %d (%s): memory %+v, sqlite %+v", i, query.String(qs[i]), a[i], b[i])
		}
	}
	// the set is not vacuous: these must answer something, these must refuse
	// cites=b1 and supersedes=b1 answer nothing on purpose: their only subject,
	// b2, is superseded by b3 and so not current (§5.4)
	mustAnswer := map[int]bool{0: true, 2: true, 3: true, 7: true, 8: true, 9: true, 11: true, 13: true, 14: true}
	mustRefuse := map[int]bool{15: true}
	for i := len(qs) - 4; i < len(qs); i++ { // the four hand-built refusals
		mustRefuse[i] = true
	}
	for i := range qs {
		if mustAnswer[i] && len(a[i].Subjects) == 0 {
			t.Errorf("query %d (%s) answered nothing; the comparison is vacuous there", i, query.String(qs[i]))
		}
		if mustRefuse[i] && a[i].Err == "" {
			t.Errorf("query %d (%s) did not refuse", i, query.String(qs[i]))
		}
	}
	// history agrees, retracted-by included
	for seq := uint64(1); seq <= w.s.Head(); seq++ {
		m, merr := mem.Edge(seq)
		s, serr := sq.Edge(seq)
		if (merr == nil) != (serr == nil) || m.Edge.Rel != s.Edge.Rel || m.Edge.S != s.Edge.S || m.Edge.O != s.Edge.O || m.Hash != s.Hash || m.RetractedBy != s.RetractedBy {
			t.Errorf("history at %d: memory %+v %v, sqlite %+v %v", seq, m, merr, s, serr)
		}
	}
	if _, err := sq.Edge(9999); !errors.Is(err, contract.ErrNoEdge) {
		t.Errorf("absent seq: %v", err)
	}

	// CONTROL: the same comparison must see one changed edge
	lossy := openMem(t)
	err := lossy.Rebuild(func(fn func(log.Verified) error) error {
		return w.s.ReadLog(1, func(v log.Verified) error {
			if v.Edge.Rel == "cites" && v.Edge.S == w.b2 {
				v.Edge.Rel = "test.dropped"
			}
			return fn(v)
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(a, answers(lossy, qs)) {
		t.Fatal("control: a SQLite index missing one cites edge answered identically to memory")
	}
}

// TestRebuildComparesAndPoisons is §10.3 and D-31 on the SQLite index: an
// index built edge by edge equals one rebuilt from seq 1; a failed rebuild
// poisons every call, including the history read, until a rebuild succeeds;
// a reopened file resumes at its At().
func TestRebuildComparesAndPoisons(t *testing.T) {
	w := newWorld(t)
	path := filepath.Join(t.TempDir(), "index.sqlite")
	live, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.s.ReadLog(1, live.Apply); err != nil {
		t.Fatal(err)
	}
	qs := w.queries(t)
	liveAnswers := answers(live, qs)
	live.Close()
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.At() != w.s.Head() {
		t.Fatalf("reopened At() %d, want %d", reopened.At(), w.s.Head())
	}
	if !reflect.DeepEqual(answers(reopened, qs), liveAnswers) {
		t.Fatal("a reopened index answers differently from the one that was closed")
	}
	rebuilt := openMem(t)
	if err := rebuilt.Rebuild(readAll(w.s)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(answers(rebuilt, qs), liveAnswers) {
		t.Fatal("rebuilt answers differ from the live index")
	}
	// a gap is refused and At() stays
	if err := rebuilt.Apply(log.Verified{Edge: canon.Edge{Seq: w.s.Head() + 2, Rel: "cites", S: w.b1, O: w.b2}}); !errors.Is(err, contract.ErrGap) {
		t.Fatalf("gap: %v", err)
	}
	// poison
	boom := errors.New("the disk went away")
	err = rebuilt.Rebuild(func(fn func(log.Verified) error) error {
		return w.s.ReadLog(1, func(v log.Verified) error {
			if v.Edge.Seq == 4 {
				return boom
			}
			return fn(v)
		})
	})
	if !errors.Is(err, boom) {
		t.Fatalf("rebuild did not report the read's error: %v", err)
	}
	if _, err := rebuilt.Query(qs[0]); !errors.Is(err, contract.ErrPoisoned) {
		t.Fatalf("Query after a failed rebuild: %v", err)
	}
	if _, err := rebuilt.QueryAt(qs[0], 0); !errors.Is(err, contract.ErrPoisoned) {
		t.Fatalf("QueryAt after a failed rebuild: %v", err)
	}
	if _, err := rebuilt.Edge(1); !errors.Is(err, contract.ErrPoisoned) {
		t.Fatalf("Edge after a failed rebuild: %v", err)
	}
	if err := rebuilt.Apply(log.Verified{Edge: canon.Edge{Seq: 1}}); !errors.Is(err, contract.ErrPoisoned) {
		t.Fatalf("Apply after a failed rebuild: %v", err)
	}
	if err := rebuilt.Rebuild(readAll(w.s)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(answers(rebuilt, qs), liveAnswers) {
		t.Fatal("after a successful rebuild the answers did not return")
	}
	// stale
	if _, err := rebuilt.QueryAt(qs[0], w.s.Head()+1); !errors.Is(err, contract.ErrStale) {
		t.Fatalf("stale: %v", err)
	}
}

// TestTenThousandEdgeRebuild is M2's acceptance figure: a rebuild of a
// 10,000-edge log under 10 s on the reference machine, recorded in the
// roadmap. It applies a synthetic verified stream (no store: the store's
// signing and sealing are not what is measured) and compares the memory
// index's answers to the SQLite index's on the same queries. Skipped under
// -short because it costs seconds.
func TestTenThousandEdgeRebuild(t *testing.T) {
	if testing.Short() {
		t.Skip("costs seconds; run without -short")
	}
	const n = 10_000
	stream := func(fn func(log.Verified) error) error {
		for i := uint64(1); i <= n; i++ {
			var e canon.Edge
			switch i % 5 {
			case 0:
				e = canon.Edge{Seq: i, Rel: "is-about", S: fmt.Sprintf("sha256:%064x", i), O: fmt.Sprintf("c:%026d", (i/5)%50)}
			case 1:
				e = canon.Edge{Seq: i, Rel: "cites", S: fmt.Sprintf("sha256:%064x", i), O: fmt.Sprintf("sha256:%064x", i-1)}
			case 2:
				if i%20 == 2 { // one is-about subject in four is superseded; the rest stay current
					e = canon.Edge{Seq: i, Rel: "supersedes", S: fmt.Sprintf("sha256:%064x", i), O: fmt.Sprintf("sha256:%064x", i-2)}
				} else {
					e = canon.Edge{Seq: i, Rel: "cites", S: fmt.Sprintf("sha256:%064x", i-2), O: fmt.Sprintf("sha256:%064x", i-1)}
				}
			case 3:
				e = canon.Edge{Seq: i, Rel: "names", S: fmt.Sprintf("c:%026d", (i/5)%50), O: canon.Literal(fmt.Sprintf("name%d", (i/5)%50))}
			default:
				if i < 5 {
					e = canon.Edge{Seq: i, Rel: "is-about", S: fmt.Sprintf("sha256:%064x", i), O: fmt.Sprintf("c:%026d", (i/5)%50)}
				} else {
					e = canon.Edge{Seq: i, Rel: "retracts", S: fmt.Sprintf("edge:%d", i-3), O: fmt.Sprintf("edge:%d", i-3)} // withdraws the cites edge three back, never an is-about
				}
			}
			if err := fn(log.Verified{Edge: e, Hash: fmt.Sprintf("%064x", i)}); err != nil {
				return err
			}
		}
		return nil
	}
	sq := openMem(t)
	start := time.Now()
	if err := sq.Rebuild(stream); err != nil {
		t.Fatal(err)
	}
	took := time.Since(start)
	t.Logf("sqlite rebuild of %d edges: %s", n, took)
	if took > 10*time.Second {
		t.Fatalf("rebuild took %s, over the 10 s acceptance", took)
	}
	mem := index.New()
	if err := mem.Rebuild(stream); err != nil {
		t.Fatal(err)
	}
	var qs []contract.Query
	for k := 0; k < 50; k++ {
		c := fmt.Sprintf("c:%026d", k)
		qs = append(qs,
			contract.Query{Terms: []contract.Term{{Rel: "is-about", Object: c}}},
			contract.Query{Terms: []contract.Term{{Rel: "is-about", Object: c}}, Traversal: []contract.Traversal{{Rel: "cites", Depth: 3}}},
			contract.Query{Terms: []contract.Term{{Rel: contract.NameRel, Object: fmt.Sprintf("name%d", k)}}},
		)
	}
	a, b := answers(mem, qs), answers(sq, qs)
	if !reflect.DeepEqual(a, b) {
		for i := range qs {
			if !reflect.DeepEqual(a[i], b[i]) {
				t.Fatalf("query %d: memory %+v sqlite %+v", i, a[i], b[i])
			}
		}
	}
	nonEmpty := 0
	for _, x := range a {
		if len(x.Subjects) > 0 {
			nonEmpty++
		}
	}
	if nonEmpty < 100 {
		t.Fatalf("only %d of %d queries answered anything", nonEmpty, len(qs))
	}
}

// TestTamperedFileOpensPoisoned is quill's finding at coop main/1074: a file
// whose recorded At() disagrees with its rows is a state the memory index
// cannot hold, and an At() ABOVE the rows defeats QueryAt, which refuses only
// a position that is too low. Three tamperings -- the position inflated, the
// position deflated, a row missing inside the range -- each open poisoned and
// answer nothing until a Rebuild; the control is the untouched file, which
// opens at its At() and answers.
func TestTamperedFileOpensPoisoned(t *testing.T) {
	w := newWorld(t)
	q := contract.Query{Terms: []contract.Term{{Rel: "is-about", Object: w.alpha}}}
	build := func() string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "index.sqlite")
		x, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := x.Rebuild(readAll(w.s)); err != nil {
			t.Fatal(err)
		}
		x.Close()
		return path
	}
	tamper := func(path, stmt string) {
		t.Helper()
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
		db.Close()
	}
	head := w.s.Head()
	cases := map[string]string{
		"position inflated": fmt.Sprintf(`UPDATE meta SET v = %d WHERE k = 'at'`, head+5),
		"position deflated": `UPDATE meta SET v = 1 WHERE k = 'at'`,
		"row missing":       `DELETE FROM edges WHERE seq = 7`,
	}
	for name, stmt := range cases {
		path := build()
		tamper(path, stmt)
		x, err := Open(path)
		if err != nil {
			t.Fatalf("%s: Open refused outright: %v (it should open poisoned)", name, err)
		}
		if _, err := x.Query(q); !errors.Is(err, contract.ErrPoisoned) {
			t.Errorf("%s: Query %v, want ErrPoisoned", name, err)
		}
		if _, err := x.QueryAt(q, head); !errors.Is(err, contract.ErrPoisoned) {
			t.Errorf("%s: QueryAt %v, want ErrPoisoned", name, err)
		}
		if _, err := x.Edge(1); !errors.Is(err, contract.ErrPoisoned) {
			t.Errorf("%s: Edge %v, want ErrPoisoned", name, err)
		}
		if _, err := x.Current(w.b1); !errors.Is(err, contract.ErrPoisoned) {
			t.Errorf("%s: Current %v, want ErrPoisoned (every reader refuses, D-34)", name, err)
		}
		if err := x.Rebuild(readAll(w.s)); err != nil {
			t.Fatalf("%s: rebuild after tampering: %v", name, err)
		}
		if got, err := x.Query(q); err != nil || len(got) == 0 {
			t.Errorf("%s: after the rebuild: %v %v", name, got, err)
		}
		x.Close()
	}
	// control
	path := build()
	x, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	if x.At() != head {
		t.Fatalf("control: At() %d, want %d", x.At(), head)
	}
	if got, err := x.QueryAt(q, head); err != nil || len(got) == 0 {
		t.Fatalf("control: an untouched file answers: %v %v", got, err)
	}
}

// TestSync is D-35: Sync brings the index file beside a store to the log's
// head whatever state it is in -- fresh (rebuilt), behind (the tail
// applied, no rebuild), poisoned (rebuilt), beyond the head (rebuilt) -- and
// returns an index at the head. The tail case is told from a rebuild by
// a counter on the reader: a resume reads from At()+1, a rebuild from 1.
func TestSync(t *testing.T) {
	w := newWorld(t)
	var froms []uint64
	read := func(from uint64, fn func(log.Verified) error) error {
		froms = append(froms, from)
		return w.s.ReadLog(from, fn)
	}
	head := w.s.Head()
	q := contract.Query{Terms: []contract.Term{{Rel: "is-about", Object: w.alpha}}}
	// fresh: rebuilt from 1
	x, err := Sync(w.s.Root, head, read)
	if err != nil || x.At() != head {
		t.Fatalf("fresh: %v at %d", err, x.At())
	}
	fresh, _ := x.QueryAt(q, head)
	x.Close()
	if len(froms) != 1 || froms[0] != 1 {
		t.Fatalf("fresh sync did not rebuild from 1: %v", froms)
	}
	// behind: two more edges, the tail applied from At()+1
	froms = nil
	for i := 0; i < 2; i++ {
		if _, err := w.s.Edge("is-about", w.b3, w.alpha, nil); err != nil {
			t.Fatal(err)
		}
	}
	x, err = Sync(w.s.Root, w.s.Head(), read)
	if err != nil || x.At() != w.s.Head() {
		t.Fatalf("behind: %v at %d head %d", err, x.At(), w.s.Head())
	}
	x.Close()
	if len(froms) != 1 || froms[0] != head+1 {
		t.Fatalf("a resume must read from At()+1 = %d, read %v", head+1, froms)
	}
	// beyond the head (a tampered position): rebuilt from 1
	froms = nil
	db, _ := sql.Open("sqlite", filepath.Join(w.s.Root, FileName))
	if _, err := db.Exec(`UPDATE meta SET v = 999 WHERE k = 'at'`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	x, err = Sync(w.s.Root, w.s.Head(), read)
	if err != nil || x.At() != w.s.Head() {
		t.Fatalf("tampered: %v at %d", err, x.At())
	}
	got, err := x.QueryAt(q, w.s.Head())
	x.Close()
	if err != nil || len(got) == 0 || len(got) != len(fresh) {
		t.Fatalf("after the rebuild: %v %v (fresh %v)", got, err, fresh)
	}
	if len(froms) != 1 || froms[0] != 1 {
		t.Fatalf("a poisoned file must be rebuilt from 1, read %v", froms)
	}
	// a reader that falls short of the head is refused, not trusted
	short := func(from uint64, fn func(log.Verified) error) error {
		return w.s.ReadLog(from, func(v log.Verified) error {
			if v.Edge.Seq > w.s.Head()-1 {
				return nil
			}
			return fn(v)
		})
	}
	if _, err := w.s.Edge("cites", w.b3, w.b1, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(w.s.Root, w.s.Head(), short); !errors.Is(err, contract.ErrStale) {
		t.Fatalf("a short tail: %v, want ErrStale", err)
	}
	// the converse (quill, coop main/1085): a HEAD short of the reader. The
	// index is at the log's end; asked for head 3, the over-long branch
	// rebuilds through a reader that ends at the log's end again, and the
	// first form returned that index with err nil -- At() 29, head 3, and
	// QueryAt(q, 3) unable to refuse. Now no index comes back.
	x, err = Sync(w.s.Root, w.s.Head(), read)
	if err != nil {
		t.Fatal(err)
	}
	x.Close()
	if x, err := Sync(w.s.Root, 3, read); !errors.Is(err, contract.ErrHeadDisagrees) || errors.Is(err, contract.ErrStale) || x != nil {
		t.Fatalf("a head short of the reader: %v, index %v; want ErrHeadDisagrees (not ErrStale: the index is ahead, 10.2) and no index", err, x)
	}
	// and the control: the true head still syncs after the refusal
	if x, err := Sync(w.s.Root, w.s.Head(), read); err != nil || x.At() != w.s.Head() {
		t.Fatalf("control after the refusal: %v", err)
	} else {
		x.Close()
	}
}
