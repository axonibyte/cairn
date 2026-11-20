// Package index is the first implementation of §6.1's Index contract: an
// in-memory index built by applying edges, and nothing else.
//
// WHY MEMORY FIRST. §6.3 names SQLite as the reference index. This package
// is the oracle the SQLite index will be compared against (TESTING.md tier
// 2, two implementations of one claim): it carries the semantics of §5.4,
// §7.1 and §11.1 in the plainest Go that states them, so a disagreement
// between it and the SQLite index is a finding about one of them and not a
// question about what the rule meant. It is also what a reader with no
// SQLite gets.
//
// WHAT IT MAY NOT DO (§10.3, D-29). Every answer here is derivable from the
// edges applied at or before At(); the only write path is Apply, which takes
// an edge; Rebuild from seq 1 answers identically to the index it replaces,
// and index_test.go rebuilds and compares with a control that proves the
// comparison can see a one-edge difference. An index is never the only copy
// of anything: deleting one loses nothing but time.
package index

import (
	"fmt"
	"sort"

	"forge.axonibyte.com/axonibyte/cairn/internal/canon"
	"forge.axonibyte.com/axonibyte/cairn/internal/contract"
	"forge.axonibyte.com/axonibyte/cairn/internal/log"
)

// Record is one edge as the index holds it: the edge, its chain hash, and
// the seq of the `retracts` edge that withdrew it (0 while it stands). A
// retracted edge is absent for query and currency and present here as
// history (§7.1).
type Record struct {
	Edge        canon.Edge
	Hash        string
	RetractedBy uint64
}

// Memory is the in-memory index. The zero value is an index at seq 0.
type Memory struct {
	at    uint64
	edges map[uint64]*Record
	// the error of the last failed Rebuild; while set, the index answers
	// nothing (quill, coop main/1058: an index at 0 after a failed rebuild
	// answered [] with no error, indistinguishable from "nothing matches")
	poisoned error
	// by relation then object: the seqs of edges `rel` whose o is `object`
	byRelObject map[string]map[string][]uint64
	// seqs of `supersedes` edges by their object (§5.4)
	supersededBy map[string][]uint64
}

var _ contract.Index = (*Memory)(nil)

// New is an empty index at seq 0.
func New() *Memory {
	return &Memory{edges: map[uint64]*Record{}, byRelObject: map[string]map[string][]uint64{}, supersededBy: map[string][]uint64{}}
}

func (m *Memory) reset() { *m = *New() }

// poison is the state after a failed Rebuild: every answer refuses with
// ErrPoisoned until a Rebuild succeeds, so a caller that ignores Rebuild's
// error cannot read an empty index as an empty store.
func (m *Memory) poison(err error) {
	m.reset()
	m.poisoned = fmt.Errorf("%w: %v", contract.ErrPoisoned, err)
}

// At is the seq of the last edge applied (§10.2). A poisoned index is at 0,
// which is true -- it reflects no position -- and is why At() alone never
// says "fresh": every reader of a position asks Query, Apply or Edge, which
// refuse (D-31).
func (m *Memory) At() uint64 { return m.at }

// Apply applies the edge at At()+1. Every fact the index will ever return
// enters here and nowhere else (§10.3).
func (m *Memory) Apply(v log.Verified) error {
	if m.poisoned != nil {
		return m.poisoned
	}
	e := v.Edge
	if e.Seq != m.at+1 {
		return fmt.Errorf("%w: got seq %d at At() %d", contract.ErrGap, e.Seq, m.at)
	}
	if m.edges == nil {
		m.reset()
	}
	if e.Rel == "retracts" {
		target, err := edgeRef(e.O)
		if err != nil {
			return err
		}
		rec, ok := m.edges[target]
		if !ok {
			return fmt.Errorf("%w: seq %d retracts %s", contract.ErrDangling, e.Seq, e.O)
		}
		if rec.RetractedBy == 0 {
			rec.RetractedBy = e.Seq
		}
	}
	m.edges[e.Seq] = &Record{Edge: e, Hash: v.Hash}
	if e.Rel != "seal" && e.O != "" {
		byObject := m.byRelObject[e.Rel]
		if byObject == nil {
			byObject = map[string][]uint64{}
			m.byRelObject[e.Rel] = byObject
		}
		byObject[e.O] = append(byObject[e.O], e.Seq)
	}
	if e.Rel == "supersedes" {
		m.supersededBy[e.O] = append(m.supersededBy[e.O], e.Seq)
	}
	m.at = e.Seq
	return nil
}

