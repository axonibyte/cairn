package store

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"forge.axonibyte.com/axonibyte/cairn/internal/canon"
	"forge.axonibyte.com/axonibyte/cairn/internal/log"
)

func edgesOf(t *testing.T, s *Store) []canon.Edge {
	t.Helper()
	var out []canon.Edge
	if _, err := log.Read(s.Root, s.Founder, s.Config.SegmentBytes, 1, func(v log.Verified) error { out = append(out, v.Edge); return nil }); err != nil {
		t.Fatal(err)
	}
	return out
}

// Init lays the store out (cairn.json, .keys/ at 0700 with two 0600 key
// files, log/), the founding hash is cairn.json's bytes, the first two edges
// register the founder's keys under one principal with their public keys
// in a.pub, and Init over a store refuses by name.
func TestInitLaysOutTheStoreAndRegistersTheFounder(t *testing.T) {
	root := filepath.Join(t.TempDir(), "s")
	s, err := Init(root, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	cb, _ := os.ReadFile(filepath.Join(root, "cairn.json"))
	if !strings.HasPrefix(string(cb), `{"cairn":"store","v":1,"spec":"0.1","signing":"ed25519:`) {
		t.Fatalf("cairn.json: %s", cb)
	}
	if s.Founder.Hash != canon.Hash(cb) {
		t.Fatal("the founding hash is not cairn.json's")
	}
	info, _ := os.Stat(filepath.Join(root, ".keys"))
	if info.Mode().Perm() != 0o700 {
		t.Fatalf(".keys mode %o", info.Mode().Perm())
	}
	for _, f := range []string{"ed25519.key", "mldsa65.key"} {
		fi, err := os.Stat(filepath.Join(root, ".keys", f))
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v", f, err)
		}
	}
	n, seal, err := s.Verify()
	if err != nil || n != 2 || seal != 0 {
		t.Fatalf("verify after init: %v %d %d", err, n, seal)
	}
	edges := edgesOf(t, s)
	if edges[0].Rel != "key-of" || edges[1].Rel != "key-of" || edges[0].O != edges[1].O || !strings.HasPrefix(edges[0].O, "c:") {
		t.Fatalf("first edges: %+v %+v", edges[0], edges[1])
	}
	if edges[0].S != s.Writer() || edges[0].A["pub"] != s.Config.Signing || edges[1].A["pub"] != s.Config.Sealing {
		t.Fatalf("key-of edges do not carry the founder's keys: %+v %+v", edges[0], edges[1])
	}
	if edges[0].Prev != s.Founder.Hash {
		t.Fatal("the first edge's prev is not the founding hash")
	}
	if _, err := Init(root, 0, 0, 0); !errors.Is(err, ErrExists) {
		t.Fatalf("init over a store: %v", err)
	}
}

