// Package blob is the file backend's blob store (SPEC §3): content-addressed
// bytes at blobs/<xx>/<rest>, written to a temporary name and renamed into
// place, verified against their name on every read, and chunked into a
// manifest above a bound.
package blob

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"forge.axonibyte.com/axonibyte/cairn/internal/canon"
)

// Defaults of SPEC §3.3; an instance may set both.
const (
	DefaultBound     = 8 << 20
	DefaultChunkSize = 4 << 20
)

// ErrHashMismatch is returned when a blob's bytes do not hash to its name.
var ErrHashMismatch = errors.New("blob: bytes do not match identity")

// ErrNotFound is returned when no blob has the identity.
var ErrNotFound = errors.New("blob: not found")

// ErrBadIdentity is returned for an identity not of the form sha256:<64 hex>.
var ErrBadIdentity = errors.New("blob: malformed identity")

// Store is a blob store rooted at a directory. Zero values of Bound and
// ChunkSize mean the defaults.
type Store struct {
	Root      string
	Bound     int64
	ChunkSize int64
}

// Manifest is the format of SPEC §3.4. Keys are emitted in this order.
type Manifest struct {
	Cairn  string   `json:"cairn"`
	V      int      `json:"v"`
	Size   int64    `json:"size"`
	Chunks []string `json:"chunks"`
}

func (s Store) bound() int64 {
	if s.Bound > 0 {
		return s.Bound
	}
	return DefaultBound
}

func (s Store) chunk() int64 {
	if s.ChunkSize > 0 {
		return s.ChunkSize
	}
	return DefaultChunkSize
}

// Path is where a blob of the identity lives: blobs/<first two hex>/<rest>.
func (s Store) Path(identity string) (string, error) {
	hexpart, ok := strings.CutPrefix(identity, "sha256:")
	if !ok || len(hexpart) != 64 {
		return "", fmt.Errorf("%w: %q", ErrBadIdentity, identity)
	}
	if _, err := hex.DecodeString(hexpart); err != nil || strings.ToLower(hexpart) != hexpart {
		return "", fmt.Errorf("%w: %q", ErrBadIdentity, identity)
	}
	return filepath.Join(s.Root, "blobs", hexpart[:2], hexpart[2:]), nil
}

// Put stores the bytes read from r and returns their identity. Above the
// bound the bytes are stored as chunks and a manifest, and the manifest's
// identity is returned (§3.3). Idempotent: a blob already present is not
// rewritten.
func (s Store) Put(r io.Reader) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	if int64(len(data)) <= s.bound() {
		return s.putOne(data)
	}
	m := Manifest{Cairn: "manifest", V: 1, Size: int64(len(data))}
	for off := int64(0); off < int64(len(data)); off += s.chunk() {
		end := off + s.chunk()
		if end > int64(len(data)) {
			end = int64(len(data))
		}
		id, err := s.putOne(data[off:end])
		if err != nil {
			return "", err
		}
		m.Chunks = append(m.Chunks, id)
	}
	mb, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return s.putOne(mb)
}

func (s Store) putOne(data []byte) (string, error) {
	id := canon.Hash(data)
	path, err := s.Path(id)
	if err != nil {
		return "", err
	}
	// idempotent, and self-healing: a verified file under the name is the
	// blob; anything else under the name (a corrupt file, a directory) is
	// not, and the write goes ahead -- the rename then succeeds over a file
	// and fails, as an error, over a directory
	if existing, err := os.ReadFile(path); err == nil && canon.Hash(existing) == id {
		return id, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return "", err
	}
	return id, nil
}

// Get returns the bytes of the identity, verified: a file whose bytes do not
// hash to its name is refused by name (§3.2). A manifest is followed and the
// large blob's bytes are returned whole; the manifest itself is readable
// with GetRaw.
func (s Store) Get(identity string) ([]byte, error) {
	data, err := s.GetRaw(identity)
	if err != nil {
		return nil, err
	}
	m, ok := asManifest(data)
	if !ok {
		return data, nil
	}
	out := make([]byte, 0, m.Size)
	for _, c := range m.Chunks {
		cb, err := s.GetRaw(c)
		if err != nil {
			return nil, fmt.Errorf("blob: manifest %s: chunk %w", identity, err)
		}
		out = append(out, cb...)
	}
	if int64(len(out)) != m.Size {
		return nil, fmt.Errorf("blob: manifest %s says size %d, chunks hold %d", identity, m.Size, len(out))
	}
	return out, nil
}

// GetRaw returns the bytes stored under the identity exactly, verified, with
// no manifest following.
func (s Store) GetRaw(identity string) ([]byte, error) {
	path, err := s.Path(identity)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, identity)
	}
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if "sha256:"+hex.EncodeToString(sum[:]) != identity {
		return nil, fmt.Errorf("%w: %s at %s", ErrHashMismatch, identity, path)
	}
	return data, nil
}

// IsManifest reports whether the verified bytes of the identity are a
// manifest of §3.4.
func (s Store) IsManifest(identity string) (bool, error) {
	data, err := s.GetRaw(identity)
	if err != nil {
		return false, err
	}
	_, ok := asManifest(data)
	return ok, nil
}

func asManifest(data []byte) (Manifest, bool) {
	if !bytes.HasPrefix(data, []byte(`{"cairn":"manifest"`)) {
		return Manifest{}, false
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil || m.Cairn != "manifest" || m.V != 1 {
		return Manifest{}, false
	}
	return m, true
}
