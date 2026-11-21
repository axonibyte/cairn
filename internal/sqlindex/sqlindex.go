// Package sqlindex is §6.3's reference index: SQLite, derived from the log,
// disposable. It implements the same contract.Index as internal/index and
// is held to it by the tier-2 comparison in sqlindex_test.go: the two answer
// one query set identically, errors included, so a disagreement is a finding
// about one of them and never a question about the rule (TESTING.md).
//
// SAME RULES AS THE MEMORY INDEX (§10.3, D-29, D-31): written only by Apply;
// Rebuild from seq 1 replaces everything or poisons; At() is the log
// position; Query answers §11.1 and walks §11.2; QueryAt is §10.2's refusal;
// Edge is the history read. The schema (§10.1) is this file's and not the
// specification's: edges by seq with their relation, subject, object, hash
// and the seq of the retracts that withdrew them, indexed by (rel, o) and
// (rel, s). Everything a query needs is one table, because the log is one
// sequence.
//
// THE DRIVER IS PURE GO (modernc.org/sqlite), the project's first dependency
// (D-34): the reference index must build wherever the store does -- the
// FreeBSD jails and the reaper guest have no C toolchain by default -- and a
// cgo driver would make the archival reader's second implementation depend
// on a compiler. The file lives beside the store as `index.sqlite` or in
// memory for tests; deleting it loses nothing (§10.3).
package sqlindex

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"

	_ "modernc.org/sqlite"

	"forge.axonibyte.com/axonibyte/cairn/internal/canon"
	"forge.axonibyte.com/axonibyte/cairn/internal/contract"
	"forge.axonibyte.com/axonibyte/cairn/internal/index"
	"forge.axonibyte.com/axonibyte/cairn/internal/log"
)

// SQLite is the index. Open it with Open; the zero value is not usable.
type SQLite struct {
	db       *sql.DB
	at       uint64
	poisoned error
}

var _ contract.Index = (*SQLite)(nil)

const schema = `
CREATE TABLE IF NOT EXISTS edges (
  seq          INTEGER PRIMARY KEY,
  rel          TEXT NOT NULL,
  s            TEXT NOT NULL,
  o            TEXT NOT NULL,
  hash         TEXT NOT NULL,
  retracted_by INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS edges_rel_o ON edges(rel, o);
CREATE INDEX IF NOT EXISTS edges_rel_s ON edges(rel, s);
CREATE TABLE IF NOT EXISTS meta (k TEXT PRIMARY KEY, v INTEGER NOT NULL);
`

