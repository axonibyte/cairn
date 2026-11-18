// Package store is a cairn store on the file backend: a root directory with
// cairn.json (the configuration, whose bytes as first written are the
// founding hash), blobs/ (SPEC §3), log/ (§4) and .keys/ (the writer's
// seeds, mode 0700, never served and never exported, D-28). It composes
// blob, log and writer into the operations the CLI exposes: Init, Open,
// Put, Edge, Verify.
package store

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"forge.axonibyte.com/axonibyte/cairn/internal/blob"
	"forge.axonibyte.com/axonibyte/cairn/internal/canon"
	"forge.axonibyte.com/axonibyte/cairn/internal/log"
	"forge.axonibyte.com/axonibyte/cairn/internal/writer"
)

// Core relation names (SPEC §5.2). The engine's; an instance cannot turn
// them off, and an instance relation must carry a dot.
var CoreRelations = []string{"names", "is-about", "cites", "contradicts", "supersedes", "retracts", "decided-by", "wrote", "in-scope", "key-of", "seal"}

// Config is cairn.json. Keys are emitted in this order and the file is
// written once, at Init; its bytes are the founding hash.
type Config struct {
	Cairn        string `json:"cairn"`
	V            int    `json:"v"`
	Spec         string `json:"spec"`
	Signing      string `json:"signing"`       // the founder's per-edge public key, <suite>:<hex>
	Sealing      string `json:"sealing"`       // the founder's mldsa65 public key
	BlobBound    int64  `json:"blob_bound"`    // §3.3
	ChunkSize    int64  `json:"chunk_size"`    // §3.3
	SegmentBytes int64  `json:"segment_bytes"` // §4.1
}

// Store is an open store.
type Store struct {
	Root    string
	Config  Config
	Founder log.Founder
	Blobs   blob.Store
	Log     *log.Log
	signing *writer.Key // nil when this machine holds no key for the store
	sealing *writer.Key
}

var (
	// ErrExists is Init over a root that already holds a store.
	ErrExists = errors.New("store: a store is already here")
	// ErrNoStore is Open over a root with no cairn.json.
	ErrNoStore = errors.New("store: no cairn.json here")
	// ErrNoKey is a write by a machine that holds no key for the store.
	ErrNoKey = errors.New("store: this machine holds no writer key for the store")
	// ErrRelation is a relation name neither core nor dotted (§5.2).
	ErrRelation = errors.New("store: relation is neither core nor an instance relation with a dot")
	// ErrIdentity is an s or o that is not a concept id, blob identity, edge reference or literal (§4.2).
	ErrIdentity = errors.New("store: not a concept id, blob identity, edge reference or literal")
)

type keyFile struct {
	Cairn string `json:"cairn"`
	Suite string `json:"suite"`
	Seed  string `json:"seed"`
}

