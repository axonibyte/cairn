package log

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"forge.axonibyte.com/axonibyte/cairn/internal/canon"
	"forge.axonibyte.com/axonibyte/cairn/internal/writer"
)

type fixture struct {
	root    string
	founder Founder
	signing *writer.Key
	sealing *writer.Key
	log     *Log
}

func newFixture(t *testing.T, segmentBytes int64) *fixture {
	t.Helper()
	sk, _ := writer.Generate(writer.Ed25519)
	ml, _ := writer.Generate(writer.MLDSA65)
	f := &fixture{root: t.TempDir(), signing: sk, sealing: ml}
	f.founder = Founder{Signing: sk.Public(), Sealing: ml.Public(), Hash: canon.Hash([]byte("founding"))}
	l, err := Open(f.root, f.founder, segmentBytes)
	if err != nil {
		t.Fatal(err)
	}
	f.log = l
	return f
}

// edge builds and signs the next edge by the fixture's signing key.
func (f *fixture) edge(t *testing.T, rel, s, o string) canon.Edge {
	t.Helper()
	seq, prev := f.log.Next()
	e := canon.Edge{Seq: seq, T: "2026-11-18T09:00:00.000Z", W: f.signing.ID(), Prev: prev, Rel: rel, S: s, O: o}
	if err := Sign(&e, f.signing); err != nil {
		t.Fatal(err)
	}
	return e
}

func (f *fixture) append(t *testing.T, rel, s, o string) uint64 {
	t.Helper()
	seq, err := f.log.Append(f.edge(t, rel, s, o), f.sealing)
	if err != nil {
		t.Fatal(err)
	}
	return seq
}

func readAll(t *testing.T, f *fixture) (edges []canon.Edge, lastSeal uint64) {
	t.Helper()
	lastSeal, err := Read(f.root, f.founder, f.log.SegmentBytes, 1, func(v Verified) error { edges = append(edges, v.Edge); return nil })
	if err != nil {
		t.Fatal(err)
	}
	return edges, lastSeal
}

// §4.3, §8.7: the first edge's prev is the founding hash, each edge's prev
// is the hash of the previous written form, seq is contiguous, and the lines
// in the segment are the written forms exactly.
func TestAppendChainsAndWritesCanonicalLines(t *testing.T) {
	f := newFixture(t, 0)
	e1 := f.edge(t, "names", "c:01J", "lit:cal")
	if e1.Prev != f.founder.Hash || e1.Seq != 1 {
		t.Fatalf("first edge: seq %d prev %s", e1.Seq, e1.Prev)
	}
	if _, err := f.log.Append(e1, nil); err != nil {
		t.Fatal(err)
	}
	w1, _ := canon.Canonical(e1, true)
	e2 := f.edge(t, "is-about", "sha256:"+strings.Repeat("0", 64), "c:01J")
	if e2.Prev != canon.Hash(w1) {
		t.Fatalf("second edge's prev is not the hash of the first: %s", e2.Prev)
	}
	if _, err := f.log.Append(e2, nil); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(f.root, "log", "000000.jsonl"))
	w2, _ := canon.Canonical(e2, true)
	if string(data) != string(w1)+"\n"+string(w2)+"\n" {
		t.Fatalf("segment holds something other than the written forms:\n%s", data)
	}
	edges, seal := readAll(t, f)
	if len(edges) != 2 || seal != 0 {
		t.Fatalf("read back %d edges, last seal %d", len(edges), seal)
	}
}