// Edge admits the core relations and dotted instance relations and refuses
// the rest by name; s and o must be identities, with literals allowed only
// as o; a seal is not a caller's edge; a concept is minted by a names edge.
func TestEdgeAdmitsAndRefusesByName(t *testing.T) {
	s, err := Init(filepath.Join(t.TempDir(), "s"), 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	c, seq, err := s.NewConcept("cal")
	if err != nil || seq != 3 || !strings.HasPrefix(c, "c:") || len(c) != 28 {
		t.Fatalf("new concept: %v %d %s", err, seq, c)
	}
	d, _, _ := s.NewConcept("the cairn")
	if _, err := s.Edge("axonibyte.decided-by", d, c, nil); err != nil {
		t.Fatalf("instance relation: %v", err)
	}
	if _, err := s.Edge("is-about", "sha256:"+strings.Repeat("a", 64), d, map[string]any{"via": "tool"}); err != nil {
		t.Fatalf("core relation with attrs: %v", err)
	}
	cases := map[string]struct {
		rel, sub, obj string
		want          error
	}{
		"undotted instance relation": {"decidedby", d, c, ErrRelation},
		"uppercase relation":         {"Is-About", d, c, ErrRelation},
		"seal by a caller":           {"seal", "edge:1", "", ErrRelation},
		"literal as subject":         {"names", "lit:x", c, ErrIdentity},
		"bare word as object":        {"is-about", d, "cal", ErrIdentity},
		"short concept id":           {"is-about", "c:ABC", d, ErrIdentity},
		"uppercase blob hex":         {"is-about", "sha256:" + strings.Repeat("A", 64), d, ErrIdentity},
	}
	before := s.Log.LastSeq()
	for name, cs := range cases {
		if _, err := s.Edge(cs.rel, cs.sub, cs.obj, nil); !errors.Is(err, cs.want) {
			t.Fatalf("%s: got %v, want %v", name, err, cs.want)
		}
	}
	if s.Log.LastSeq() != before {
		t.Fatal("a refused edge moved the log")
	}
	// §5.2's shapes: a core relation's subject and object are of the kinds
	// the table gives, an object is required; an instance relation may be
	// unary and may take a literal object
	shapes := map[string]struct{ rel, sub, obj string }{
		"names with a concept object": {"names", d, c},
		"retracts without an object":  {"retracts", "edge:3", ""},
		"retracts of a concept":       {"retracts", "edge:3", d},
		"wrote by a concept":          {"wrote", d, "sha256:" + strings.Repeat("b", 64)},
		"is-about a blob as object":   {"is-about", d, "sha256:" + strings.Repeat("b", 64)},
		"key-of a blob":               {"key-of", s.Writer(), "sha256:" + strings.Repeat("b", 64)},
	}
	for name, cs := range shapes {
		if _, err := s.Edge(cs.rel, cs.sub, cs.obj, nil); !errors.Is(err, ErrIdentity) {
			t.Fatalf("%s: got %v, want a shape refusal", name, err)
		}
	}
	if _, err := s.Edge("retracts", "edge:3", "edge:4", nil); err != nil {
		t.Fatalf("retracts edge -> edge: %v", err)
	}
	if _, err := s.Edge("axonibyte.flagged", d, "", nil); err != nil {
		t.Fatalf("unary instance relation: %v", err)
	}
	if _, err := s.Edge("axonibyte.note", d, "lit:a literal object", nil); err != nil {
		t.Fatalf("instance relation with a literal: %v", err)
	}
}

// Put stores the blob and records the writer's wrote edge on its first
// appearance only; a second Put of the same bytes adds no edge; a large
// blob's identity is its manifest's and Get returns the bytes whole.
func TestPutRecordsWroteOnce(t *testing.T) {
	s, err := Init(filepath.Join(t.TempDir(), "s"), 1000, 300, 0)
	if err != nil {
		t.Fatal(err)
	}
	id, seq, err := s.Put(strings.NewReader("a note"))
	if err != nil || seq != 3 || id != canon.Hash([]byte("a note")) {
		t.Fatalf("put: %v %d %s", err, seq, id)
	}
	edges := edgesOf(t, s)
	last := edges[len(edges)-1]
	if last.Rel != "wrote" || last.S != s.Writer() || last.O != id {
		t.Fatalf("wrote edge: %+v", last)
	}
	if id2, seq2, err := s.Put(strings.NewReader("a note")); err != nil || id2 != id || seq2 != 0 || s.Log.LastSeq() != 3 {
		t.Fatalf("second put: %v %s %d %d", err, id2, seq2, s.Log.LastSeq())
	}
	big := bytes.Repeat([]byte("z"), 2500)
	bid, bseq, err := s.Put(bytes.NewReader(big))
	if err != nil || bseq != 4 {
		t.Fatalf("large put: %v %d", err, bseq)
	}
	if m, _ := s.Blobs.IsManifest(bid); !m {
		t.Fatal("a 2500-byte blob under a 1000 bound is a manifest")
	}
	if got, err := s.Blobs.Get(bid); err != nil || !bytes.Equal(got, big) {
		t.Fatalf("large get: %v", err)
	}
}

// Open on a second "machine" (the keys absent) reads and verifies but
// cannot write, by name; a swapped cairn.json breaks the chain at seq 1.
func TestOpenWithoutKeysReadsButCannotWrite(t *testing.T) {
	root := filepath.Join(t.TempDir(), "s")
	s, _ := Init(root, 0, 0, 0)
	s.NewConcept("x")
	os.RemoveAll(filepath.Join(root, ".keys"))
	r, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if r.Writer() != "" {
		t.Fatal("a store without keys reports a writer")
	}
	if n, _, err := r.Verify(); err != nil || n != 3 {
		t.Fatalf("verify without keys: %v %d", err, n)
	}
	if _, _, err := r.NewConcept("y"); !errors.Is(err, ErrNoKey) {
		t.Fatalf("write without keys: %v", err)
	}
	if _, _, err := r.Put(strings.NewReader("b")); !errors.Is(err, ErrNoKey) {
		t.Fatalf("put without keys: %v", err)
	}
	cb, _ := os.ReadFile(filepath.Join(root, "cairn.json"))
	os.WriteFile(filepath.Join(root, "cairn.json"), bytes.Replace(cb, []byte(`"spec":"0.1"`), []byte(`"spec":"0.2"`), 1), 0o644)
	if _, err := Open(root); err == nil || !strings.Contains(err.Error(), "at seq 1") {
		t.Fatalf("swapped configuration: %v", err)
	}
}

// ULIDs are 26 Crockford characters, distinct, and sort by time.
func TestULID(t *testing.T) {
	a, _ := NewULID()
	b, _ := NewULID()
	if len(a) != 26 || a == b || strings.ContainsAny(a, "ILOU") {
		t.Fatalf("%s %s", a, b)
	}
	for _, r := range a {
		if !strings.ContainsRune(crockford, r) {
			t.Fatalf("%q is not a Crockford digit", r)
		}
	}
	if a[:10] > b[:10] {
		t.Fatalf("time prefix not sortable: %s %s", a, b)
	}
}
