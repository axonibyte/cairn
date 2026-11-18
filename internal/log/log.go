// Package log is the file backend's edge log (SPEC §4, §6.2, §8.3, §8.7):
// segments of canonical lines under log/, one writer, contiguous seq, every
// edge signed by a registered writer, every edge chained to the one before
// by prev, and a seal by the post-quantum key at every segment roll and on
// demand. Reading verifies all of it and reports the seq of the last seal
// as the bound of post-quantum provenance.
package log

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"forge.axonibyte.com/axonibyte/cairn/internal/canon"
	"forge.axonibyte.com/axonibyte/cairn/internal/writer"
)

// DefaultSegmentBytes is §4.1's default segment size, 64 MiB.
const DefaultSegmentBytes = 64 << 20

// Core relation names this package acts on.
const (
	RelKeyOf = "key-of"
	RelSeal  = "seal"
)

var (
	// ErrSeq is a non-contiguous seq (§4.3).
	ErrSeq = errors.New("log: seq is not the next value")
	// ErrPrev is a prev that is not the hash of the previous edge (§8.7).
	ErrPrev = errors.New("log: prev does not match the chain")
	// ErrWriter is an edge by a writer the log does not know (§8.3).
	ErrWriter = errors.New("log: writer is not registered")
	// ErrSignature is a signature that does not verify.
	ErrSignature = errors.New("log: signature does not verify")
	// ErrChain is a chain or seal that does not verify on read; it names the
	// first bad seq.
	ErrChain = errors.New("log: chain broken")
	// ErrKeyOf is a key-of edge that does not register a key: the subject is
	// not the id of the key in a.pub, or a.pub is missing or malformed.
	ErrKeyOf = errors.New("log: key-of does not register its key")
	// ErrSealOwed is a segment that reached its bound without a seal as its
	// last line (§8.7): on append, the write is refused before it happens;
	// on open or read, the store is refused by name.
	ErrSealOwed = errors.New("log: a rolled segment is unsealed")
)

// Founder is the store's founding writer (§8.3): the public keys the first
// edges are verified against, and the hash the first edge's prev carries.
type Founder struct {
	Signing writer.Public // the per-edge key
	Sealing writer.Public // the mldsa65 key of the seals
	Hash    string        // the founding hash, prev of seq 1
}

// Log is an open edge log. Not safe for concurrent use by more than one
// goroutine: a store has one writer (D-4).
type Log struct {
	Root         string
	SegmentBytes int64
	founder      Founder
	keys         map[string]writer.Public
	segment      int
	segmentSize  int64
	lastSeq      uint64
	lastHash     string
	lastSeal     uint64
	lastSealedAt uint64 // the seq of the last seal edge itself
}

// Open opens the log under root, creating log/ if absent, replaying what
// is there with full verification and recovering the position. The founder
// seeds the registry; key-of edges extend it.
func Open(root string, founder Founder, segmentBytes int64) (*Log, error) {
	if segmentBytes <= 0 {
		segmentBytes = DefaultSegmentBytes
	}
	if founder.Hash == "" || founder.Signing.Suite == "" || founder.Sealing.Suite != writer.MLDSA65 {
		return nil, errors.New("log: a founder needs a signing key, an mldsa65 sealing key and a founding hash")
	}
	l := &Log{Root: root, SegmentBytes: segmentBytes, founder: founder, keys: map[string]writer.Public{}, lastHash: founder.Hash}
	l.keys[founder.Signing.ID()] = founder.Signing
	l.keys[founder.Sealing.ID()] = founder.Sealing
	if err := os.MkdirAll(filepath.Join(root, "log"), 0o755); err != nil {
		return nil, err
	}
	segs, err := l.segments()
	if err != nil {
		return nil, err
	}
	if len(segs) == 0 {
		l.segment = 0
		return l, nil
	}
	// replay every segment, verifying; the registry and the position follow
	for _, seg := range segs {
		n, size, err := l.replay(seg)
		if err != nil {
			return nil, err
		}
		l.segment = n
		l.segmentSize = size
	}
	// a segment that ended with a seal at the bound has rolled: the next
	// append opens the following segment
	if l.segmentSize >= l.SegmentBytes {
		l.segment++
		l.segmentSize = 0
	}
	return l, nil
}

// segments lists log/<n>.jsonl in order, refusing a gap in the numbering.
func (l *Log) segments() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(l.Root, "log"))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for i, n := range names {
		if n != fmt.Sprintf("%06d.jsonl", i) {
			return nil, fmt.Errorf("log: segments are not contiguous: expected %06d.jsonl, found %s", i, n)
		}
	}
	return names, nil
}

func (l *Log) segmentPath(n int) string {
	return filepath.Join(l.Root, "log", fmt.Sprintf("%06d.jsonl", n))
}