// §4.3 and §8.3: a wrong seq, a wrong prev, an unknown writer, a forged or
// missing signature are each refused by name, and the log does not move.
func TestAppendRefusesByName(t *testing.T) {
	f := newFixture(t, 0)
	f.append(t, "names", "c:01J", "lit:cal")
	good := f.edge(t, "names", "c:01K", "lit:quill")
	cases := map[string]struct {
		mutate func(e canon.Edge) canon.Edge
		want   error
	}{
		"seq skipped":  {func(e canon.Edge) canon.Edge { e.Seq = 3; Sign(&e, f.signing); return e }, ErrSeq},
		"seq repeated": {func(e canon.Edge) canon.Edge { e.Seq = 1; Sign(&e, f.signing); return e }, ErrSeq},
		"prev wrong":   {func(e canon.Edge) canon.Edge { e.Prev = f.founder.Hash; Sign(&e, f.signing); return e }, ErrPrev},
		"unknown writer": {func(e canon.Edge) canon.Edge {
			k, _ := writer.Generate(writer.Ed25519)
			e.W = k.ID()
			Sign(&e, k)
			return e
		}, ErrWriter},
		"forged": {func(e canon.Edge) canon.Edge {
			k, _ := writer.Generate(writer.Ed25519)
			Sign(&e, k) // the registered writer's id, another key's signature
			return e
		}, ErrSignature},
		"unsigned":              {func(e canon.Edge) canon.Edge { e.Sig = ""; return e }, ErrSignature},
		"altered after signing": {func(e canon.Edge) canon.Edge { e.O = "lit:quill2"; return e }, ErrSignature},
	}
	for name, c := range cases {
		_, err := f.log.Append(c.mutate(good), nil)
		if !errors.Is(err, c.want) {
			t.Fatalf("%s: got %v, want %v", name, err, c.want)
		}
		if f.log.LastSeq() != 1 {
			t.Fatalf("%s: the log moved to %d", name, f.log.LastSeq())
		}
	}
	if _, err := f.log.Append(good, nil); err != nil {
		t.Fatalf("the control edge must append: %v", err)
	}
}

// §8.2-8.3: a key-of edge registers a second writer, whose edges then
// verify; one that does not carry its key, or carries a key that is not
// the subject, is refused by name; before registration the writer is
// unknown.
func TestKeyOfRegistersAWriter(t *testing.T) {
	f := newFixture(t, 0)
	second, _ := writer.Generate(writer.Ed25519)
	seq, prev := f.log.Next()
	by2 := canon.Edge{Seq: seq, T: "2026-11-18T09:00:00.000Z", W: second.ID(), Prev: prev, Rel: "names", S: "c:01L", O: "lit:x"}
	Sign(&by2, second)
	if _, err := f.log.Append(by2, nil); !errors.Is(err, ErrWriter) {
		t.Fatalf("before key-of: %v", err)
	}
	// a key-of without a.pub, and one whose a.pub is another key, are refused
	bad := f.edge(t, RelKeyOf, second.ID(), "c:principal")
	if _, err := f.log.Append(bad, nil); !errors.Is(err, ErrKeyOf) {
		t.Fatalf("key-of without pub: %v", err)
	}
	other, _ := writer.Generate(writer.Ed25519)
	seq, prev = f.log.Next()
	wrong := canon.Edge{Seq: seq, T: "2026-11-18T09:00:00.000Z", W: f.signing.ID(), Prev: prev, Rel: RelKeyOf, S: second.ID(), O: "c:principal", A: map[string]any{"pub": other.Public().String()}}
	Sign(&wrong, f.signing)
	if _, err := f.log.Append(wrong, nil); !errors.Is(err, ErrKeyOf) {
		t.Fatalf("key-of with another key: %v", err)
	}
	seq, prev = f.log.Next()
	reg := canon.Edge{Seq: seq, T: "2026-11-18T09:00:00.000Z", W: f.signing.ID(), Prev: prev, Rel: RelKeyOf, S: second.ID(), O: "c:principal", A: map[string]any{"pub": second.Public().String()}}
	Sign(&reg, f.signing)
	if _, err := f.log.Append(reg, nil); err != nil {
		t.Fatal(err)
	}
	if !f.log.Registered(second.ID()) {
		t.Fatal("second writer not registered after key-of")
	}
	seq, prev = f.log.Next()
	by2 = canon.Edge{Seq: seq, T: "2026-11-18T09:00:00.000Z", W: second.ID(), Prev: prev, Rel: "names", S: "c:01L", O: "lit:x"}
	Sign(&by2, second)
	if _, err := f.log.Append(by2, nil); err != nil {
		t.Fatalf("after key-of: %v", err)
	}
	// and a fresh reader learns the registration from the log alone (§8.4)
	edges, _ := readAll(t, f)
	if len(edges) != 2 || edges[1].W != second.ID() {
		t.Fatalf("read back: %d edges, last by %s", len(edges), edges[len(edges)-1].W)
	}
}

