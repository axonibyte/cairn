package store

import (
	"io"

	"forge.axonibyte.com/axonibyte/cairn/internal/canon"
	"forge.axonibyte.com/axonibyte/cairn/internal/contract"
	"forge.axonibyte.com/axonibyte/cairn/internal/log"
)

// The file backend is §6.1's reference Store. The assertion is the contract
// test: a method lost here fails to compile, not to run.
var _ contract.Store = (*Store)(nil)

// PutBlob is Put without the edge seq: the identity, idempotent (§6.1). The
// `wrote` edge of §5.2 is still recorded on a blob's first appearance.
func (s *Store) PutBlob(r io.Reader) (string, error) {
	id, _, err := s.Put(r)
	return id, err
}

// GetBlob reads the blob, through its manifest for a large one; the bytes
// are verified against the identity before return (blob.ErrHashMismatch).
func (s *Store) GetBlob(identity string) ([]byte, error) {
	return s.Blobs.Get(identity)
}

// Append appends an already signed edge and seals when the segment rolls.
// The log refuses a non-contiguous seq, a wrong prev and an invalid
// signature by name (§6.1); a machine with no sealing key refuses before
// anything is written, because a roll owes a seal.
func (s *Store) Append(e canon.Edge) (uint64, error) {
	if s.sealing == nil {
		return 0, ErrNoKey
	}
	return s.Log.Append(e, s.sealing)
}

// ReadLog verifies the whole log from the founder and streams the edges
// from seq `from` on (§6.1). Verification is never partial: a reader asking
// from seq 50 still has the chain checked from 1.
func (s *Store) ReadLog(from uint64, fn func(log.Verified) error) error {
	_, err := log.Read(s.Root, s.Founder, s.Config.SegmentBytes, from, fn)
	return err
}

// Head is the seq of the last edge in the log.
func (s *Store) Head() uint64 { return s.Log.LastSeq() }
