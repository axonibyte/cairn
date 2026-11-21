package query

import (
	"errors"
	"reflect"
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

// TestOneErrorIdentity: every refusal is an ErrSyntax, and the traversal
// refusals are ALSO their specific error, so one question or two both work
// (quill, coop main/1065 finding 4).
func TestOneErrorIdentity(t *testing.T) {
	for _, text := range []string{"", "is-about=", "cites*0", "is-about=c:A/cites*0", "is-about=c:A/cites*65", "is-about=c:A/is-about*2", "is-about=c:A/cites^", "cites", "name"} {
		_, err := Parse(text)
		if !errors.Is(err, ErrSyntax) {
			t.Errorf("%q: %v does not wrap ErrSyntax", text, err)
		}
	}
	_, err := Parse("is-about=c:A/cites*0")
	if !errors.Is(err, contract.ErrDepth) {
		t.Errorf("depth refusal lost its own identity: %v", err)
	}
	_, err = Parse("is-about=c:A/is-about*2")
	if !errors.Is(err, contract.ErrTraversalRel) {
		t.Errorf("relation refusal lost its own identity: %v", err)
	}
}

// TestBareRelationRefused is the eighteenth refusal: a bare word that is a
// core or dotted relation, or `name` itself, is a reader who forgot the
// object, not a name term. The control is a bare word that is none of those.
func TestBareRelationRefused(t *testing.T) {
	for _, w := range []string{"cites", "is-about", "supersedes", "names", "name", "key-of", "axonibyte.decided-by"} {
		if _, err := Parse(w); !errors.Is(err, ErrBareRelation) {
			t.Errorf("bare %q: %v, want ErrBareRelation", w, err)
		}
	}
	q, err := Parse("alpha")
	if err != nil || q.Terms[0] != (contract.Term{Rel: contract.NameRel, Object: "alpha"}) {
		t.Fatalf("control bare name: %+v %v", q, err)
	}
	if _, err := Parse("cites=sha256:x"); err != nil {
		t.Fatalf("control cites with an object: %v", err)
	}
}

// TestCheckBoundsTheInverse: String then Parse is the identity exactly on
// the queries Check admits (finding 3). The two hand-built queries quill
// named print to texts Parse refuses, and Check refuses them first; a
// name with a slash is refused by Check as the store refuses it at the
// edge; Parse's own output always passes Check.
func TestCheckBoundsTheInverse(t *testing.T) {
	bad := []contract.Query{
		{Traversal: []contract.Traversal{{Rel: "supersedes", Head: true}}},
		{Terms: []contract.Term{{Rel: contract.NameRel, Object: ""}}},
		{Terms: []contract.Term{{Rel: contract.NameRel, Object: "a/b"}}},
		{Terms: []contract.Term{{Rel: "is-about", Object: "c:A"}}, Traversal: []contract.Traversal{{Rel: "cites", Depth: 0}}},
		{Terms: []contract.Term{{Rel: "is-about", Object: "c:A"}}, Traversal: []contract.Traversal{{Rel: "is-about", Depth: 1}}},
	}
	for i, q := range bad {
		if err := Check(q); !errors.Is(err, ErrSyntax) {
			t.Errorf("bad %d (%q): Check %v", i, String(q), err)
		}
		// String then Parse is not the identity on these: either Parse refuses
		// the text, or (the slash case) it reads a different query
		if again, err := Parse(String(q)); err == nil && String(again) == String(q) && reflect.DeepEqual(again, q) {
			t.Errorf("bad %d: %q round-tripped although Check refused it", i, String(q))
		}
	}
	// = in an object is admitted and round-trips: Parse cuts at the first =
	eq := contract.Query{Terms: []contract.Term{{Rel: contract.NameRel, Object: "b=c"}}}
	if err := Check(eq); err != nil {
		t.Errorf("Check refused = in an object: %v", err)
	}
	if again, err := Parse(String(eq)); err != nil || !reflect.DeepEqual(again, eq) {
		t.Errorf("= in an object did not round-trip: %+v %v", again, err)
	}
	for _, text := range []string{"is-about=c:A", "alpha/cites*2/supersedes^", "axonibyte.decided-by=c:P/supersedes*64"} {
		q, err := Parse(text)
		if err != nil {
			t.Fatal(err)
		}
		if err := Check(q); err != nil {
			t.Errorf("Parse's output %q refused by Check: %v", text, err)
		}
		if again, _ := Parse(String(q)); String(again) != String(q) {
			t.Errorf("%q: not the identity", text)
		}
	}
}