// §8.7: at the segment bound a seal by the mldsa65 key closes the segment,
// its subject the last edge and its prev that edge's hash; the next edge
// opens the next segment; Read reports the last seal; a seal on demand
// works the same; a seal by the wrong suite is refused.
func TestSealAtTheRollAndOnDemand(t *testing.T) {
	f := newFixture(t, 600) // small segments: a few edges each
	var seqs []uint64
	for i := 0; i < 6; i++ {
		seqs = append(seqs, f.append(t, "names", "c:"+strconv.Itoa(i), "lit:n"))
	}
	segs, _ := filepath.Glob(filepath.Join(f.root, "log", "*.jsonl"))
	if len(segs) < 2 {
		t.Fatalf("expected a roll, segments: %v", segs)
	}
	edges, lastSeal := readAll(t, f)
	if lastSeal == 0 {
		t.Fatal("no seal read back")
	}
	var seals int
	for i, e := range edges {
		if e.Rel == RelSeal {
			seals++
			if e.W != f.sealing.ID() || e.S != "edge:"+strconv.FormatUint(edges[i-1].Seq, 10) {
				t.Fatalf("seal %d: w %s s %s", e.Seq, e.W, e.S)
			}
			if !strings.HasPrefix(e.Sig, "mldsa65:") {
				t.Fatalf("seal signed by %s", e.Sig[:8])
			}
		}
	}
	if seals == 0 {
		t.Fatal("no seal edge in the log")
	}
	// the first segment ends with a seal
	first, _ := os.ReadFile(segs[0])
	lines := strings.Split(strings.TrimSpace(string(first)), "\n")
	if !strings.Contains(lines[len(lines)-1], `"rel":"seal"`) {
		t.Fatalf("segment 0 does not end with a seal: %s", lines[len(lines)-1])
	}
	// on demand
	before := f.log.LastSeal()
	s, err := f.log.Seal(f.sealing)
	if err != nil || s != f.log.LastSeq() || f.log.LastSeal() <= before {
		t.Fatalf("seal on demand: %v %d %d", err, s, f.log.LastSeal())
	}
	if _, err := f.log.Seal(f.signing); !errors.Is(err, ErrSignature) {
		t.Fatalf("seal by the ed25519 key: %v", err)
	}
	// and an edge that pretends to be a seal, signed by the per-edge key
	fake := f.edge(t, RelSeal, "edge:"+strconv.FormatUint(f.log.LastSeq(), 10), "")
	if _, err := f.log.Append(fake, nil); !errors.Is(err, ErrSignature) {
		t.Fatalf("fake seal: %v", err)
	}
	_ = seqs
}

