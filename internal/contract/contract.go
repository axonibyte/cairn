// Package contract is SPEC §6.1 as Go interfaces: the two contracts the
// engine speaks to storage through, so that backends may change behind them
// (D-10). Store is truth; Index is derived from it and may answer nothing
// the log cannot (§10.3, D-29).
//
// The query expression of §11 lives here too, as data: a conjunction of
// terms. Its textual form (§11.3) and traversal (§11.2) are later heads of
// M2 and are not in this file.
package contract

import (
	"errors"
	"io"

	"forge.axonibyte.com/axonibyte/cairn/internal/canon"
	"forge.axonibyte.com/axonibyte/cairn/internal/log"
)

// Store is §6.1's truth contract. The file backend (internal/store) is the
// reference implementation and the archival form (§6.2).
type Store interface {
	// PutBlob stores the bytes and returns the identity; idempotent.
	PutBlob(r io.Reader) (identity string, err error)
	// GetBlob returns the bytes, verified against the identity before return.
	GetBlob(identity string) ([]byte, error)
	// Append appends a signed edge. It MUST refuse a non-contiguous seq and
	// an invalid signature.
	Append(e canon.Edge) (uint64, error)
	// ReadLog streams the verified edges from seq `from` on.
	ReadLog(from uint64, fn func(log.Verified) error) error
	// Head is the seq of the last edge in the log.
	Head() uint64
}

// Index is §6.1's derived contract. An index is written only by applying
// edges from the log (§10.3): there is no other method that changes what it
// answers.
type Index interface {
	// Rebuild discards everything and applies every edge `read` yields, in
	// order from seq 1. A conformant implementation carries a test that
	// rebuilds and compares (§10.3).
	Rebuild(read func(fn func(log.Verified) error) error) error
	// Apply applies the next edge. It MUST refuse an edge whose seq is not
	// At()+1: an index with a gap would answer from a log that never was.
	Apply(v log.Verified) error
	// At is the log position the index reflects (§10.2).
	At() uint64
	// Query answers q from the edges at or before At() (§11.1).
	Query(q Query) ([]string, error)
	// QueryAt answers q only if At() is not staler than head (§10.2); a
	// staler index refuses with ErrStale rather than answering from the past.
	// The head is the CALLER's to supply, from Store.Head(): an index never
	// sees the log (§10.3), so it cannot know the head itself, and a caller
	// that passes 0 has asked for no consistency and gets none (D-31).
	QueryAt(q Query, head uint64) ([]string, error)
}

// Term is one `rel=object` of §11.1. Rel "name" is the name term, which
// resolves through `names` edges to the concepts called `Object`.
type Term struct {
	Rel    string
	Object string
}

// Query is a conjunction of terms (§11.1): the subjects for which every term
// holds on a current, un-retracted edge.
type Query struct {
	Terms []Term
}

// NameRel is the relation name a name term is written with.
const NameRel = "name"

var (
	// ErrStale is QueryAt on an index behind the head it was asked to be
	// consistent with (§10.2).
	ErrStale = errors.New("index: At() is behind the log head; rebuild or apply before a consistent answer")
	// ErrGap is Apply of an edge whose seq is not At()+1.
	ErrGap = errors.New("index: edge seq is not At()+1; an index with a gap answers from a log that never was")
	// ErrEmptyQuery is a query with no term: every subject would hold, which
	// is not an answer §11.1 defines.
	ErrEmptyQuery = errors.New("index: a query needs at least one term")
	// ErrDangling is a retracts edge naming an edge the index has not seen.
	ErrDangling = errors.New("index: retracts names an edge not at or before this seq")
	// ErrPoisoned is any call on an index whose last Rebuild failed: it
	// answers nothing until a Rebuild succeeds, so an empty index cannot pass
	// for an empty store (D-31).
	ErrPoisoned = errors.New("index: the last rebuild failed; rebuild before asking")
)
