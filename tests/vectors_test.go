// Package tests holds the suite's cross-cutting arms: the vector file
// (TESTING tier 2) held against the code, and the archival script.
package tests

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"forge.axonibyte.com/axonibyte/cairn/internal/canon"
	"forge.axonibyte.com/axonibyte/cairn/internal/writer"
)

type vectors struct {
	Spec  string `json:"spec"`
	Seeds struct{ Ed25519, MLDSA65 string }
	Keys  struct {
		Ed25519Public string `json:"ed25519_public"`
		Ed25519ID     string `json:"ed25519_id"`
		MLDSA65Public string `json:"mldsa65_public"`
		MLDSA65ID     string `json:"mldsa65_id"`
	}
	Config   string `json:"config"`
	Founding string `json:"founding"`
	Edges    []struct {
		Input   json.RawMessage `json:"input"`
		Signing string          `json:"signing_form"`
		Sig     string          `json:"sig"`
		Written string          `json:"written_form"`
		Hash    string          `json:"hash"`
	} `json:"edges"`
	Strings []struct{ Input, Escaped string } `json:"strings"`
}

func load(t *testing.T) vectors {
	t.Helper()
	b, err := os.ReadFile("../tools/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	if v.Spec != "0.1" || len(v.Edges) < 6 || len(v.Strings) < 8 {
		t.Fatalf("vector file shape: spec %s, %d edges, %d strings", v.Spec, len(v.Edges), len(v.Strings))
	}
	return v
}

// TESTING tier 2: from the seeds alone, the code reproduces every key id,
// public key, signing form, signature, written form, chain hash and the
// founding hash in the committed vector file, byte for byte. A change to
// §4.2a, §8.1, §8.3 or §8.7 that moves any of them fails here before it
// ships; the file is regenerated only with a decision beside it.
func TestTheCodeReproducesTheVectorFile(t *testing.T) {
	v := load(t)
	sa, _ := hex.DecodeString(v.Seeds.Ed25519)
	sb, _ := hex.DecodeString(v.Seeds.MLDSA65)
	ed, err := writer.FromSeed(writer.Ed25519, sa)
	if err != nil {
		t.Fatal(err)
	}
	ml, err := writer.FromSeed(writer.MLDSA65, sb)
	if err != nil {
		t.Fatal(err)
	}
	if ed.Public().String() != v.Keys.Ed25519Public || ed.ID() != v.Keys.Ed25519ID || ml.Public().String() != v.Keys.MLDSA65Public || ml.ID() != v.Keys.MLDSA65ID {
		t.Fatal("keys from the seeds differ from the vector file")
	}
	if canon.Hash([]byte(v.Config)) != v.Founding {
		t.Fatal("the founding hash of the vector config differs")
	}
	prev := v.Founding
	for i, ev := range v.Edges {
		e, err := canon.Parse(ev.Input)
		if err != nil {
			t.Fatalf("edge %d input: %v", i+1, err)
		}
		if e.Prev != prev {
			t.Fatalf("edge %d: prev %s, chain is at %s", i+1, e.Prev, prev)
		}
		signing, err := canon.Canonical(e, false)
		if err != nil || string(signing) != ev.Signing {
			t.Fatalf("edge %d signing form:\n got %s\nwant %s (%v)", i+1, signing, ev.Signing, err)
		}
		key := ed
		if e.W == ml.ID() {
			key = ml
		}
		sig, err := key.Sign(signing)
		if err != nil || sig != ev.Sig {
			t.Fatalf("edge %d signature differs (%v)", i+1, err)
		}
		if err := key.Public().Verify(signing, ev.Sig); err != nil {
			t.Fatalf("edge %d: the vector's signature does not verify: %v", i+1, err)
		}
		e.Sig = sig
		written, _ := canon.Canonical(e, true)
		if string(written) != ev.Written {
			t.Fatalf("edge %d written form differs", i+1)
		}
		if canon.Hash(written) != ev.Hash {
			t.Fatalf("edge %d hash differs", i+1)
		}
		// and the written form parses back to itself: the line is its own canonical form
		back, err := canon.Parse([]byte(ev.Written))
		if err != nil {
			t.Fatalf("edge %d written form does not parse: %v", i+1, err)
		}
		again, _ := canon.Canonical(back, true)
		if string(again) != ev.Written {
			t.Fatalf("edge %d written form is not a fixed point", i+1)
		}
		prev = ev.Hash
	}
	last := v.Edges[len(v.Edges)-1]
	if !strings.Contains(last.Written, `"rel":"seal"`) || !strings.HasPrefix(last.Sig, "mldsa65:") {
		t.Fatal("the vector file's last edge must be the mldsa65 seal")
	}
	for _, sv := range v.Strings {
		if got := canon.EscapeString(sv.Input); got != sv.Escaped {
			t.Fatalf("string %q escaped as %s, vector says %s", sv.Input, got, sv.Escaped)
		}
	}
}

// The shell implementation holds the vector file too: tools/archive-check.sh
// --vectors re-renders every written form with jq and re-chains it with
// sha256sum. Run from the Go suite so the two implementations are compared
// on every run, not only by hand.
func TestTheShellImplementationHoldsTheVectorFile(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Fatal("jq is required for the vector arm (tools/reaper-bootstrap.sh installs it)")
	}
	out, err := exec.Command("sh", "../tools/archive-check.sh", "--vectors", "../tools/vectors.json").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "edges re-rendered and chained with jq and sha256sum") {
		t.Fatalf("archive-check --vectors: %v\n%s", err, out)
	}
}