// P-7, SPEC §8.7: flip one byte of a sealed edge and Read refuses, naming
// the first bad seq; the chain breaks at the altered edge and at the one
// after it; a removed line breaks at the gap; a tampered seal is refused.
func TestTamperingIsNamedAtTheFirstBadSeq(t *testing.T) {
	f := newFixture(t, 600)
	for i := 0; i < 6; i++ {
		f.append(t, "names", "c:"+strconv.Itoa(i), "lit:n")
	}
	seg := filepath.Join(f.root, "log", "000000.jsonl")
	orig, _ := os.ReadFile(seg)
	lines := strings.Split(strings.TrimSuffix(string(orig), "\n"), "\n")
	restore := func() { os.WriteFile(seg, orig, 0o644) }

	// flip one byte inside edge 2's object string
	alt := strings.Replace(lines[1], `"o":"lit:n"`, `"o":"lit:m"`, 1)
	os.WriteFile(seg, []byte(strings.Join(append(append([]string{lines[0]}, alt), lines[2:]...), "\n")+"\n"), 0o644)
	_, err := Read(f.root, f.founder, f.log.SegmentBytes, 1, nil)
	if !errors.Is(err, ErrChain) || !strings.Contains(err.Error(), "at seq 2") {
		t.Fatalf("altered edge: %v", err)
	}
	restore()

	// remove edge 2: the gap is named at seq 2 (seq 3 arrives where 2 is due)
	os.WriteFile(seg, []byte(strings.Join(append([]string{lines[0]}, lines[2:]...), "\n")+"\n"), 0o644)
	_, err = Read(f.root, f.founder, f.log.SegmentBytes, 1, nil)
	if !errors.Is(err, ErrChain) || !strings.Contains(err.Error(), "at seq 3") {
		t.Fatalf("removed edge: %v", err)
	}
	restore()

	// tamper with the seal's signature (the last line of segment 0)
	last := lines[len(lines)-1]
	i := strings.LastIndex(last, `"sig":"mldsa65:`)
	if i < 0 {
		t.Fatalf("segment 0 does not end with a seal: %s", last)
	}
	b := []byte(last)
	j := i + len(`"sig":"mldsa65:`)
	if b[j] == '0' {
		b[j] = '1'
	} else {
		b[j] = '0'
	}
	os.WriteFile(seg, []byte(strings.Join(append(lines[:len(lines)-1], string(b)), "\n")+"\n"), 0o644)
	_, err = Read(f.root, f.founder, f.log.SegmentBytes, 1, nil)
	if !errors.Is(err, ErrChain) || !strings.Contains(err.Error(), "at seq "+strconv.Itoa(len(lines))) {
		t.Fatalf("tampered seal: %v", err)
	}
	restore()

	// a line that parses to the same edge but is not its own canonical form
	// (a space after a colon): the signature and the chain would both still
	// verify, so the line rule is the only thing refusing it, by name
	spaced := strings.Replace(lines[0], `"rel":`, `"rel": `, 1)
	os.WriteFile(seg, []byte(strings.Join(append([]string{spaced}, lines[1:]...), "\n")+"\n"), 0o644)
	_, err = Read(f.root, f.founder, f.log.SegmentBytes, 1, nil)
	if !errors.Is(err, ErrChain) || !strings.Contains(err.Error(), "not its own canonical form") || !strings.Contains(err.Error(), "at seq 1") {
		t.Fatalf("non-canonical line: %v", err)
	}
	restore()
	if _, err := Read(f.root, f.founder, f.log.SegmentBytes, 1, nil); err != nil {
		t.Fatalf("control after restore: %v", err)
	}
}

// Reopening recovers the position, the registry and the segment, and the
// next append continues the chain; a log whose segments have a gap is
// refused by name.
func TestReopenContinuesTheChain(t *testing.T) {
	f := newFixture(t, 600)
	for i := 0; i < 5; i++ {
		f.append(t, "names", "c:"+strconv.Itoa(i), "lit:n")
	}
	lastSeq, lastSeal := f.log.LastSeq(), f.log.LastSeal()
	l2, err := Open(f.root, f.founder, 600)
	if err != nil {
		t.Fatal(err)
	}
	if l2.LastSeq() != lastSeq || l2.LastSeal() != lastSeal {
		t.Fatalf("reopened at %d/%d, want %d/%d", l2.LastSeq(), l2.LastSeal(), lastSeq, lastSeal)
	}
	f.log = l2
	f.append(t, "names", "c:after", "lit:reopen")
	if _, err := Read(f.root, f.founder, f.log.SegmentBytes, 1, nil); err != nil {
		t.Fatalf("chain after reopen: %v", err)
	}
	os.Rename(filepath.Join(f.root, "log", "000001.jsonl"), filepath.Join(f.root, "log", "000002.jsonl"))
	if _, err := Open(f.root, f.founder, 600); err == nil || !strings.Contains(err.Error(), "not contiguous") {
		t.Fatalf("segment gap: %v", err)
	}
}

