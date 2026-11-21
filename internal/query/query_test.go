package query

import (
	"errors"
	"testing"

	"forge.axonibyte.com/axonibyte/cairn/internal/contract"
)

// TestRoundTrip: text -> query -> text is the identity on the long form, and
// query -> text -> query is the identity on the value. The bare-name form
// prints long, so one text means one query.
func TestRoundTrip(t *testing.T) {
	cases := []struct{ in, out string }{
		{"is-about=c:A", "is-about=c:A"},
		{"/is-about=c:A/cites=sha256:B/", "is-about=c:A/cites=sha256:B"},
		{"alpha", "name=alpha"},
		{"name=alpha/is-about=c:A", "name=alpha/is-about=c:A"},
		{"is-about=c:A/supersedes^", "is-about=c:A/supersedes^"},
		{"is-about=c:A/cites*2/supersedes*1", "is-about=c:A/cites*2/supersedes*1"},
		{"axonibyte.decided-by=c:P", "axonibyte.decided-by=c:P"},
	}
	for _, c := range cases {
		q, err := Parse(c.in)
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if got := String(q); got != c.out {
			t.Fatalf("%q printed %q, want %q", c.in, got, c.out)
		}
		again, err := Parse(String(q))
		if err != nil {
			t.Fatalf("reparse of %q: %v", String(q), err)
		}
		if String(again) != c.out {
			t.Fatalf("%q did not survive a second round: %q", c.in, String(again))
		}
	}
}

// TestParseValues pins what the components become.
func TestParseValues(t *testing.T) {
	q, err := Parse("alpha/is-about=c:A/cites*3/supersedes^")
	if err != nil {
		t.Fatal(err)
	}
	wantTerms := []contract.Term{{Rel: contract.NameRel, Object: "alpha"}, {Rel: "is-about", Object: "c:A"}}
	if len(q.Terms) != 2 || q.Terms[0] != wantTerms[0] || q.Terms[1] != wantTerms[1] {
		t.Fatalf("terms %+v", q.Terms)
	}
	if len(q.Traversal) != 2 || q.Traversal[0] != (contract.Traversal{Rel: "cites", Depth: 3}) || q.Traversal[1] != (contract.Traversal{Rel: "supersedes", Head: true}) {
		t.Fatalf("traversals %+v", q.Traversal)
	}
}

// TestRefusals: each malformed text is refused by name, never guessed. The
// control at the end is a well-formed neighbor of each, so the refusals are
// about the defect and not the shape.
func TestRefusals(t *testing.T) {
	refuse := map[string]error{
		"":                         ErrSyntax,
		"/":                        ErrSyntax,
		"is-about=c:A//cites=x":    ErrSyntax,
		"is-about=":                ErrSyntax,
		"=c:A":                     ErrSyntax,
		"supersedes^":              ErrSyntax, // a traversal before any term
		"cites*2/is-about=c:A":     ErrSyntax,
		"is-about=c:A/cites*2/x=y": ErrSyntax, // a term after a traversal
		"is-about=c:A/cites*0":     contract.ErrDepth,
		"is-about=c:A/cites*65":    contract.ErrDepth,
		"is-about=c:A/cites*01":    contract.ErrDepth,
		"is-about=c:A/cites*-1":    contract.ErrDepth,
		"is-about=c:A/cites*two":   contract.ErrDepth,
		"is-about=c:A/is-about*2":  contract.ErrTraversalRel,
		"is-about=c:A/cites^":      contract.ErrTraversalRel,
		"is-abo*ut=c:A":            ErrSyntax,
		"al^pha":                   ErrSyntax,
	}
	for text, want := range refuse {
		if _, err := Parse(text); !errors.Is(err, want) {
			t.Errorf("%q: %v, want %v", text, err, want)
		}
	}
	for _, ok := range []string{"is-about=c:A/cites*1", "is-about=c:A/cites*64", "is-about=c:A/supersedes^", "alpha"} {
		if _, err := Parse(ok); err != nil {
			t.Errorf("control %q refused: %v", ok, err)
		}
	}
}
