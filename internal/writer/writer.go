// Package writer is a signing key with a suite (SPEC §8.0-8.3): its id, its
// signature over an edge's signing form, and verification. Two suites in
// 0.1: ed25519, the per-edge default, and mldsa65 (ML-DSA-65, FIPS 204), the
// suite of the seals. An identifier this package does not know is refused
// by name, never guessed (§8.0).
//
// Encodings (D-24): a key id is "k:<suite>:" + lowercase hex SHA-256 of the
// public key's bytes; a signature is "<suite>:" + lowercase hex of the raw
// signature; a public key travels as "<suite>:" + lowercase hex of its raw
// bytes. Hex, not base64, so that a reader with a shell and no library can
// still tell two keys apart and a vector file is unambiguous.
package writer

import (
	"crypto/ed25519"
	"crypto/mldsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Suite identifiers of 0.1 (SPEC §8.1).
const (
	Ed25519 = "ed25519"
	MLDSA65 = "mldsa65"
)

// ErrUnknownSuite is returned for a suite identifier 0.1 does not name.
var ErrUnknownSuite = errors.New("writer: unknown suite")

// ErrBadSignature is returned when a signature does not verify.
var ErrBadSignature = errors.New("writer: signature does not verify")

// ErrMalformed is returned for a key or signature string not of the form
// "<suite>:<hex>".
var ErrMalformed = errors.New("writer: malformed")

// Key is a signing key pair of one suite.
type Key struct {
	Suite string
	ed    ed25519.PrivateKey
	ml    *mldsa.PrivateKey
}

// Public is a verifying key of one suite.
type Public struct {
	Suite string
	raw   []byte
}

// Generate makes a new key pair of the suite from the system's randomness.
func Generate(suite string) (*Key, error) {
	switch suite {
	case Ed25519:
		_, sk, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		return &Key{Suite: suite, ed: sk}, nil
	case MLDSA65:
		sk, err := mldsa.GenerateKey(mldsa.MLDSA65())
		if err != nil {
			return nil, err
		}
		return &Key{Suite: suite, ml: sk}, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownSuite, suite)
	}
}

// FromSeed rebuilds a key pair from its seed: 32 bytes for either suite
// (ed25519's seed; ML-DSA's xi). The seed is the secret a key file holds.
func FromSeed(suite string, seed []byte) (*Key, error) {
	switch suite {
	case Ed25519:
		if len(seed) != ed25519.SeedSize {
			return nil, fmt.Errorf("%w: ed25519 seed must be %d bytes, got %d", ErrMalformed, ed25519.SeedSize, len(seed))
		}
		return &Key{Suite: suite, ed: ed25519.NewKeyFromSeed(seed)}, nil
	case MLDSA65:
		sk, err := mldsa.NewPrivateKey(mldsa.MLDSA65(), seed)
		if err != nil {
			return nil, fmt.Errorf("%w: mldsa65 seed: %v", ErrMalformed, err)
		}
		return &Key{Suite: suite, ml: sk}, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownSuite, suite)
	}
}

// Seed returns the 32-byte secret the key is rebuilt from.
func (k *Key) Seed() []byte {
	switch k.Suite {
	case Ed25519:
		return append([]byte(nil), k.ed.Seed()...)
	case MLDSA65:
		return append([]byte(nil), k.ml.Bytes()...)
	}
	return nil
}

// Public returns the verifying half.
func (k *Key) Public() Public {
	switch k.Suite {
	case Ed25519:
		return Public{Suite: k.Suite, raw: append([]byte(nil), k.ed.Public().(ed25519.PublicKey)...)}
	case MLDSA65:
		return Public{Suite: k.Suite, raw: append([]byte(nil), k.ml.PublicKey().Bytes()...)}
	}
	return Public{}
}

// ID is the writer's id, "k:<suite>:<hex sha256 of the public key>" (§8.1).
func (k *Key) ID() string { return k.Public().ID() }