// M1 acceptance: ten thousand edges across rolled segments read back
// verified, in order, with the last seal reported.
func TestTenThousandEdges(t *testing.T) {
	if testing.Short() {
		t.Skip("ten thousand edges skipped under -short")
	}
	f := newFixture(t, 256<<10)
	for i := 0; i < 10000; i++ {
		f.append(t, "names", "c:"+strconv.Itoa(i), "lit:n")
	}
	var n int
	var prevSeq uint64
	lastSeal, err := Read(f.root, f.founder, f.log.SegmentBytes, 1, func(v Verified) error {
		if v.Edge.Seq != prevSeq+1 {
			return errors.New("out of order")
		}
		prevSeq = v.Edge.Seq
		n++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n < 10000 || lastSeal == 0 {
		t.Fatalf("read %d edges, last seal %d", n, lastSeal)
	}
}

// §8.7 (quill, coop main/1001 finding 8): a roll owes a seal and the
// obligation is never held in a return value. An append that would reach
// the bound with no sealing key is refused before anything is written; a
// store on disk whose rolled segment does not end in a seal is refused by
// name on Open and on Read, so no reader can be told the post-quantum bound
// is 0 without a complaint.
func TestAnOwedSealIsNeverLeftInAReturnValue(t *testing.T) {
	f := newFixture(t, 600)
	// drive to just under the roll with the sealer, then try the rolling edge without it
	for {
		e := f.edge(t, "names", "c:x", "lit:n")
		w, _ := canon.Canonical(e, true)
		if f.log.segmentSize+int64(len(w))+1 >= f.log.SegmentBytes {
			before := f.log.LastSeq()
			if _, err := f.log.Append(e, nil); !errors.Is(err, ErrSealOwed) || !strings.Contains(err.Error(), "nothing written") {
				t.Fatalf("rolling append without a sealer: %v", err)
			}
			if f.log.LastSeq() != before {
				t.Fatal("the refused edge was written")
			}
			if _, err := f.log.Append(e, f.signing); !errors.Is(err, ErrSealOwed) {
				t.Fatalf("rolling append with the wrong suite as sealer: %v", err)
			}
			if _, err := f.log.Append(e, f.sealing); err != nil {
				t.Fatalf("the same edge with the sealer: %v", err)
			}
			break
		}
		if _, err := f.log.Append(e, f.sealing); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Read(f.root, f.founder, 600, 1, nil); err != nil {
		t.Fatalf("control: %v", err)
	}
	// forge the on-disk state a crash could leave: segment 0 at its bound
	// with its seal line removed; Open and Read both refuse by name
	seg := filepath.Join(f.root, "log", "000000.jsonl")
	orig, _ := os.ReadFile(seg)
	lines := strings.Split(strings.TrimSuffix(string(orig), "\n"), "\n")
	if !strings.Contains(lines[len(lines)-1], `"rel":"seal"`) {
		t.Fatalf("segment 0 should end with a seal: %s", lines[len(lines)-1])
	}
	// the rule is about the bound: the same bytes with the seal line removed,
	// read under a bound the segment exceeds, are a rolled segment that does
	// not end in a seal, and both Open and Read refuse them by name
	unsealed := strings.Join(lines[:len(lines)-1], "\n") + "\n"
	os.WriteFile(seg, []byte(unsealed), 0o644)
	os.Remove(filepath.Join(f.root, "log", "000001.jsonl"))
	if _, err := Open(f.root, f.founder, 100); !errors.Is(err, ErrSealOwed) {
		t.Fatalf("Open over a rolled unsealed segment: %v", err)
	}
	if _, err := Read(f.root, f.founder, 100, 1, nil); !errors.Is(err, ErrSealOwed) {
		t.Fatalf("Read over a rolled unsealed segment: %v", err)
	}
	// under a bound the segment has not reached, the same bytes are a fine,
	// unrolled, unsealed segment: the rule is about the bound, not the seal
	if l, err := Open(f.root, f.founder, 1<<20); err != nil || l.LastSeal() != 0 {
		t.Fatalf("Open under a large bound: %v", err)
	}
}
