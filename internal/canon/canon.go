// Package canon is the canonical form of an edge (SPEC §4.2, §8.3) and the
// hash over it (§8.7). Everything that signs, chains or compares edges goes
// through this package, so there is exactly one answer to "what are the
// bytes of this edge".
//
// Two forms (SPEC §4.2a): the signing form, without `sig`, and the written
// form, with it -- the line in the log and the chain's input, so that a
// forged signature breaks the chain. Both are the edge's JSON with keys in
// §4.2's order, the keys of `a` sorted bytewise, no whitespace, strings
// escaped exactly as §4.2a says and nothing else escaped, integers as plain
// decimal, and attribute values limited to strings, 64-bit integers,
// booleans, null and arrays of those; anything else is refused by name, as
// is a duplicate key.
package canon

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"unicode/utf8"
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

// ErrValue is returned for an attribute value SPEC §4.2a does not admit: a
// fractional or exponent number, a nested object, a number out of the
// signed 64-bit range, or a non-UTF-8 string.
var ErrValue = errors.New("canon: value not admitted")

// ErrDuplicateKey is returned by Parse for a key that appears twice.
var ErrDuplicateKey = errors.New("canon: duplicate key")

// ErrTrailing is returned by Parse for a line that holds more than one JSON
// value (§4.1: one edge per line).
var ErrTrailing = errors.New("canon: not one JSON value")

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
		kb, err := escapeString(k)
		if err != nil {
			return err
		}
		b.Write(kb)
		b.WriteByte(':')
		vb, err := marshalValue(v, 0)
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

// marshalValue encodes v by §4.2a: object keys sorted bytewise at every
// depth (only `a` itself is an object; a nested object is refused), strings
// escaped by escapeString, integers as decimal, booleans and null as
// written, arrays of those. Depth 0 is `a`; a map below it is refused.
func marshalValue(v any, depth int) ([]byte, error) {
	switch x := v.(type) {
	case map[string]any:
		if depth > 0 {
			return nil, fmt.Errorf("%w: a nested object", ErrValue)
		}
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
			kb, err := escapeString(k)
			if err != nil {
				return nil, err
			}
			b.Write(kb)
			b.WriteByte(':')
			vb, err := marshalValue(x[k], depth+1)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", k, err)
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
			eb, err := marshalValue(el, depth+1)
			if err != nil {
				return nil, err
			}
			b.Write(eb)
		}
		b.WriteByte(']')
		return b.Bytes(), nil
	case string:
		return escapeString(x)
	case bool:
		if x {
			return []byte("true"), nil
		}
		return []byte("false"), nil
	case nil:
		return []byte("null"), nil
	case int:
		return []byte(strconv.FormatInt(int64(x), 10)), nil
	case int64:
		return []byte(strconv.FormatInt(x, 10)), nil
	case uint64:
		if x > math.MaxInt64 {
			return nil, fmt.Errorf("%w: %d is above the signed 64-bit range", ErrValue, x)
		}
		return []byte(strconv.FormatUint(x, 10)), nil
	case json.Number:
		// re-formatted, never echoed: `-0` and `+0` are the integer 0 and are
		// written as `0` (quill, coop main/998 finding 6)
		n, err := strconv.ParseInt(string(x), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%w: number %s is not a 64-bit integer written as decimal digits", ErrValue, x)
		}
		return []byte(strconv.FormatInt(n, 10)), nil
	case float64:
		if x != math.Trunc(x) || math.Abs(x) > math.MaxInt64 {
			return nil, fmt.Errorf("%w: number %v is not a 64-bit integer", ErrValue, x)
		}
		return []byte(strconv.FormatInt(int64(x), 10)), nil
	default:
		return nil, fmt.Errorf("%w: a %T", ErrValue, v)
	}
}

// EscapeString is escapeString for the vector file: the one string encoding.
func EscapeString(s string) string {
	b, err := escapeString(s)
	if err != nil {
		return ""
	}
	return string(b)
}

// escapeString writes a JSON string by §4.2a: `"` and `\\` escaped; the
// five controls with short forms (backspace, form feed, newline, carriage
// return, tab) as \b \f \n \r \t; every other control below U+0020 as
// \u00xx with lowercase hex; everything else, non-ASCII included, written
// raw. This is exactly what jq -c writes, so the shell implementation agrees
// byte for byte (found by the archival script, D-25). A string that is not
// valid UTF-8 is refused by name.
func escapeString(s string) ([]byte, error) {
	if !utf8.ValidString(s) {
		return nil, fmt.Errorf("%w: a string that is not UTF-8", ErrValue)
	}
	var b bytes.Buffer
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.Bytes(), nil
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
	if err := refuseDuplicateKeys(line); err != nil {
		return Edge{}, err
	}
	var raw map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return Edge{}, fmt.Errorf("canon: not a JSON object: %w", err)
	}
	// one edge per line (§4.1): anything after the object is refused by name
	// (quill, coop main/998 finding 7)
	if _, err := dec.Token(); err != io.EOF {
		return Edge{}, fmt.Errorf("%w: bytes after the edge's object on the line", ErrTrailing)
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
		// every value must be one §4.2a admits; marshalValue is the judge
		if _, err := marshalValue(a, 0); err != nil {
			return Edge{}, fmt.Errorf("canon: a: %w", err)
		}
		e.A = a
	}
	return e, nil
}

// refuseDuplicateKeys walks the tokens of a JSON text and refuses by name a
// key that appears twice in one object, at any depth, which encoding/json
// would otherwise resolve silently by taking the last.
func refuseDuplicateKeys(line []byte) error {
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.UseNumber()
	type frame struct {
		object    bool
		seen      map[string]bool
		expectKey bool
	}
	var stack []*frame
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return nil // the decoder that follows reports the syntax error
		}
		switch t := tok.(type) {
		case json.Delim:
			switch t {
			case '{':
				stack = append(stack, &frame{object: true, seen: map[string]bool{}, expectKey: true})
			case '[':
				stack = append(stack, &frame{})
			case '}', ']':
				stack = stack[:len(stack)-1]
				if len(stack) > 0 && stack[len(stack)-1].object {
					stack[len(stack)-1].expectKey = true
				}
			}
		default:
			if len(stack) == 0 {
				continue
			}
			top := stack[len(stack)-1]
			if top.object && top.expectKey {
				k := t.(string)
				if top.seen[k] {
					return fmt.Errorf("%w: %q", ErrDuplicateKey, k)
				}
				top.seen[k] = true
				top.expectKey = false
			} else if top.object {
				top.expectKey = true
			}
		}
	}
}
