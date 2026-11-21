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
// a traversal over a relation §11.2 does not name, a bare word that could
// be a relation: each is a parse error naming the component, because the
// mount will hand these to a reader as directory names and a guessed
// meaning is a wrong listing that looks right.
package query

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"forge.axonibyte.com/axonibyte/cairn/internal/contract"
)

// ErrSyntax is a text that is not a query of §11.3; the message names the
// component that failed.
var ErrSyntax = errors.New("query: not a query of SPEC 11.3")

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
			return q, err
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

// String prints the textual form: every term in its long form, then the
// traversals, joined by `/`, no leading or trailing slash.
func String(q contract.Query) string {
	parts := make([]string, 0, len(q.Terms)+len(q.Traversal))
	for _, t := range q.Terms {
		parts = append(parts, t.Rel+"="+t.Object)
	}
	for _, t := range q.Traversal {
		if t.Head {
			parts = append(parts, t.Rel+"^")
		} else {
			parts = append(parts, t.Rel+"*"+strconv.Itoa(t.Depth))
		}
	}
	return strings.Join(parts, "/")
}
