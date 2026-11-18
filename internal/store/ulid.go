package store

import (
	"crypto/rand"
	"time"
)

// Crockford's base32 alphabet, the ULID spec's.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewULID mints a ULID: 48 bits of Unix milliseconds and 80 bits of
// randomness, 26 characters of Crockford base32, sortable by time. The
// concept id of §4.2 is "c:" and one of these.
func NewULID() (string, error) {
	var b [16]byte
	ms := uint64(time.Now().UnixMilli())
	for i := 5; i >= 0; i-- {
		b[i] = byte(ms)
		ms >>= 8
	}
	if _, err := rand.Read(b[6:]); err != nil {
		return "", err
	}
	// 128 bits into 26 base32 characters, high bits first (the first character
	// carries 3 bits)
	out := make([]byte, 26)
	var acc uint64
	var nbits int
	j := 25
	for i := 15; i >= 0; i-- {
		acc |= uint64(b[i]) << nbits
		nbits += 8
		for nbits >= 5 && j >= 0 {
			out[j] = crockford[acc&31]
			acc >>= 5
			nbits -= 5
			j--
		}
	}
	for j >= 0 {
		out[j] = crockford[acc&31]
		acc >>= 5
		j--
	}
	return string(out), nil
}