// Open opens or creates the index at path; ":memory:" is an index that lives
// as long as the process. An existing file resumes at its recorded At().
func Open(path string) (*SQLite, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// one connection: an in-memory database per connection would be two
	// indexes, and on a file it serialises every reader -- acceptable for
	// 0.1's one-process CLI and a bottleneck the mount (M3) must revisit
	// (quill, coop main/1074)
	db.SetMaxOpenConns(1)
	// the index is disposable (§6.3, §10.3): durability is the log's, so the
	// journal and the sync are relaxed -- a crash mid-write costs a rebuild
	// and nothing else, and a rebuild is the test of the index anyway (D-34)
	if _, err := db.Exec(`PRAGMA journal_mode = MEMORY; PRAGMA synchronous = OFF;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlindex: pragmas: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlindex: schema: %w", err)
	}
	ix := &SQLite{db: db}
	var at sql.NullInt64
	if err := db.QueryRow(`SELECT v FROM meta WHERE k = 'at'`).Scan(&at); err != nil && !errors.Is(err, sql.ErrNoRows) {
		db.Close()
		return nil, err
	}
	if at.Valid {
		ix.at = uint64(at.Int64)
	}
	// THE RESUME IS CHECKED, NOT TRUSTED (quill, coop main/1074). A file is a
	// state the memory index can never hold: its recorded position and its
	// rows can disagree after a crash or a hand, and an At() above the rows
	// defeats the one currency guard the contract has, since ErrStale fires
	// only when At() is too low. Every seq 1..At() must be present and none
	// beyond it, or the index opens poisoned and answers nothing until a
	// Rebuild.
	var count, maxSeq int64
	if err := db.QueryRow(`SELECT COUNT(*), COALESCE(MAX(seq), 0) FROM edges`).Scan(&count, &maxSeq); err != nil {
		db.Close()
		return nil, err
	}
	if uint64(maxSeq) != ix.at || uint64(count) != ix.at {
		ix.poison(fmt.Errorf("the file's recorded position %d disagrees with its rows (%d rows, highest seq %d)", ix.at, count, maxSeq))
	}
	return ix, nil
}

// Close releases the database. The index on disk is unchanged.
func (x *SQLite) Close() error { return x.db.Close() }

// At is the seq of the last edge applied (§10.2); 0 reflects nothing, fresh
// or poisoned alike (D-31).
func (x *SQLite) At() uint64 { return x.at }

func (x *SQLite) poison(err error) {
	x.at = 0
	x.poisoned = fmt.Errorf("%w: %v", contract.ErrPoisoned, err)
}

// execer is what applyIn writes through: the database itself for one edge,
// or the transaction of a Rebuild for all of them.
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

// Apply applies the edge at At()+1 in one transaction: the edge row, the
// retraction it performs, and the new At().
func (x *SQLite) Apply(v log.Verified) error {
	if x.poisoned != nil {
		return x.poisoned
	}
	tx, err := x.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := x.applyIn(tx, v); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	x.at = v.Edge.Seq
	return nil
}

// applyIn writes one edge through w without committing or moving At(); the
// caller commits and advances, so a failed write leaves At() where it was.
func (x *SQLite) applyIn(tx execer, v log.Verified) error {
	e := v.Edge
	if e.Seq != x.at+1 {
		return fmt.Errorf("%w: got seq %d at At() %d", contract.ErrGap, e.Seq, x.at)
	}
	if e.Rel == "retracts" {
		target, err := index.EdgeRef(e.O)
		if err != nil {
			return err
		}
		res, err := tx.Exec(`UPDATE edges SET retracted_by = ? WHERE seq = ? AND retracted_by = 0`, e.Seq, target)
		if err != nil {
			return err
		}
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM edges WHERE seq = ?`, target).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return fmt.Errorf("%w: seq %d retracts %s", contract.ErrDangling, e.Seq, e.O)
		}
		_ = res // a retracts of an already retracted edge records the mistake and changes nothing (§7.1, D-31)
	}
	if _, err := tx.Exec(`INSERT INTO edges(seq, rel, s, o, hash, retracted_by) VALUES (?, ?, ?, ?, ?, 0)`, e.Seq, e.Rel, e.S, e.O, v.Hash); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO meta(k, v) VALUES ('at', ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`, e.Seq); err != nil {
		return err
	}
	return nil
}

// Rebuild drops every row and applies the stream from seq 1 IN ONE
// TRANSACTION -- ten thousand edges committed one by one took thirty seconds
// on the reference machine against the roadmap's ten, and the per-edge
// commit bought nothing an index that is rebuilt on failure needs. A failed
// read rolls the transaction back and leaves the index POISONED, as the
// memory index does (D-31).
func (x *SQLite) Rebuild(read func(fn func(log.Verified) error) error) error {
	if err := x.reset(); err != nil {
		x.poison(err)
		return err
	}
	tx, err := x.db.Begin()
	if err != nil {
		x.poison(err)
		return err
	}
	at := uint64(0)
	err = read(func(v log.Verified) error {
		if err := x.applyIn(tx, v); err != nil {
			return err
		}
		x.at = v.Edge.Seq // applyIn checks contiguity against x.at
		at = v.Edge.Seq
		return nil
	})
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		tx.Rollback()
		if rerr := x.reset(); rerr != nil {
			err = fmt.Errorf("%w; and the reset after it: %v", err, rerr)
		}
		x.poison(err)
		return err
	}
	x.at = at
	x.poisoned = nil
	return nil
}

func (x *SQLite) reset() error {
	x.at = 0
	x.poisoned = nil
	_, err := x.db.Exec(`DELETE FROM edges; DELETE FROM meta;`)
	return err
}

// Edge is the history read (§7.1): ErrNoEdge for an absent seq, ErrPoisoned
// while poisoned.
func (x *SQLite) Edge(seq uint64) (index.Record, error) {
	if x.poisoned != nil {
		return index.Record{}, x.poisoned
	}
	var rec index.Record
	var retracted int64
	err := x.db.QueryRow(`SELECT rel, s, o, hash, retracted_by FROM edges WHERE seq = ?`, seq).
		Scan(&rec.Edge.Rel, &rec.Edge.S, &rec.Edge.O, &rec.Hash, &retracted)
	if errors.Is(err, sql.ErrNoRows) {
		return index.Record{}, fmt.Errorf("%w: seq %d at At() %d", contract.ErrNoEdge, seq, x.at)
	}
	if err != nil {
		return index.Record{}, err
	}
	rec.Edge.Seq = seq
	rec.RetractedBy = uint64(retracted)
	return rec, nil
}

// Query answers §11.1 then §11.2, as the memory index does and by the same
// rules; the comparison test holds the two to one answer.
func (x *SQLite) Query(q contract.Query) ([]string, error) {
	if x.poisoned != nil {
		return nil, x.poisoned
	}
	if len(q.Terms) == 0 {
		return nil, contract.ErrEmptyQuery
	}
	var result map[string]bool
	for _, t := range q.Terms {
		rel, object := t.Rel, t.Object
		if rel == contract.NameRel {
			rel, object = "names", canon.Literal(t.Object)
		}
		rows, err := x.db.Query(`SELECT s FROM edges WHERE rel = ? AND o = ? AND retracted_by = 0`, rel, object)
		if err != nil {
			return nil, err
		}
		subjects := map[string]bool{}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				rows.Close()
				return nil, err
			}
			subjects[s] = true
		}
		rows.Close()
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
	current := map[string]bool{}
	for s := range result {
		sup, err := x.superseded(s)
		if err != nil {
			return nil, err
		}
		if !sup {
			current[s] = true
		}
	}
	for _, t := range q.Traversal {
		var err error
		current, err = x.traverse(current, t)
		if err != nil {
			return nil, err
		}
	}
	out := make([]string, 0, len(current))
	for s := range current {
		out = append(out, s)
	}
	sort.Strings(out)
	return out, nil
}

// QueryAt is §10.2's refusal; the head is the caller's (D-31).
func (x *SQLite) QueryAt(q contract.Query, head uint64) ([]string, error) {
	if x.poisoned != nil {
		return nil, x.poisoned
	}
	if x.at < head {
		return nil, fmt.Errorf("%w: At() %d, head %d", contract.ErrStale, x.at, head)
	}
	return x.Query(q)
}

// superseded is §5.4.
func (x *SQLite) superseded(s string) (bool, error) {
	var n int
	err := x.db.QueryRow(`SELECT COUNT(*) FROM edges WHERE rel = 'supersedes' AND o = ? AND retracted_by = 0`, s).Scan(&n)
	return n > 0, err
}

// objectsOf is the forward hop: the objects of un-retracted `rel` edges whose
// subject is s, in seq order.
func (x *SQLite) objectsOf(rel, s string) ([]string, error) {
	rows, err := x.db.Query(`SELECT o FROM edges WHERE rel = ? AND s = ? AND retracted_by = 0 ORDER BY seq`, rel, s)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var o string
		if err := rows.Scan(&o); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// traverse is §11.2 with the memory index's exact rules (index.go).
func (x *SQLite) traverse(set map[string]bool, t contract.Traversal) (map[string]bool, error) {
	if t.Head {
		if t.Rel != "supersedes" {
			return nil, fmt.Errorf("%w: %s^", contract.ErrTraversalRel, t.Rel)
		}
		out := map[string]bool{}
		for s := range set {
			head, err := x.headOf(s)
			if err != nil {
				return nil, err
			}
			out[head] = true
		}
		return out, nil
	}
	if t.Rel != "supersedes" && t.Rel != "cites" {
		return nil, fmt.Errorf("%w: %s", contract.ErrTraversalRel, t.Rel)
	}
	if t.Depth < 1 || t.Depth > contract.MaxDepth {
		return nil, fmt.Errorf("%w: %s*%d", contract.ErrDepth, t.Rel, t.Depth)
	}
	visited := map[string]bool{}
	frontier := set
	for hop := 0; hop < t.Depth && len(frontier) > 0; hop++ {
		next := map[string]bool{}
		for s := range frontier {
			objects, err := x.objectsOf(t.Rel, s)
			if err != nil {
				return nil, err
			}
			for _, o := range objects {
				if visited[o] {
					continue
				}
				visited[o] = true
				next[o] = true
			}
		}
		frontier = next
	}
	return visited, nil
}

// headOf is §5.4's current head, bounded twice as the memory index's is
// (D-32): the lowest-seq un-retracted superseder is followed at a fork.
func (x *SQLite) headOf(s string) (string, error) {
	seen := map[string]bool{}
	for hops := 0; ; hops++ {
		if seen[s] || hops > contract.MaxDepth {
			return "", fmt.Errorf("%w: at %s after %d hops", contract.ErrNoHead, s, hops)
		}
		seen[s] = true
		var next string
		err := x.db.QueryRow(`SELECT s FROM edges WHERE rel = 'supersedes' AND o = ? AND retracted_by = 0 ORDER BY seq LIMIT 1`, s).Scan(&next)
		if errors.Is(err, sql.ErrNoRows) {
			return s, nil
		}
		if err != nil {
			return "", err
		}
		s = next
	}
}
