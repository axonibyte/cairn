package blob

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newStore(t *testing.T) Store {
	t.Helper()
	return Store{Root: t.TempDir()}
}

// §3.1-3.2: the identity is sha256:<hex>, the path is blobs/<xx>/<rest>, and
// a second Put of the same bytes is the same identity and no second file.
func TestPutGetRoundTripAndLayout(t *testing.T) {
	s := newStore(t)
	data := []byte("a stone on the cairn")
	id, err := s.Put(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if id != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatalf("identity %s", id)
	}
	path, _ := s.Path(id)
	if want := filepath.Join(s.Root, "blobs", id[7:9], id[9:]); path != want {
		t.Fatalf("path %s, want %s", path, want)
	}
	got, err := s.Get(id)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("get: %v %q", err, got)
	}
	if id2, err := s.Put(bytes.NewReader(data)); err != nil || id2 != id {
		t.Fatalf("second put: %v %s", err, id2)
	}
	// nothing but the one blob under blobs/: no temporary file survives a Put
	var files []string
	filepath.Walk(filepath.Join(s.Root, "blobs"), func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	if len(files) != 1 || files[0] != path {
		t.Fatalf("files under blobs/: %v", files)
	}
	// and the report says so: created on the first appearance only
	if _, created, _ := s.PutReport(bytes.NewReader(data)); created {
		t.Fatal("a second put reported created")
	}
	if _, created, _ := s.PutReport(strings.NewReader("a new stone")); !created {
		t.Fatal("a first put did not report created")
	}
}

// §3.2: a blob whose bytes do not hash to its name is refused by name, and
// the refusal names the identity and the path.
func TestCorruptBlobIsRefusedByName(t *testing.T) {
	s := newStore(t)
	id, err := s.Put(strings.NewReader("honest bytes"))
	if err != nil {
		t.Fatal(err)
	}
	path, _ := s.Path(id)
	if err := os.WriteFile(path, []byte("honest bytez"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = s.Get(id)
	if !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("got %v, want hash mismatch", err)
	}
	if !strings.Contains(err.Error(), id) || !strings.Contains(err.Error(), path) {
		t.Fatalf("the refusal does not name the identity and the path: %v", err)
	}
	if _, err := s.Get("sha256:" + strings.Repeat("0", 64)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("absent blob: got %v", err)
	}
	for _, bad := range []string{"sha256:abc", "md5:" + strings.Repeat("0", 32), "sha256:" + strings.Repeat("G", 64), "sha256:" + strings.Repeat("A", 64)} {
		if _, err := s.Get(bad); !errors.Is(err, ErrBadIdentity) {
			t.Fatalf("%q: got %v, want bad identity", bad, err)
		}
	}
}

// §3.3-3.4: at the bound a blob is one file; one byte over it is a manifest
// whose chunks are blobs, the manifest's keys in the specified order, and the
// large blob comes back byte-identical through Get.
func TestManifestAtTheBoundAndOneOver(t *testing.T) {
	s := Store{Root: t.TempDir(), Bound: 1000, ChunkSize: 300}
	at := bytes.Repeat([]byte("x"), 1000)
	id, err := s.Put(bytes.NewReader(at))
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := s.IsManifest(id); m {
		t.Fatal("a blob at the bound must not be a manifest")
	}
	over := make([]byte, 1001)
	rand.New(rand.NewSource(1)).Read(over)
	mid, err := s.Put(bytes.NewReader(over))
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := s.IsManifest(mid); !m {
		t.Fatal("a blob one over the bound must be a manifest")
	}
	raw, _ := s.GetRaw(mid)
	if mp, _ := s.Path(mid); !fileExists(mp+".manifest") || fileExists(mp) {
		t.Fatalf("a manifest lives at <path>.manifest and nowhere else (D-26)")
	}
	if !strings.HasPrefix(string(raw), `{"cairn":"manifest","v":1,"size":1001,"chunks":["sha256:`) {
		t.Fatalf("manifest form: %s", raw)
	}
	if n := strings.Count(string(raw), "sha256:"); n != 4 {
		t.Fatalf("1001 bytes in chunks of 300 is 4 chunks, manifest lists %d", n)
	}
	got, err := s.Get(mid)
	if err != nil || !bytes.Equal(got, over) {
		t.Fatalf("large blob round trip: %v, equal %v", err, bytes.Equal(got, over))
	}
	// a chunk corrupted is refused by name through the manifest
	chunkID := strings.Split(strings.Split(string(raw), `["`)[1], `"`)[0]
	cp, _ := s.Path(chunkID)
	os.WriteFile(cp, []byte("not the chunk"), 0o644)
	if _, err := s.Get(mid); !errors.Is(err, ErrHashMismatch) || !strings.Contains(err.Error(), mid) {
		t.Fatalf("corrupt chunk: %v", err)
	}
}

// ROADMAP M1: a manifest round-trips a 40 MiB blob byte-identically under
// the default bound and chunk size (10 chunks of 4 MiB).
func TestFortyMiBRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("40 MiB round trip skipped under -short")
	}
	s := newStore(t)
	big := make([]byte, 40<<20)
	rand.New(rand.NewSource(40)).Read(big)
	id, err := s.Put(bytes.NewReader(big))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := s.GetRaw(id)
	if n := strings.Count(string(raw), "sha256:"); n != 10 {
		t.Fatalf("expected 10 chunks, manifest lists %d", n)
	}
	got, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, big) {
		t.Fatal("40 MiB blob did not round-trip byte-identically")
	}
}

