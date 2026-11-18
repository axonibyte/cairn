// Package canon is the canonical form of an edge (SPEC §4.2, §8.3) and the
// hash over it (§8.7). Everything that signs, chains or compares edges goes
// through this package, so there is exactly one answer to "what are the
// bytes of this edge".
//
// The canonical form is the edge's JSON with keys in the order of SPEC §4.2
// (seq, t, w, prev, rel, s, o, a, sig), no whitespace, UTF-8, with the keys
// of `a` sorted bytewise. Signing omits `sig`; the chain hash is over the
// signed edge, `sig` included, so that a forged signature breaks the chain.
package canon

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// Edge is one line of the log. Field names follow SPEC §4.2; O, A and Sig
// are optional and absent when empty.
type Edge struct {
	Seq  uint64
	T    string
	W    string
	Prev string
	Rel  string
	S    string
	O    string
	A    map[string]any
	Sig  string
}

// Keys in canonical order (SPEC §4.2).
var keys = []string{"seq", "t", "w", "prev", "rel", "s", "o", "a", "sig"}

// ErrUnknownKey is returned by Parse for a key SPEC §4.2 does not name.
var ErrUnknownKey = errors.New("canon: unknown key")

// ErrMissing is returned by Parse for a required key that is absent.
var ErrMissing = errors.New("canon: missing key")

// Canonical returns the canonical bytes of e, with or without its signature.
// withSig false is the signing form (§8.3); true is the line that is written
// and hashed (§8.7).
func Canonical(e Edge, withSig bool) ([]byte, error) {
	if e.Seq == 0 {
		return nil, fmt.Errorf("%w: seq must be at least 1", ErrMissing)
	}
	for k, v := range map[string]string{"t": e.T, "w": e.W, "prev": e.Prev, "rel": e.Rel, "s": e.S} {
		if v == "" {
			return nil, fmt.Errorf("%w: %s", ErrMissing, k)
		}
	}
	var b bytes.Buffer
	b.WriteByte('{')
	put := func(k string, v any) error {
		if b.Len() > 1 {
			b.WriteByte(',')
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return err
		}
		b.Write(kb)
		b.WriteByte(':')
		vb, err := marshalValue(v)
		if err != nil {
			return fmt.Errorf("canon: %s: %w", k, err)
		}
		b.Write(vb)
		return nil
	}
	if err := put("seq", e.Seq); err != nil {
		return nil, err
	}
	for _, kv := range [][2]string{{"t", e.T}, {"w", e.W}, {"prev", e.Prev}, {"rel", e.Rel}, {"s", e.S}} {
		if err := put(kv[0], kv[1]); err != nil {
			return nil, err
		}
	}
	if e.O != "" {
		if err := put("o", e.O); err != nil {
			return nil, err
		}
	}
	if len(e.A) > 0 {
		if err := put("a", e.A); err != nil {
			return nil, err
		}
	}
	if withSig {
		if e.Sig == "" {
			return nil, fmt.Errorf("%w: sig", ErrMissing)
		}
		if err := put("sig", e.Sig); err != nil {
			return nil, err
		}
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// marshalValue encodes v with object keys sorted bytewise at every depth and
// no whitespace, so two attribute maps with the same content give the same
// bytes whatever order they were built in.
func marshalValue(v any) ([]byte, error) {
	switch x := v.(type) {
	case map[string]any:
		ks := make([]string, 0, len(x))
		for k := range x {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		var b bytes.Buffer
		b.WriteByte('{')
		for i, k := range ks {
			if i > 0 {
				b.WriteByte(',')
			}
			kb, err := json.Marshal(k)
			if err != nil {
				return nil, err
			}
			b.Write(kb)
			b.WriteByte(':')
			vb, err := marshalValue(x[k])
			if err != nil {
				return nil, err
			}
			b.Write(vb)
		}
		b.WriteByte('}')
		return b.Bytes(), nil
	case []any:
		var b bytes.Buffer
		b.WriteByte('[')
		for i, el := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			eb, err := marshalValue(el)
			if err != nil {
				return nil, err
			}
			b.Write(eb)
		}
		b.WriteByte(']')
		return b.Bytes(), nil
	default:
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(v); err != nil {
			return nil, err
		}
		return bytes.TrimRight(b.Bytes(), "\n"), nil
	}
}

// Hash is the identity of a byte sequence in the form SPEC §3.1 and §8.7
// use everywhere: "sha256:" and the lowercase hex of its SHA-256.
func Hash(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Parse reads one log line into an Edge. Keys may arrive in any order and
// with any whitespace; the canonical form is recovered by Canonical. A key
// §4.2 does not name is refused by name, a required key that is absent is
// refused by name, and `a` must be a flat object.
func Parse(line []byte) (Edge, error) {
	var raw map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return Edge{}, fmt.Errorf("canon: not a JSON object: %w", err)
	}
	known := map[string]bool{}
	for _, k := range keys {
		known[k] = true
	}
	for k := range raw {
		if !known[k] {
			return Edge{}, fmt.Errorf("%w: %q", ErrUnknownKey, k)
		}
	}
	var e Edge
	str := func(k string, dst *string, required bool) error {
		v, ok := raw[k]
		if !ok {
			if required {
				return fmt.Errorf("%w: %s", ErrMissing, k)
			}
			return nil
		}
		if err := json.Unmarshal(v, dst); err != nil {
			return fmt.Errorf("canon: %s: %w", k, err)
		}
		if required && *dst == "" {
			return fmt.Errorf("%w: %s is empty", ErrMissing, k)
		}
		return nil
	}
	v, ok := raw["seq"]
	if !ok {
		return Edge{}, fmt.Errorf("%w: seq", ErrMissing)
	}
	if err := json.Unmarshal(v, &e.Seq); err != nil || e.Seq == 0 {
		return Edge{}, fmt.Errorf("canon: seq must be a positive integer, got %s", string(v))
	}
	for _, kv := range []struct {
		k   string
		dst *string
		req bool
	}{{"t", &e.T, true}, {"w", &e.W, true}, {"prev", &e.Prev, true}, {"rel", &e.Rel, true}, {"s", &e.S, true}, {"o", &e.O, false}, {"sig", &e.Sig, false}} {
		if err := str(kv.k, kv.dst, kv.req); err != nil {
			return Edge{}, err
		}
	}
	if av, ok := raw["a"]; ok {
		var a map[string]any
		d := json.NewDecoder(bytes.NewReader(av))
		d.UseNumber()
		if err := d.Decode(&a); err != nil {
			return Edge{}, fmt.Errorf("canon: a must be an object: %w", err)
		}
		for k, x := range a {
			if _, nested := x.(map[string]any); nested {
				return Edge{}, fmt.Errorf("canon: a is not flat: %q holds an object", k)
			}
		}
		e.A = a
	}
	return e, nil
}
