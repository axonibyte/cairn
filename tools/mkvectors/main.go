// mkvectors writes tools/vectors.json, the cross-implementation vector file
// of TESTING tier 2: fixed seeds, fixed edges, and the bytes every
// conformant implementation must produce for them -- signing forms,
// written forms, chain hashes, key ids, signatures of both suites, and the
// founding hash of a fixed cairn.json. Run `go run ./tools/mkvectors >
// tools/vectors.json` only when the specification changes; the committed
// file is the record, and tests/vectors_test.go holds the code to it.
package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"forge.axonibyte.com/axonibyte/cairn/internal/canon"
	"forge.axonibyte.com/axonibyte/cairn/internal/log"
	"forge.axonibyte.com/axonibyte/cairn/internal/writer"
)

type Vectors struct {
	Spec     string      `json:"spec"`
	Note     string      `json:"note"`
	Seeds    Seeds       `json:"seeds"`
	Keys     Keys        `json:"keys"`
	Config   string      `json:"config"`
	Founding string      `json:"founding"`
	Edges    []EdgeVec   `json:"edges"`
	Strings  []StringVec `json:"strings"`
}

type Seeds struct {
	Ed25519 string `json:"ed25519"`
	MLDSA65 string `json:"mldsa65"`
}

type Keys struct {
	Ed25519Public string `json:"ed25519_public"`
	Ed25519ID     string `json:"ed25519_id"`
	MLDSA65Public string `json:"mldsa65_public"`
	MLDSA65ID     string `json:"mldsa65_id"`
}

type EdgeVec struct {
	Input   map[string]any `json:"input"`
	Signing string         `json:"signing_form"`
	Sig     string         `json:"sig"`
	Written string         `json:"written_form"`
	Hash    string         `json:"hash"`
}

type StringVec struct {
	Input   string `json:"input"`
	Escaped string `json:"escaped"`
}

func main() {
	seedA := strings.Repeat("01", 32)
	seedB := strings.Repeat("02", 32)
	sa, _ := hex.DecodeString(seedA)
	sb, _ := hex.DecodeString(seedB)
	ed, err := writer.FromSeed(writer.Ed25519, sa)
	must(err)
	ml, err := writer.FromSeed(writer.MLDSA65, sb)
	must(err)
	config := fmt.Sprintf(`{"cairn":"store","v":1,"spec":"0.1","signing":"%s","sealing":"%s","blob_bound":8388608,"chunk_size":4194304,"segment_bytes":67108864}`+"\n", ed.Public().String(), ml.Public().String())
	founding := canon.Hash([]byte(config))
	v := Vectors{
		Spec:   "0.1",
		Note:   "TESTING tier 2. Every value here is derived from the seeds and the inputs by SPEC §4.2a, §8.1, §8.3 and §8.7; an implementation that produces different bytes for any of them is not conformant. ML-DSA-65 signatures are the deterministic variant (D-24).",
		Seeds:  Seeds{Ed25519: seedA, MLDSA65: seedB},
		Keys:   Keys{Ed25519Public: ed.Public().String(), Ed25519ID: ed.ID(), MLDSA65Public: ml.Public().String(), MLDSA65ID: ml.ID()},
		Config: config, Founding: founding,
	}
	prev := founding
	inputs := []canon.Edge{
		{Seq: 1, T: "2026-11-18T00:00:00.000Z", W: ed.ID(), Rel: "key-of", S: ed.ID(), O: "c:01ARZ3NDEKTSV4RRFFQ69G5FAV", A: map[string]any{"pub": ed.Public().String()}},
		{Seq: 2, T: "2026-11-18T00:00:00.001Z", W: ed.ID(), Rel: "key-of", S: ml.ID(), O: "c:01ARZ3NDEKTSV4RRFFQ69G5FAV", A: map[string]any{"pub": ml.Public().String()}},
		{Seq: 3, T: "2026-11-18T00:00:01.000Z", W: ed.ID(), Rel: "names", S: "c:01ARZ3NDEKTSV4RRFFQ69G5FAV", O: "lit:cal"},
		{Seq: 4, T: "2026-11-18T00:00:02.000Z", W: ed.ID(), Rel: "is-about", S: "sha256:" + strings.Repeat("ab", 32), O: "c:01ARZ3NDEKTSV4RRFFQ69G5FAV", A: map[string]any{"via": "tool <&> / \"quoted\" \\ é   tab\t", "n": int64(-7), "big": int64(9007199254740993), "ok": true, "none": nil, "list": []any{int64(1), "two", false}}},
		{Seq: 5, T: "2026-11-18T00:00:03.000Z", W: ed.ID(), Rel: "axonibyte.flagged", S: "c:01ARZ3NDEKTSV4RRFFQ69G5FAV"},
	}
	for i := range inputs {
		e := inputs[i]
		e.Prev = prev
		key := ed
		if i == 5 {
			key = ml
		}
		must(log.Sign(&e, key))
		signing, _ := canon.Canonical(e, false)
		written, _ := canon.Canonical(e, true)
		in := map[string]any{"seq": e.Seq, "t": e.T, "w": e.W, "prev": e.Prev, "rel": e.Rel, "s": e.S}
		if e.O != "" {
			in["o"] = e.O
		}
		if e.A != nil {
			in["a"] = e.A
		}
		v.Edges = append(v.Edges, EdgeVec{Input: in, Signing: string(signing), Sig: e.Sig, Written: string(written), Hash: canon.Hash(written)})
		prev = canon.Hash(written)
	}
	// the seal: subject the last edge, signed by the mldsa65 key
	seal := canon.Edge{Seq: 6, T: "2026-11-18T00:00:04.000Z", W: ml.ID(), Prev: prev, Rel: "seal", S: "edge:5"}
	must(log.Sign(&seal, ml))
	signing, _ := canon.Canonical(seal, false)
	written, _ := canon.Canonical(seal, true)
	v.Edges = append(v.Edges, EdgeVec{Input: map[string]any{"seq": seal.Seq, "t": seal.T, "w": seal.W, "prev": seal.Prev, "rel": seal.Rel, "s": seal.S}, Signing: string(signing), Sig: seal.Sig, Written: string(written), Hash: canon.Hash(written)})
	for _, s := range []string{"", "plain", "\"quoted\" and \\ backslash", "<&>/", "é ü 中 \U0001F600", "  ", "\x00\x01\x1f", "tab\tnew\nline\rret"} {
		v.Strings = append(v.Strings, StringVec{Input: s, Escaped: canon.EscapeString(s)})
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	must(enc.Encode(v))
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