// §3.2's temp-and-rename is only a guarantee if a rename that fails is an
// error and leaves nothing behind. A non-empty directory sitting at the
// blob's path makes the rename fail (it is not a verified blob, so Put does
// not take it for one); Put must say so, the directory must hold no
// temporary file afterwards, and nothing claims the blob is stored.
// Mutant: ignore the rename's error -- Put then returns the identity of a
// blob that is not there, and this arm fails.
func TestRenameFailureIsAnErrorAndLeavesNothing(t *testing.T) {
	s := newStore(t)
	data := []byte("a stone that cannot be placed")
	h := sha256.Sum256(data)
	id := "sha256:" + hex.EncodeToString(h[:])
	path, _ := s.Path(id)
	if err := os.MkdirAll(filepath.Join(path, "occupied"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(bytes.NewReader(data)); err == nil {
		t.Fatal("Put over a directory at the blob's path must fail")
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Fatalf("a temporary file was left behind: %s", e.Name())
		}
	}
	if _, err := s.Get(id); err == nil {
		t.Fatal("the blob must not read as stored")
	}
}

// Put is idempotent on a verified blob and self-healing on a corrupt one: a
// file under the name whose bytes do not hash to it is rewritten, so a Put
// after a disk fault leaves the store honest (§3.2 read side, §6.1
// idempotence).
func TestPutRewritesACorruptFile(t *testing.T) {
	s := newStore(t)
	data := []byte("the honest bytes")
	id, err := s.Put(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	path, _ := s.Path(id)
	os.WriteFile(path, []byte("rotten"), 0o644)
	if _, err := s.Get(id); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("control: the corruption must be visible first, got %v", err)
	}
	if id2, err := s.Put(bytes.NewReader(data)); err != nil || id2 != id {
		t.Fatalf("put over corrupt: %v %s", err, id2)
	}
	if got, err := s.Get(id); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("after the rewrite: %v %q", err, got)
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// D-26 (quill, coop main/993 finding 3): a blob whose bytes are a valid
// manifest is a blob. Get(Put(x)) == x and the bytes hash to the identity.
func TestManifestShapedUserBytesAreABlob(t *testing.T) {
	s := newStore(t)
	inner, _ := s.Put(strings.NewReader("the chunk a forged manifest would point at"))
	user := []byte(`{"cairn":"manifest","v":1,"size":42,"chunks":["` + inner + `"]}`)
	id, err := s.Put(bytes.NewReader(user))
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, user) {
		t.Fatalf("Get(Put(x)) != x: got %q", got)
	}
	if m, _ := s.IsManifest(id); m {
		t.Fatal("a user's manifest-shaped bytes read as a manifest")
	}
}

// D-26 (finding 4): a manifest larger than the bound is still one manifest
// file, since the manifest is exempt from the bound, and the large blob
// round-trips through it.
func TestManifestAboveTheBoundIsStillOneManifest(t *testing.T) {
	s := Store{Root: t.TempDir(), Bound: 200, ChunkSize: 50}
	big := make([]byte, 5000)
	rand.New(rand.NewSource(5)).Read(big)
	id, err := s.Put(bytes.NewReader(big))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := s.GetRaw(id)
	if len(raw) <= 200 {
		t.Fatalf("the arm needs a manifest over the bound; this one is %d bytes", len(raw))
	}
	if m, _ := s.IsManifest(id); !m {
		t.Fatal("the manifest must be stored as a manifest")
	}
	if got, err := s.Get(id); err != nil || !bytes.Equal(got, big) {
		t.Fatalf("round trip: %v", err)
	}
}

// §3.3: a manifest whose chunks do not resolve to its stated size is refused
// by name (quill's surviving mutant, main/993). The manifest is rewritten
// with a wrong size under its own hash so every chunk still verifies.
func TestManifestWithWrongSizeIsRefused(t *testing.T) {
	s := Store{Root: t.TempDir(), Bound: 100, ChunkSize: 40}
	data := bytes.Repeat([]byte("y"), 150)
	id, _ := s.Put(bytes.NewReader(data))
	raw, _ := s.GetRaw(id)
	forged := []byte(strings.Replace(string(raw), `"size":150`, `"size":149`, 1))
	fid := "sha256:" + func() string { h := sha256.Sum256(forged); return hex.EncodeToString(h[:]) }()
	fp, _ := s.Path(fid)
	os.MkdirAll(filepath.Dir(fp), 0o755)
	os.WriteFile(fp+".manifest", forged, 0o644)
	_, err := s.Get(fid)
	if err == nil || !strings.Contains(err.Error(), "says size 149, chunks hold 150") {
		t.Fatalf("wrong size: %v", err)
	}
}
