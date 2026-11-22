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
	// At is the log position the index reflects (§10.2). 0 is an index that
	// reflects nothing, fresh or poisoned alike; it is not evidence of an
	// empty store, which only Query, Apply or a history read can give (D-31).
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

// Currency is the one question §5.4 asks of an index beyond a query: the
// current head of an identity's supersedes chain. It is an extension and
// not part of Index, because §6.1 lists what every index MUST answer and a
// head is derivable from Query (`<term>/supersedes^`) for any identity a
// term can select; an index that implements it answers `cairn current`
// directly. An identity nothing supersedes is its own head, seen or not.
type Currency interface {
	Current(identity string) (string, error)
}

// Term is one `rel=object` of §11.1. Rel "name" is the name term, which
// resolves through `names` edges to the concepts called `Object`.
type Term struct {
	Rel    string
	Object string
}

// Traversal is §11.2: after the terms select a set of subjects, a traversal
// of bounded depth maps the set along one relation. `supersedes^` (Head)
// replaces each subject by the current head of its supersedes chain, the
// one no un-retracted supersedes edge names as object; `rel*N` replaces the
// set by everything reachable from it in 1..N hops along un-retracted `rel`
// edges, subject to object. Depth is bounded by MaxDepth; unbounded
// traversal is not in 0.1.
type Traversal struct {
	Rel   string
	Depth int  // hops, 1..MaxDepth; ignored when Head
	Head  bool // supersedes^: follow the chain forward to its current head
}

// Query is a conjunction of terms (§11.1): the subjects for which every term
// holds on a current, un-retracted edge, then the traversals of §11.2 in
// order.
type Query struct {
	Terms     []Term
	Traversal []Traversal
}

// NameRel is the relation name a name term is written with.
const NameRel = "name"

// MaxDepth bounds a traversal (§11.2): a depth above it is refused, not
// clamped, because a clamped answer is a different question answered
// quietly.
const MaxDepth = 64

var (
	// ErrStale is QueryAt on an index behind the head it was asked to be
	// consistent with (§10.2).
	ErrStale = errors.New("index: At() is behind the log head; rebuild or apply before a consistent answer")
	// ErrHeadDisagrees is Sync's refusal when the head it was given is BEHIND
	// the log its reader ends at: neither side is trusted and no index comes
	// back. §10.2 defines ErrStale as an index behind the head, so the converse
	// has its own identity rather than a message that contradicts its name
	// (quill, coop main/1087).
	ErrHeadDisagrees = errors.New("index: the head asked for is behind the log the reader ends at; neither is trusted")
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
	// ErrNoEdge is a history read of a seq the index does not hold.
	ErrNoEdge = errors.New("index: no edge at that seq")
	// ErrDepth is a traversal depth outside 1..MaxDepth (§11.2).
	ErrDepth = errors.New("query: traversal depth is not within 1..64; unbounded traversal is not in 0.1")
	// ErrNoHead is supersedes^ over a chain with no current head: every member
	// is superseded, which only a cycle of supersedes edges can produce.
	ErrNoHead = errors.New("query: the supersedes chain has no current head (a cycle)")
	// ErrTraversalRel is a traversal over a relation that is not one of §11.2's.
	ErrTraversalRel = errors.New("query: a traversal walks supersedes or cites, nothing else in 0.1")
)
