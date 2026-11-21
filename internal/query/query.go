// Package query is §11.3, the textual form of a query, which is also the
// mount's path grammar (§12.1): terms separated by `/`, each `rel=object`,
// a traversal of §11.2 as its own component (`supersedes^`, `supersedes*N`,
// `cites*N`), and a bare word as a name term when it can be nothing else.
//
// PARSE AND PRINT ARE INVERSES, and the test holds them to it: a query
// printed and parsed again is the same value, and a text parsed and
// printed again is the same text, up to the bare-name shorthand, which
// prints in its long form (`name=alpha`) so that one text means one query.
//
// THE GRAMMAR REFUSES RATHER THAN GUESSES. An empty component, a component
// with no `=` that looks like a term (`rel=`), a depth that is not a
// positive integer within contract.MaxDepth, a traversal before any term,
// a traversal over a relation §11.2 does not name, a bare word that IS a
// relation name (`cites` with its object forgotten): each is a parse error
// wrapping ErrSyntax and naming the component, because the mount will hand
// these to a reader as directory names and a guessed meaning is a wrong
// listing that looks right.
//
// A NAME MAY NOT HOLD `/`. The separator splits before anything else is
// read, so `name=a/b` is two terms and could never be one; the store
// refuses such a name where it is written (store.ErrNameSlash, D-33), and
// this package inherits the guarantee rather than inventing an escape.
package query

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"forge.axonibyte.com/axonibyte/cairn/internal/contract"
	"forge.axonibyte.com/axonibyte/cairn/internal/store"
)

// ErrSyntax is a text that is not a query of §11.3; the message names the
// component that failed. EVERY refusal of Parse wraps it, including the
// traversal refusals that also wrap contract.ErrDepth or
// contract.ErrTraversalRel, so a caller with one question -- "is this a
// query?" -- asks errors.Is(err, ErrSyntax) and nothing else (quill, coop
// main/1065 finding 4).
var ErrSyntax = errors.New("query: not a query of SPEC 11.3")

// ErrBareRelation is a bare word that is a relation name: a reader who typed
// `cites` and forgot the object, refused rather than read as name=cites
// (D-33).
var ErrBareRelation = errors.New("query: a relation name with no object")

// traversable are the relations §11.2 lets a traversal walk.
var traversable = map[string]bool{"supersedes": true, "cites": true}

// Parse reads the textual form. Leading and trailing `/` are allowed, as a
// path has them; empty components between `/` are not.
func Parse(text string) (contract.Query, error) {
	var q contract.Query
	trimmed := strings.Trim(text, "/")
	if trimmed == "" {
		return q, fmt.Errorf("%w: empty", ErrSyntax)
	}
	for _, c := range strings.Split(trimmed, "/") {
		if c == "" {
			return q, fmt.Errorf("%w: empty component in %q", ErrSyntax, text)
		}
		if t, ok, err := parseTraversal(c); err != nil {
			return q, fmt.Errorf("%w: %w", ErrSyntax, err)
		} else if ok {
			if len(q.Terms) == 0 {
				return q, fmt.Errorf("%w: traversal %q before any term", ErrSyntax, c)
			}
			q.Traversal = append(q.Traversal, t)
			continue
		}
		if len(q.Traversal) > 0 {
			return q, fmt.Errorf("%w: term %q after a traversal; terms come first", ErrSyntax, c)
		}
		rel, obj, found := strings.Cut(c, "=")
		if !found {
			// a bare word is a name term when it can be nothing else
			if strings.ContainsAny(c, "*^=") {
				return q, fmt.Errorf("%w: %q is neither rel=object nor a traversal", ErrSyntax, c)
			}
			if c == contract.NameRel || store.IsRelationName(c) {
				return q, fmt.Errorf("%w: %w: %q", ErrSyntax, ErrBareRelation, c)
			}
			q.Terms = append(q.Terms, contract.Term{Rel: contract.NameRel, Object: c})
			continue
		}
		if rel == "" || obj == "" {
			return q, fmt.Errorf("%w: %q needs both a relation and an object", ErrSyntax, c)
		}
		if strings.ContainsAny(rel, "*^") {
			return q, fmt.Errorf("%w: %q mixes a traversal into a term", ErrSyntax, c)
		}
		q.Terms = append(q.Terms, contract.Term{Rel: rel, Object: obj})
	}
	return q, nil
}

// parseTraversal reads `rel^` or `rel*N`; ok is false for anything else so
// the caller can try it as a term.
func parseTraversal(c string) (contract.Traversal, bool, error) {
	if strings.Contains(c, "=") {
		return contract.Traversal{}, false, nil
	}
	if rel, found := strings.CutSuffix(c, "^"); found {
		if rel != "supersedes" {
			return contract.Traversal{}, true, fmt.Errorf("%w: %q: ^ is the supersedes head and nothing else", contract.ErrTraversalRel, c)
		}
		return contract.Traversal{Rel: rel, Head: true}, true, nil
	}
	rel, n, found := strings.Cut(c, "*")
	if !found {
		return contract.Traversal{}, false, nil
	}
	if !traversable[rel] {
		return contract.Traversal{}, true, fmt.Errorf("%w: %q", contract.ErrTraversalRel, c)
	}
	depth, err := strconv.Atoi(n)
	if err != nil || n != strconv.Itoa(depth) || depth < 1 || depth > contract.MaxDepth {
		return contract.Traversal{}, true, fmt.Errorf("%w: %q", contract.ErrDepth, c)
	}
	return contract.Traversal{Rel: rel, Depth: depth}, true, nil
}

// Check is Parse's grammar applied to a hand-built query, so that String
// then Parse is the identity exactly on the queries Check admits: at least
// one term, no empty relation or object, no `/` in an object (the path
// grammar's separator; §12.1), no relation written as a name, and every
// traversal of §11.2's form. An object MAY hold `=`, which Parse reads at
// its first occurrence only. Parse's own output always passes it.
func Check(q contract.Query) error {
	if len(q.Terms) == 0 {
		return fmt.Errorf("%w: no term", ErrSyntax)
	}
	for _, t := range q.Terms {
		if t.Rel == "" || t.Object == "" {
			return fmt.Errorf("%w: term %q needs both a relation and an object", ErrSyntax, t.Rel+"="+t.Object)
		}
		// `=` is significant only at its first occurrence, so an object may
		// hold it and round-trips (quill, coop main/1067); `/` is the separator
		if strings.ContainsAny(t.Rel, "/*^=") || strings.Contains(t.Object, "/") {
			return fmt.Errorf("%w: %q holds a separator", ErrSyntax, t.Rel+"="+t.Object)
		}
	}
	for _, t := range q.Traversal {
		if _, _, err := parseTraversal(traversalString(t)); err != nil {
			return fmt.Errorf("%w: %w", ErrSyntax, err)
		}
	}
	return nil
}

func traversalString(t contract.Traversal) string {
	if t.Head {
		return t.Rel + "^"
	}
	return t.Rel + "*" + strconv.Itoa(t.Depth)
}

// String prints the textual form: every term in its long form, then the
// traversals, joined by `/`, no leading or trailing slash. It prints what
// it is given; Check says whether Parse would read it back (quill, coop
// main/1065 finding 3).
func String(q contract.Query) string {
	parts := make([]string, 0, len(q.Terms)+len(q.Traversal))
	for _, t := range q.Terms {
		parts = append(parts, t.Rel+"="+t.Object)
	}
	for _, t := range q.Traversal {
		parts = append(parts, traversalString(t))
	}
	return strings.Join(parts, "/")
}