// Init creates a store at root: two fresh keys written to .keys/ (0700,
// files 0600), cairn.json with their public halves and the instance
// settings (zero means the default), then the first edge: the founder's
// key-of to a principal concept, so the log opens with a registered writer
// whose key is readable from the log alone (§8.4). Returns the open store.
func Init(root string, blobBound, chunkSize, segmentBytes int64) (*Store, error) {
	if _, err := os.Stat(filepath.Join(root, "cairn.json")); err == nil {
		return nil, fmt.Errorf("%w: %s", ErrExists, root)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	signing, err := writer.Generate(writer.Ed25519)
	if err != nil {
		return nil, err
	}
	sealing, err := writer.Generate(writer.MLDSA65)
	if err != nil {
		return nil, err
	}
	if err := writeKeys(root, signing, sealing); err != nil {
		return nil, err
	}
	if blobBound <= 0 {
		blobBound = blob.DefaultBound
	}
	if chunkSize <= 0 {
		chunkSize = blob.DefaultChunkSize
	}
	if segmentBytes <= 0 {
		segmentBytes = log.DefaultSegmentBytes
	}
	cfg := Config{Cairn: "store", V: 1, Spec: "0.1", Signing: signing.Public().String(), Sealing: sealing.Public().String(), BlobBound: blobBound, ChunkSize: chunkSize, SegmentBytes: segmentBytes}
	cb, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	cb = append(cb, '\n')
	if err := writeAtomic(filepath.Join(root, "cairn.json"), cb, 0o644); err != nil {
		return nil, err
	}
	s, err := Open(root)
	if err != nil {
		return nil, err
	}
	// the founder's principal and its two keys, as the first edges
	principal, err := s.newConceptID()
	if err != nil {
		return nil, err
	}
	for _, k := range []*writer.Key{signing, sealing} {
		if _, err := s.Edge("key-of", k.ID(), principal, map[string]any{"pub": k.Public().String()}); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func writeKeys(root string, keys ...*writer.Key) error {
	dir := filepath.Join(root, ".keys")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for _, k := range keys {
		kf := keyFile{Cairn: "key", Suite: k.Suite, Seed: hex.EncodeToString(k.Seed())}
		b, err := json.Marshal(kf)
		if err != nil {
			return err
		}
		if err := writeAtomic(filepath.Join(dir, k.Suite+".key"), append(b, '\n'), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	return os.Rename(name, path)
}

// Open opens the store at root: cairn.json read and hashed for the
// founding hash, the keys loaded if this machine holds them, the log
// replayed and verified.
func Open(root string) (*Store, error) {
	cb, err := os.ReadFile(filepath.Join(root, "cairn.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNoStore, root)
	}
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(cb, &cfg); err != nil || cfg.Cairn != "store" || cfg.V != 1 {
		return nil, fmt.Errorf("store: cairn.json is not a store configuration of v1")
	}
	signingPub, err := writer.ParsePublic(cfg.Signing)
	if err != nil {
		return nil, fmt.Errorf("store: cairn.json signing key: %w", err)
	}
	sealingPub, err := writer.ParsePublic(cfg.Sealing)
	if err != nil || sealingPub.Suite != writer.MLDSA65 {
		return nil, fmt.Errorf("store: cairn.json sealing key must be mldsa65: %v", err)
	}
	s := &Store{Root: root, Config: cfg,
		Founder: log.Founder{Signing: signingPub, Sealing: sealingPub, Hash: canon.Hash(cb)},
		Blobs:   blob.Store{Root: root, Bound: cfg.BlobBound, ChunkSize: cfg.ChunkSize}}
	for _, suite := range []string{writer.Ed25519, writer.MLDSA65} {
		kb, err := os.ReadFile(filepath.Join(root, ".keys", suite+".key"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var kf keyFile
		if err := json.Unmarshal(kb, &kf); err != nil || kf.Cairn != "key" || kf.Suite != suite {
			return nil, fmt.Errorf("store: .keys/%s.key is not a key file of suite %s", suite, suite)
		}
		seed, err := hex.DecodeString(kf.Seed)
		if err != nil {
			return nil, fmt.Errorf("store: .keys/%s.key seed: %w", suite, err)
		}
		k, err := writer.FromSeed(suite, seed)
		if err != nil {
			return nil, err
		}
		if suite == writer.Ed25519 {
			s.signing = k
		} else {
			s.sealing = k
		}
	}
	l, err := log.Open(root, s.Founder, cfg.SegmentBytes)
	if err != nil {
		return nil, err
	}
	s.Log = l
	return s, nil
}

// Writer is the id of this machine's signing key, or "" when it holds none.
func (s *Store) Writer() string {
	if s.signing == nil {
		return ""
	}
	return s.signing.ID()
}

var (
	conceptRe = regexp.MustCompile(`^c:[0-9A-HJKMNP-TV-Z]{26}$`)
	keyRe     = regexp.MustCompile(`^k:(ed25519|mldsa65):[0-9a-f]{64}$`)
	blobRe    = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	edgeRe    = regexp.MustCompile(`^edge:[1-9][0-9]*$`)
	relRe     = regexp.MustCompile(`^[a-z][a-z0-9-]*(\.[a-z][a-z0-9-]*)+$`)
)

// kindOf names what an identity is: concept, writer, blob, edge, literal,
// or "" for none of them (§4.2).
func kindOf(v string) string {
	switch {
	case conceptRe.MatchString(v):
		return "concept"
	case keyRe.MatchString(v):
		return "writer"
	case blobRe.MatchString(v):
		return "blob"
	case edgeRe.MatchString(v):
		return "edge"
	case strings.HasPrefix(v, "lit:"):
		return "literal"
	}
	return ""
}

// coreShape is §5.2's table: the kinds a core relation's subject and object
// may be. An instance relation (dotted) takes any identity as subject and
// any identity or literal, or nothing, as object.
var coreShape = map[string][2][]string{
	"names":       {{"concept"}, {"literal"}},
	"is-about":    {{"blob", "concept"}, {"concept"}},
	"cites":       {{"blob", "concept"}, {"blob", "concept"}},
	"contradicts": {{"blob", "concept"}, {"blob", "concept"}},
	"supersedes":  {{"blob", "concept"}, {"blob", "concept"}},
	"retracts":    {{"edge"}, {"edge"}},
	"decided-by":  {{"blob", "concept"}, {"concept"}},
	"wrote":       {{"writer"}, {"blob"}},
	"in-scope":    {{"blob", "concept"}, {"concept"}},
	"key-of":      {{"writer"}, {"concept"}},
}

func within(kind string, kinds []string) bool {
	for _, k := range kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// checkShape admits an edge's subject and object: identities of the kinds
// §5.2 gives for a core relation; any identity (object optional, literal
// allowed) for an instance relation.
func checkShape(rel, subject, object string) error {
	sk, ok := kindOf(subject), kindOf(object)
	if sk == "" || sk == "literal" {
		return fmt.Errorf("subject: %w: %q", ErrIdentity, subject)
	}
	if object != "" && ok == "" {
		return fmt.Errorf("object: %w: %q", ErrIdentity, object)
	}
	shape, core := coreShape[rel]
	if !core {
		return nil
	}
	if !within(sk, shape[0]) {
		return fmt.Errorf("subject: %w: %s takes %s, got a %s", ErrIdentity, rel, strings.Join(shape[0], " or "), sk)
	}
	if object == "" {
		return fmt.Errorf("object: %w: %s takes %s, got none", ErrIdentity, rel, strings.Join(shape[1], " or "))
	}
	if !within(ok, shape[1]) {
		return fmt.Errorf("object: %w: %s takes %s, got a %s", ErrIdentity, rel, strings.Join(shape[1], " or "), ok)
	}
	return nil
}

func checkRelation(rel string) error {
	for _, r := range CoreRelations {
		if rel == r {
			return nil
		}
	}
	if relRe.MatchString(rel) {
		return nil
	}
	return fmt.Errorf("%w: %q", ErrRelation, rel)
}

// Edge appends a signed edge by this machine's writer: rel, s, o (empty for
// a unary relation) and attributes. The relation must be core or dotted;
// s and o must be identities (o may be a literal). A seal is not an edge a
// caller makes. Returns the seq.
func (s *Store) Edge(rel, subject, object string, attrs map[string]any) (uint64, error) {
	if s.signing == nil || s.sealing == nil {
		return 0, ErrNoKey
	}
	if rel == "seal" {
		return 0, fmt.Errorf("%w: a seal is made by the log, not by a caller", ErrRelation)
	}
	if err := checkRelation(rel); err != nil {
		return 0, err
	}
	if err := checkShape(rel, subject, object); err != nil {
		return 0, err
	}
	seq, prev := s.Log.Next()
	e := canon.Edge{Seq: seq, T: log.Now(), W: s.signing.ID(), Prev: prev, Rel: rel, S: subject, O: object, A: attrs}
	if err := log.Sign(&e, s.signing); err != nil {
		return 0, err
	}
	return s.Log.Append(e, s.sealing)
}

// Put stores the bytes as a blob and records the writer's `wrote` edge on
// the blob's first appearance (§5.2). Returns the identity and the seq of
// the wrote edge (0 when the blob was already here and no edge was added).
func (s *Store) Put(r io.Reader) (identity string, seq uint64, err error) {
	if s.signing == nil {
		return "", 0, ErrNoKey
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return "", 0, err
	}
	id, created, err := s.Blobs.PutReport(bytes.NewReader(data))
	if err != nil {
		return "", 0, err
	}
	already := !created
	if already {
		return id, 0, nil
	}
	seq, err = s.Edge("wrote", s.signing.ID(), id, nil)
	if err != nil {
		return id, 0, err
	}
	return id, seq, nil
}

// Verify reads the whole log with full verification and returns the edge
// count and the seq of the last seal.
func (s *Store) Verify() (edges uint64, lastSeal uint64, err error) {
	lastSeal, err = log.Read(s.Root, s.Founder, s.Config.SegmentBytes, 1, func(v log.Verified) error { edges++; return nil })
	return edges, lastSeal, err
}

// NewConcept mints a concept id and names it (§5.1).
func (s *Store) NewConcept(name string) (string, uint64, error) {
	id, err := s.newConceptID()
	if err != nil {
		return "", 0, err
	}
	seq, err := s.Edge("names", id, "lit:"+name, nil)
	return id, seq, err
}

func (s *Store) newConceptID() (string, error) {
	u, err := NewULID()
	if err != nil {
		return "", err
	}
	return "c:" + u, nil
}
