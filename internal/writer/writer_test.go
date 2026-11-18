package writer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// §8.1: a writer's id is k:<suite>:<64 hex>; both suites generate, sign and
// verify; the sizes are the ones the spec states (64 and 3,309 bytes).
func TestBothSuitesSignAndVerify(t *testing.T) {
	for _, suite := range []string{Ed25519, MLDSA65} {
		k, err := Generate(suite)
		if err != nil {
			t.Fatal(err)
		}
		id := k.ID()
		if !strings.HasPrefix(id, "k:"+suite+":") || len(id) != len("k:"+suite+":")+64 {
			t.Fatalf("%s id %q", suite, id)
		}
		// and it is the SHA-256 of exactly the public key's bytes (§8.1), checked
		// from the transport form with the standard library, not from the code
		pubHex := strings.TrimPrefix(k.Public().String(), suite+":")
		pubRaw, err := hex.DecodeString(pubHex)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(pubRaw)
		if id != "k:"+suite+":"+hex.EncodeToString(sum[:]) {
			t.Fatalf("%s id is not the hash of the public key bytes", suite)
		}
		msg := []byte(`{"seq":1,"t":"2026-11-18T09:00:00.000Z"}`)
		sig, err := k.Sign(msg)
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]int{Ed25519: 64, MLDSA65: 3309}[suite]
		if got := (len(sig) - len(suite) - 1) / 2; got != want {
			t.Fatalf("%s signature is %d bytes, spec says %d", suite, got, want)
		}
		if err := k.Public().Verify(msg, sig); err != nil {
			t.Fatalf("%s verify: %v", suite, err)
		}
		// a changed message and a changed signature both fail by name
		if err := k.Public().Verify(append(msg, '!'), sig); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("%s altered message: %v", suite, err)
		}
		flipped := []byte(sig)
		last := flipped[len(flipped)-1]
		if last == '0' {
			flipped[len(flipped)-1] = '1'
		} else {
			flipped[len(flipped)-1] = '0'
		}
		if err := k.Public().Verify(msg, string(flipped)); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("%s altered signature: %v", suite, err)
		}
		// one encoding: the same signature in uppercase hex is refused by name,
		// since it would be a second written form of one signing act (quill,
		// coop main/995); the lowercase form is the control above
		upper := suite + ":" + strings.ToUpper(strings.TrimPrefix(sig, suite+":"))
		if err := k.Public().Verify(msg, upper); !errors.Is(err, ErrMalformed) || !strings.Contains(err.Error(), "lowercase") {
			t.Fatalf("%s uppercase signature: %v", suite, err)
		}
		// another key of the same suite does not verify it
		other, _ := Generate(suite)
		if err := other.Public().Verify(msg, sig); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("%s wrong key: %v", suite, err)
		}
	}
}

// §8.0: an unknown suite is refused by name, in generation, in a public key
// and in a signature; a signature of the other suite does not verify under
// this key.
func TestUnknownSuiteIsRefusedByName(t *testing.T) {
	if _, err := Generate("rsa2048"); !errors.Is(err, ErrUnknownSuite) || !strings.Contains(err.Error(), "rsa2048") {
		t.Fatalf("generate: %v", err)
	}
	if _, err := ParsePublic("rsa2048:00"); !errors.Is(err, ErrUnknownSuite) {
		t.Fatalf("parse: %v", err)
	}
	k, _ := Generate(Ed25519)
	if err := k.Public().Verify([]byte("m"), "sphincs:00"); !errors.Is(err, ErrUnknownSuite) {
		t.Fatalf("verify unknown suite: %v", err)
	}
	m, _ := Generate(MLDSA65)
	sig, _ := m.Sign([]byte("m"))
	if err := k.Public().Verify([]byte("m"), sig); !errors.Is(err, ErrBadSignature) || !strings.Contains(err.Error(), "mldsa65") {
		t.Fatalf("suite mismatch: %v", err)
	}
	for _, bad := range []string{"ed25519", "ed25519:zz", "ed25519:" + strings.Repeat("ab", 31)} {
		if _, err := ParsePublic(bad); !errors.Is(err, ErrMalformed) {
			t.Fatalf("%q: %v", bad, err)
		}
	}
}

// A key rebuilt from its seed is the same writer: same id, same public key,
// and a signature by one verifies under the other; ML-DSA signs
// deterministically so the same key gives the same bytes.
func TestSeedRoundTripAndPublicTransport(t *testing.T) {
	for _, suite := range []string{Ed25519, MLDSA65} {
		k, _ := Generate(suite)
		seed := k.Seed()
		if len(seed) != 32 {
			t.Fatalf("%s seed is %d bytes", suite, len(seed))
		}
		k2, err := FromSeed(suite, seed)
		if err != nil {
			t.Fatal(err)
		}
		if k2.ID() != k.ID() {
			t.Fatalf("%s: rebuilt key has another id", suite)
		}
		pub, err := ParsePublic(k.Public().String())
		if err != nil || pub.ID() != k.ID() {
			t.Fatalf("%s public transport: %v %s", suite, err, pub.ID())
		}
		msg := []byte("the same stone")
		s1, _ := k.Sign(msg)
		s2, _ := k2.Sign(msg)
		if suite == MLDSA65 && s1 != s2 {
			t.Fatal("mldsa65 signing must be deterministic for the vector file")
		}
		if err := pub.Verify(msg, s2); err != nil {
			t.Fatalf("%s: signature by the rebuilt key under the parsed public key: %v", suite, err)
		}
		if _, err := FromSeed(suite, seed[:31]); !errors.Is(err, ErrMalformed) {
			t.Fatalf("%s short seed: %v", suite, err)
		}
		if bytes.Equal(k.Public().raw, k2.Seed()) {
			t.Fatal("public and seed must differ")
		}
	}
}