// replay reads one segment file, verifying each edge against the state so
// far, and returns its number and size.
func (l *Log) replay(name string) (int, int64, error) {
	n, _ := strconv.Atoi(strings.TrimSuffix(name, ".jsonl"))
	f, err := os.Open(l.segmentPath(n))
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	var size int64
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		line := sc.Bytes()
		size += int64(len(line)) + 1
		e, err := canon.Parse(line)
		if err != nil {
			return 0, 0, fmt.Errorf("%w at seq %d: %v", ErrChain, l.lastSeq+1, err)
		}
		written, err := canon.Canonical(e, true)
		if err != nil {
			return 0, 0, fmt.Errorf("%w at seq %d: %v", ErrChain, e.Seq, err)
		}
		if string(written) != string(line) {
			return 0, 0, fmt.Errorf("%w at seq %d: the line is not its own canonical form", ErrChain, e.Seq)
		}
		if err := l.check(e); err != nil {
			return 0, 0, fmt.Errorf("%w at seq %d: %v", ErrChain, e.Seq, err)
		}
		l.advance(e, written)
	}
	if err := sc.Err(); err != nil {
		return 0, 0, err
	}
	if size >= l.SegmentBytes && l.lastSealedAt != l.lastSeq {
		return 0, 0, fmt.Errorf("%w: segment %06d is %d bytes, at or over its bound of %d, and its last line (seq %d) is not a seal", ErrSealOwed, n, size, l.SegmentBytes, l.lastSeq)
	}
	return n, size, nil
}

// check verifies an edge against the log's state: seq, prev, writer,
// signature, and what a key-of or seal edge must carry.
func (l *Log) check(e canon.Edge) error {
	if e.Seq != l.lastSeq+1 {
		return fmt.Errorf("%w: got %d, next is %d", ErrSeq, e.Seq, l.lastSeq+1)
	}
	if e.Prev != l.lastHash {
		return fmt.Errorf("%w: got %s, chain is at %s", ErrPrev, e.Prev, l.lastHash)
	}
	pub, ok := l.keys[e.W]
	if !ok {
		return fmt.Errorf("%w: %s", ErrWriter, e.W)
	}
	signing, err := canon.Canonical(e, false)
	if err != nil {
		return err
	}
	if e.Sig == "" {
		return fmt.Errorf("%w: no signature", ErrSignature)
	}
	if err := pub.Verify(signing, e.Sig); err != nil {
		return fmt.Errorf("%w: %v", ErrSignature, err)
	}
	switch e.Rel {
	case RelKeyOf:
		if _, err := keyOf(e); err != nil {
			return err
		}
	case RelSeal:
		if pub.Suite != writer.MLDSA65 {
			return fmt.Errorf("%w: a seal must be signed by the mldsa65 key, not %s", ErrSignature, pub.Suite)
		}
		if e.S != "edge:"+strconv.FormatUint(l.lastSeq, 10) {
			return fmt.Errorf("%w: a seal's subject is the last edge, edge:%d, got %s", ErrChain, l.lastSeq, e.S)
		}
	}
	return nil
}

// keyOf reads the key a key-of edge registers: the subject is the writer
// id, a.pub its public key, and the id must be the hash of that key.
func keyOf(e canon.Edge) (writer.Public, error) {
	pubStr, _ := e.A["pub"].(string)
	if pubStr == "" {
		return writer.Public{}, fmt.Errorf("%w: a.pub is missing", ErrKeyOf)
	}
	pub, err := writer.ParsePublic(pubStr)
	if err != nil {
		return writer.Public{}, fmt.Errorf("%w: %v", ErrKeyOf, err)
	}
	if pub.ID() != e.S {
		return writer.Public{}, fmt.Errorf("%w: subject %s is not the id of a.pub (%s)", ErrKeyOf, e.S, pub.ID())
	}
	return pub, nil
}

// advance moves the log's state past a verified edge.
func (l *Log) advance(e canon.Edge, written []byte) {
	l.lastSeq = e.Seq
	l.lastHash = canon.Hash(written)
	switch e.Rel {
	case RelKeyOf:
		pub, _ := keyOf(e)
		l.keys[e.S] = pub
	case RelSeal:
		l.lastSeal = e.Seq
		l.lastSealedAt = e.Seq
	}
}

// Next returns the seq and prev the next edge must carry.
func (l *Log) Next() (seq uint64, prev string) { return l.lastSeq + 1, l.lastHash }

// LastSeq is the position of the log.
func (l *Log) LastSeq() uint64 { return l.lastSeq }

// LastSeal is the seq of the last seal, the bound of post-quantum
// provenance (§8.7); 0 when none.
func (l *Log) LastSeal() uint64 { return l.lastSeal }

// Registered reports whether a writer id is known to the log.
func (l *Log) Registered(id string) bool { _, ok := l.keys[id]; return ok }

// Append verifies the signed edge against the log and writes its written
// form as the next line, rolling the segment and sealing when the segment
// reaches its bound; sealer is the mldsa65 key used for that seal and may
// be nil when no roll is due. Returns the seq written.
func (l *Log) Append(e canon.Edge, sealer *writer.Key) (uint64, error) {
	if err := l.check(e); err != nil {
		return 0, err
	}
	written, err := canon.Canonical(e, true)
	if err != nil {
		return 0, err
	}
	// a roll owes a seal (§8.7), and the obligation is never left in a return
	// value: when this line would reach the bound and no sealing key is here
	// to pay it, nothing is written (quill, coop main/1001 finding 8)
	rolls := l.segmentSize+int64(len(written))+1 >= l.SegmentBytes && e.Rel != RelSeal
	if rolls && (sealer == nil || sealer.Suite != writer.MLDSA65) {
		return 0, fmt.Errorf("%w: seq %d would roll segment %06d and a seal needs an mldsa65 key; nothing written", ErrSealOwed, e.Seq, l.segment)
	}
	if err := l.writeLine(written); err != nil {
		return 0, err
	}
	l.advance(e, written)
	if rolls {
		if _, err := l.Seal(sealer); err != nil {
			return e.Seq, err
		}
	}
	return e.Seq, nil
}