// ID is "k:<suite>:<hex sha256 of the public key bytes>".
func (p Public) ID() string {
	sum := sha256.Sum256(p.raw)
	return "k:" + p.Suite + ":" + hex.EncodeToString(sum[:])
}

// String is the public key's transport form, "<suite>:<hex>".
func (p Public) String() string { return p.Suite + ":" + hex.EncodeToString(p.raw) }

// ParsePublic reads a public key from its transport form; an unknown suite
// or a key of the wrong length is refused by name.
func ParsePublic(s string) (Public, error) {
	suite, hx, ok := strings.Cut(s, ":")
	if !ok {
		return Public{}, fmt.Errorf("%w: public key %q", ErrMalformed, s)
	}
	raw, err := hex.DecodeString(hx)
	if err != nil || strings.ToLower(hx) != hx {
		return Public{}, fmt.Errorf("%w: public key hex", ErrMalformed)
	}
	switch suite {
	case Ed25519:
		if len(raw) != ed25519.PublicKeySize {
			return Public{}, fmt.Errorf("%w: ed25519 public key must be %d bytes, got %d", ErrMalformed, ed25519.PublicKeySize, len(raw))
		}
	case MLDSA65:
		if _, err := mldsa.NewPublicKey(mldsa.MLDSA65(), raw); err != nil {
			return Public{}, fmt.Errorf("%w: mldsa65 public key: %v", ErrMalformed, err)
		}
	default:
		return Public{}, fmt.Errorf("%w: %q", ErrUnknownSuite, suite)
	}
	return Public{Suite: suite, raw: raw}, nil
}

// Sign signs a message (an edge's signing form, or a chain hash for a seal)
// and returns "<suite>:<hex>". ML-DSA signing is deterministic here so that
// a vector file can hold expected signatures for both suites.
func (k *Key) Sign(message []byte) (string, error) {
	switch k.Suite {
	case Ed25519:
		return k.Suite + ":" + hex.EncodeToString(ed25519.Sign(k.ed, message)), nil
	case MLDSA65:
		sig, err := k.ml.SignDeterministic(message, &mldsa.Options{})
		if err != nil {
			return "", err
		}
		return k.Suite + ":" + hex.EncodeToString(sig), nil
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownSuite, k.Suite)
}

// Verify checks a signature string against the message under this public
// key. A signature whose suite is not this key's suite is refused by name;
// a signature that does not verify is ErrBadSignature.
func (p Public) Verify(message []byte, signature string) error {
	suite, hx, ok := strings.Cut(signature, ":")
	if !ok {
		return fmt.Errorf("%w: signature %q", ErrMalformed, signature)
	}
	if suite != Ed25519 && suite != MLDSA65 {
		return fmt.Errorf("%w: %q", ErrUnknownSuite, suite)
	}
	if suite != p.Suite {
		return fmt.Errorf("%w: signature is %s, key is %s", ErrBadSignature, suite, p.Suite)
	}
	// lowercase hex is the one encoding (D-24): an uppercase signature would
	// verify and yet be a different written form with a different chain hash,
	// two valid lines from one signing act, so it is refused before decoding
	sig, err := hex.DecodeString(hx)
	if err != nil || strings.ToLower(hx) != hx {
		return fmt.Errorf("%w: signature hex must be lowercase", ErrMalformed)
	}
	switch suite {
	case Ed25519:
		if !ed25519.Verify(ed25519.PublicKey(p.raw), message, sig) {
			return ErrBadSignature
		}
		return nil
	case MLDSA65:
		pk, err := mldsa.NewPublicKey(mldsa.MLDSA65(), p.raw)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrMalformed, err)
		}
		if err := mldsa.Verify(pk, message, sig, &mldsa.Options{}); err != nil {
			return ErrBadSignature
		}
		return nil
	}
	return fmt.Errorf("%w: %q", ErrUnknownSuite, suite)
}
