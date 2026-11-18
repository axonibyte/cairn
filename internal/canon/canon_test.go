package canon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func full() Edge {
	return Edge{Seq: 7, T: "2026-11-18T09:00:00.123Z", W: "k:ed25519:ab", Prev: "sha256:00", Rel: "is-about", S: "sha256:11", O: "c:01J", A: map[string]any{"via": "tool", "n": json.Number("2")}, Sig: "ed25519:zz"}
}

// SPEC §4.2 and §8.3: keys in the specified order, no whitespace, `a` sorted,
// sig omitted from the signing form and present in the written form.
func TestCanonicalKeyOrderAndForms(t *testing.T) {
	e := full()
	signing, err := Canonical(e, false)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"seq":7,"t":"2026-11-18T09:00:00.123Z","w":"k:ed25519:ab","prev":"sha256:00","rel":"is-about","s":"sha256:11","o":"c:01J","a":{"n":2,"via":"tool"}}`
	if string(signing) != want {
		t.Fatalf("signing form\n got %s\nwant %s", signing, want)
	}
	written, err := Canonical(e, true)
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != strings.TrimSuffix(want, "}")+`,"sig":"ed25519:zz"}` {
		t.Fatalf("written form: %s", written)
	}
	if strings.ContainsAny(string(written), " \n\t") {
		t.Fatalf("whitespace in canonical form: %q", written)
	}
}

// A unary edge has no `o`; an edge with no attributes has no `a` (§4.2).
func TestCanonicalOmitsAbsentOptionals(t *testing.T) {
	e := full()
	e.O, e.A = "", nil
	b, err := Canonical(e, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"o"`) || strings.Contains(string(b), `"a"`) {
		t.Fatalf("optional keys emitted when absent: %s", b)
	}
	if _, err := Canonical(Edge{}, false); !errors.Is(err, ErrMissing) {
		t.Fatalf("an empty edge must be refused as missing, got %v", err)
	}
	if _, err := Canonical(full(), true); err != nil {
		t.Fatal(err)
	}
	e = full()
	e.Sig = ""
	if _, err := Canonical(e, true); !errors.Is(err, ErrMissing) {
		t.Fatalf("the written form without sig must be refused, got %v", err)
	}
}

// The canonical form does not depend on the order keys arrive in or on
// whitespace (TESTING tier 1; mutant: reorder keys, the signature must fail --
// here the bytes must be identical, which is the stronger claim).
func TestParseThenCanonicalIsStableAcrossKeyOrderAndWhitespace(t *testing.T) {
	// the one line that is already canonical: keys in §4.2's order, `a` sorted
	a := `{"seq":7,"t":"2026-11-18T09:00:00.123Z","w":"k:ed25519:ab","prev":"sha256:00","rel":"is-about","s":"sha256:11","o":"c:01J","a":{"n":2,"via":"tool"},"sig":"ed25519:zz"}`
	b := "{ \"sig\" : \"ed25519:zz\",\n \"a\" : { \"n\" : 2 , \"via\" : \"tool\" }, \"o\":\"c:01J\", \"s\":\"sha256:11\", \"rel\":\"is-about\",\n\"prev\":\"sha256:00\", \"w\":\"k:ed25519:ab\", \"t\":\"2026-11-18T09:00:00.123Z\", \"seq\": 7 }"
	ea, err := Parse([]byte(a))
	if err != nil {
		t.Fatal(err)
	}
	eb, err := Parse([]byte(b))
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := Canonical(ea, true)
	cb, _ := Canonical(eb, true)
	if string(ca) != string(cb) {
		t.Fatalf("canonical forms differ:\n%s\n%s", ca, cb)
	}
	if string(ca) != a {
		t.Fatalf("canonical form of the ordered line is not the line itself:\n%s\n%s", ca, a)
	}
	if Hash(ca) != Hash(cb) {
		t.Fatal("hashes differ for one canonical form")
	}
}

// Parse refuses by name: an unknown key, a missing required key, a zero seq,
// a nested attribute (§4.2: `a` is flat).
func TestParseRefusesByName(t *testing.T) {
	base := `{"seq":1,"t":"2026-11-18T09:00:00Z","w":"k:ed25519:ab","prev":"sha256:00","rel":"names","s":"c:01J","o":"lit:cal"}`
	cases := map[string]struct {
		line string
		want error
		text string
	}{
		"unknown key": {strings.Replace(base, `"o":`, `"x":1,"o":`, 1), ErrUnknownKey, `"x"`},
		"missing rel": {strings.Replace(base, `"rel":"names",`, "", 1), ErrMissing, "rel"},
		"seq zero":    {strings.Replace(base, `"seq":1`, `"seq":0`, 1), nil, "seq must be a positive integer"},
		"nested a":    {strings.Replace(base, `"o":"lit:cal"`, `"a":{"k":{"deep":1}}`, 1), nil, "a is not flat"},
		"not object":  {`[1,2]`, nil, "not a JSON object"},
	}
	for name, c := range cases {
		_, err := Parse([]byte(c.line))
		if err == nil {
			t.Fatalf("%s: accepted %s", name, c.line)
		}
		if c.want != nil && !errors.Is(err, c.want) {
			t.Fatalf("%s: got %v, want %v", name, err, c.want)
		}
		if !strings.Contains(err.Error(), c.text) {
			t.Fatalf("%s: the refusal does not name its cause: %v", name, err)
		}
	}
	if _, err := Parse([]byte(base)); err != nil {
		t.Fatalf("the control line must parse: %v", err)
	}
}

// Hash is "sha256:" plus the lowercase hex of SHA-256 over the bytes (§3.1),
// checked against the standard library directly and against a known vector.
func TestHash(t *testing.T) {
	if got := Hash([]byte("")); got != "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("empty input: %s", got)
	}
	in := []byte("cairn")
	sum := sha256.Sum256(in)
	if got := Hash(in); got != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatalf("got %s", got)
	}
}