// Seal appends a seal edge over the chain at this point (§8.7): subject the
// last edge, prev its hash, signed by the mldsa65 key; then, if the segment
// is at its bound, rolls to the next segment. Refused by name for a key
// that is not the registered sealing suite.
func (l *Log) Seal(sealer *writer.Key) (uint64, error) {
	if sealer == nil || sealer.Suite != writer.MLDSA65 {
		return 0, fmt.Errorf("%w: a seal needs an mldsa65 key", ErrSignature)
	}
	if l.lastSeq == 0 {
		return 0, errors.New("log: nothing to seal")
	}
	e := canon.Edge{
		Seq:  l.lastSeq + 1,
		T:    Now(),
		W:    sealer.ID(),
		Prev: l.lastHash,
		Rel:  RelSeal,
		S:    "edge:" + strconv.FormatUint(l.lastSeq, 10),
	}
	if err := Sign(&e, sealer); err != nil {
		return 0, err
	}
	if err := l.check(e); err != nil {
		return 0, err
	}
	written, _ := canon.Canonical(e, true)
	if err := l.writeLine(written); err != nil {
		return 0, err
	}
	l.advance(e, written)
	if l.segmentSize >= l.SegmentBytes {
		l.segment++
		l.segmentSize = 0
	}
	return e.Seq, nil
}

func (l *Log) writeLine(written []byte) error {
	f, err := os.OpenFile(l.segmentPath(l.segment), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(written, '\n')); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	l.segmentSize += int64(len(written)) + 1
	return nil
}

// Sign fills e.Sig with key's signature over e's signing form (§8.3).
func Sign(e *canon.Edge, key *writer.Key) error {
	signing, err := canon.Canonical(*e, false)
	if err != nil {
		return err
	}
	sig, err := key.Sign(signing)
	if err != nil {
		return err
	}
	e.Sig = sig
	return nil
}

// Now is the edge time of §4.2: RFC 3339, UTC, millisecond precision.
var Now = func() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000Z") }

// Verified is one edge read back with its position's chain hash.
type Verified struct {
	Edge canon.Edge
	Hash string
}

// Read verifies the whole log from the founder (every line its own
// canonical form, every seq, prev and signature, every seal by the mldsa65
// key over the last edge) and calls fn for each edge from seq `from` on.
// It returns the seq of the last seal. A failure names the first bad seq.
func Read(root string, founder Founder, segmentBytes int64, from uint64, fn func(Verified) error) (lastSeal uint64, err error) {
	if segmentBytes <= 0 {
		segmentBytes = DefaultSegmentBytes
	}
	l := &Log{Root: root, SegmentBytes: segmentBytes, founder: founder, keys: map[string]writer.Public{}, lastHash: founder.Hash}
	l.keys[founder.Signing.ID()] = founder.Signing
	l.keys[founder.Sealing.ID()] = founder.Sealing
	segs, err := l.segments()
	if err != nil {
		return 0, err
	}
	for _, seg := range segs {
		f, err := os.Open(filepath.Join(root, "log", seg))
		if err != nil {
			return 0, err
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 64<<20)
		var size int64
		for sc.Scan() {
			line := sc.Bytes()
			size += int64(len(line)) + 1
			e, perr := canon.Parse(line)
			if perr != nil {
				f.Close()
				return 0, fmt.Errorf("%w at seq %d: %v", ErrChain, l.lastSeq+1, perr)
			}
			written, _ := canon.Canonical(e, true)
			if string(written) != string(line) {
				f.Close()
				return 0, fmt.Errorf("%w at seq %d: the line is not its own canonical form", ErrChain, e.Seq)
			}
			if cerr := l.check(e); cerr != nil {
				f.Close()
				return 0, fmt.Errorf("%w at seq %d: %v", ErrChain, e.Seq, cerr)
			}
			l.advance(e, written)
			if e.Seq >= from && fn != nil {
				if ferr := fn(Verified{Edge: e, Hash: l.lastHash}); ferr != nil {
					f.Close()
					return 0, ferr
				}
			}
		}
		if serr := sc.Err(); serr != nil {
			f.Close()
			return 0, serr
		}
		f.Close()
		if size >= l.SegmentBytes && l.lastSealedAt != l.lastSeq {
			return 0, fmt.Errorf("%w: segment %s is %d bytes, at or over its bound of %d, and its last line (seq %d) is not a seal", ErrSealOwed, seg, size, l.SegmentBytes, l.lastSeq)
		}
	}
	return l.lastSeal, nil
}