// Rebuild discards the index and applies every edge `read` yields from seq
// 1 (§6.1, §10.3). On a failure the index is POISONED rather than left
// empty: an index that reflects part of a log is the thing §10.3 forbids,
// and an empty one that answers [] without error would pass for an empty
// store (D-31). A later successful Rebuild clears the poison.
func (m *Memory) Rebuild(read func(fn func(log.Verified) error) error) error {
	m.reset()
	if err := read(m.Apply); err != nil {
		m.poison(err)
		return err
	}
	return nil
}

// Edge is the history read: the record at seq. A retracted edge is returned
// with RetractedBy set (§7.1). An absent seq is ErrNoEdge; a poisoned index
// refuses with ErrPoisoned rather than telling a reader the edge was never
// there (quill, coop main/1060: 7.1 requires history to stay readable, and
// a false "no such edge" is the opposite of readable).
func (m *Memory) Edge(seq uint64) (Record, error) {
	if m.poisoned != nil {
		return Record{}, m.poisoned
	}
	rec, ok := m.edges[seq]
	if !ok {
		return Record{}, fmt.Errorf("%w: seq %d at At() %d", contract.ErrNoEdge, seq, m.at)
	}
	return *rec, nil
}

// Query answers §11.1: the subjects for which every term holds on an
// un-retracted edge, less the subjects an un-retracted `supersedes` edge
// names as object (§5.4). A retraction is final (§7.1, D-31): retracting a
// `retracts` edge records the mistake and restores nothing, so RetractedBy
// is never cleared here. Results are sorted, so two indexes that agree
// agree byte for byte.
func (m *Memory) Query(q contract.Query) ([]string, error) {
	if m.poisoned != nil {
		return nil, m.poisoned
	}
	if len(q.Terms) == 0 {
		return nil, contract.ErrEmptyQuery
	}
	var result map[string]bool
	for _, t := range q.Terms {
		rel := t.Rel
		if rel == contract.NameRel {
			rel = "names"
		}
		subjects := map[string]bool{}
		for _, seq := range m.byRelObject[rel][t.Object] {
			rec := m.edges[seq]
			if rec.RetractedBy != 0 {
				continue
			}
			subjects[rec.Edge.S] = true
		}
		if result == nil {
			result = subjects
			continue
		}
		for s := range result {
			if !subjects[s] {
				delete(result, s)
			}
		}
	}
	out := make([]string, 0, len(result))
	for s := range result {
		if m.superseded(s) {
			continue
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return out, nil
}

// QueryAt is Query only when the index is at least at `head` (§10.2).
func (m *Memory) QueryAt(q contract.Query, head uint64) ([]string, error) {
	if m.poisoned != nil {
		return nil, m.poisoned
	}
	if m.at < head {
		return nil, fmt.Errorf("%w: At() %d, head %d", contract.ErrStale, m.at, head)
	}
	return m.Query(q)
}

// superseded is §5.4: some un-retracted `supersedes` edge names s as object.
func (m *Memory) superseded(s string) bool {
	for _, seq := range m.supersededBy[s] {
		if m.edges[seq].RetractedBy == 0 {
			return true
		}
	}
	return false
}

// edgeRef reads `edge:<seq>` (§4.2).
func edgeRef(ref string) (uint64, error) {
	var seq uint64
	if n, err := fmt.Sscanf(ref, "edge:%d", &seq); err != nil || n != 1 || seq == 0 || fmt.Sprintf("edge:%d", seq) != ref {
		return 0, fmt.Errorf("index: %q is not an edge reference", ref)
	}
	return seq, nil
}
