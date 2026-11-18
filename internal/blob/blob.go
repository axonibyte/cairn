// Package blob is the file backend's blob store (SPEC §3): content-addressed
// bytes at blobs/<xx>/<rest>, written to a temporary name and renamed into
// place, verified against their name on every read, and chunked into a
// manifest above a bound. A manifest lives at blobs/<xx>/<rest>.manifest
// and is told from a blob by that name and never by its bytes (D-26): a
// blob whose bytes look like a manifest is a blob.
package blob

import (
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
// bound the bytes are stored as chunks and a manifest file, and the
// manifest's identity -- the hash of the manifest's own bytes -- is returned
// (§3.3). Idempotent: a blob already present is not rewritten.
func (s Store) Put(r io.Reader) (string, error) {
	id, _, err := s.PutReport(r)
	return id, err
}

// PutReport is Put that also reports whether anything new was stored:
// created is false when the identity (and, for a large blob, its manifest)
// was already here verified. A caller that records a blob's first
// appearance (§5.2's `wrote`) needs that bit.
func (s Store) PutReport(r io.Reader) (identity string, created bool, err error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", false, err
	}
	if int64(len(data)) <= s.bound() {
		return s.putOne(data, "")
	}
	m := Manifest{Cairn: "manifest", V: 1, Size: int64(len(data))}
	for off := int64(0); off < int64(len(data)); off += s.chunk() {
		end := off + s.chunk()
		if end > int64(len(data)) {
			end = int64(len(data))
		}
		id, _, err := s.putOne(data[off:end], "")
		if err != nil {
			return "", false, err
		}
		m.Chunks = append(m.Chunks, id)
	}
	mb, err := json.Marshal(m)
	if err != nil {
		return "", false, err
	}
	return s.putOne(mb, manifestSuffix)
}

const manifestSuffix = ".manifest"

func (s Store) putOne(data []byte, suffix string) (string, bool, error) {
	id := canon.Hash(data)
	path, err := s.Path(id)
	if err != nil {
		return "", false, err
	}
	path += suffix
	// idempotent, and self-healing: a verified file under the name is the
	// blob; anything else under the name (a corrupt file, a directory) is
	// not, and the write goes ahead -- the rename then succeeds over a file
	// and fails, as an error, over a directory
	if existing, err := os.ReadFile(path); err == nil && canon.Hash(existing) == id {
		return id, false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", false, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return "", false, err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", false, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", false, err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", false, err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return "", false, err
	}
	return id, true, nil
}

// Get returns the bytes of the identity, verified: a file whose bytes do not
// hash to its name is refused by name (§3.2). The identity is looked up as a
// blob first and as a manifest second, by file name; a manifest is followed
// and the large blob's bytes are returned whole, and a manifest whose
// chunks do not resolve to its stated size is refused by name.
func (s Store) Get(identity string) ([]byte, error) {
	data, isManifest, err := s.read(identity)
	if err != nil {
		return nil, err
	}
	if !isManifest {
		return data, nil
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil || m.Cairn != "manifest" || m.V != 1 {
		return nil, fmt.Errorf("blob: %s%s is not a manifest of §3.4", identity, manifestSuffix)
	}
	out := make([]byte, 0, m.Size)
	for _, c := range m.Chunks {
		cb, isM, err := s.read(c)
		if err != nil {
			return nil, fmt.Errorf("blob: manifest %s: chunk %w", identity, err)
		}
		if isM {
			return nil, fmt.Errorf("blob: manifest %s: chunk %s is itself a manifest", identity, c)
		}
		out = append(out, cb...)
	}
	if int64(len(out)) != m.Size {
		return nil, fmt.Errorf("blob: manifest %s says size %d, chunks hold %d", identity, m.Size, len(out))
	}
	return out, nil
}

// GetRaw returns the bytes stored under the identity exactly, verified: a
// blob's bytes, or a manifest's own bytes.
func (s Store) GetRaw(identity string) ([]byte, error) {
	data, _, err := s.read(identity)
	return data, err
}

// IsManifest reports whether the identity is stored as a manifest, by its
// file name (D-26).
func (s Store) IsManifest(identity string) (bool, error) {
	_, isM, err := s.read(identity)
	return isM, err
}

// read finds the identity as a blob or, failing that, as a manifest, and
// verifies the bytes against the identity either way.
func (s Store) read(identity string) ([]byte, bool, error) {
	path, err := s.Path(identity)
	if err != nil {
		return nil, false, err
	}
	data, err := os.ReadFile(path)
	isManifest := false
	if errors.Is(err, os.ErrNotExist) {
		data, err = os.ReadFile(path + manifestSuffix)
		isManifest = true
		path += manifestSuffix
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, fmt.Errorf("%w: %s", ErrNotFound, identity)
	}
	if err != nil {
		return nil, false, err
	}
	sum := sha256.Sum256(data)
	if "sha256:"+hex.EncodeToString(sum[:]) != identity {
		return nil, false, fmt.Errorf("%w: %s at %s", ErrHashMismatch, identity, path)
	}
	return data, isManifest, nil
}
