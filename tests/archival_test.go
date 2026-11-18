package tests

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"forge.axonibyte.com/axonibyte/cairn/internal/store"
	"forge.axonibyte.com/axonibyte/cairn/internal/writer"
)

// The archival script over a store with rolled segments and manifests
// (small instance settings), so that seals and chunked blobs are read by
// the shell implementation too; then a manifest whose stated size is wrong
// is refused by name, and a store whose cairn.json was swapped breaks at
// seq 1 under the script as it does under the log.
func TestTheArchivalScriptReadsSealsAndManifests(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Fatal("jq is required for the archival arm (tools/reaper-bootstrap.sh installs it)")
	}
	root := filepath.Join(t.TempDir(), "s")
	s, err := store.Init(root, 1000, 300, 1200)
	if err != nil {
		t.Fatal(err)
	}
	big := make([]byte, 3000) // ten distinct chunks under one manifest
	rand.New(rand.NewSource(3)).Read(big)
	bid, _, err := s.Put(bytes.NewReader(big))
	if err != nil {
		t.Fatal(err)
	}
	c, _, _ := s.NewConcept("the cairn")
	for i := 0; i < 12; i++ {
		if _, err := s.Edge("is-about", bid, c, map[string]any{"i": int64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if s.Log.LastSeal() == 0 {
		t.Fatal("the fixture needs a rolled, sealed segment")
	}
	script, _ := filepath.Abs("../tools/archive-check.sh")
	out, err := exec.Command("sh", script, root).CombinedOutput()
	if err != nil {
		t.Fatalf("archive-check: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "seals (last at seq") || strings.Contains(string(out), " 0 seals") || !strings.Contains(string(out), "11 blob files verified") {
		t.Fatalf("archive-check output:\n%s", out)
	}
	// a manifest whose size is wrong: rewritten under its own hash so the chunks still verify
	hx := strings.TrimPrefix(bid, "sha256:")
	mp := filepath.Join(root, "blobs", hx[:2], hx[2:]+".manifest")
	m, _ := os.ReadFile(mp)
	forged := bytes.Replace(m, []byte(`"size":3000`), []byte(`"size":2999`), 1)
	if bytes.Equal(forged, m) {
		t.Fatal("the manifest's size was not found")
	}
	os.WriteFile(mp, forged, 0o644)
	out, err = exec.Command("sh", script, root).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "bytes do not hash to the name") {
		t.Fatalf("forged manifest (its hash no longer matches its name): %v\n%s", err, out)
	}
	os.WriteFile(mp, m, 0o644)
	// a seal re-labeled as signed by the per-edge suite: the chain is whole
	// (it is the last line), the form is canonical, and only the suite rule
	// refuses it
	if _, err := s.Log.Seal(sealingKey(t, root)); err != nil {
		t.Fatal(err)
	}
	segs, _ := filepath.Glob(filepath.Join(root, "log", "*.jsonl"))
	last := segs[len(segs)-1]
	data, _ := os.ReadFile(last)
	ls := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if !strings.Contains(ls[len(ls)-1], `"rel":"seal"`) {
		t.Fatalf("the last line should be the on-demand seal: %s", ls[len(ls)-1])
	}
	ls[len(ls)-1] = strings.Replace(ls[len(ls)-1], `"sig":"mldsa65:`, `"sig":"ed25519:`, 1)
	os.WriteFile(last, []byte(strings.Join(ls, "\n")+"\n"), 0o644)
	out, err = exec.Command("sh", script, root).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "a seal must be signed by mldsa65") {
		t.Fatalf("re-labeled seal: %v\n%s", err, out)
	}
	os.WriteFile(last, data, 0o644)
	// a swapped configuration breaks the chain at seq 1
	cb, _ := os.ReadFile(filepath.Join(root, "cairn.json"))
	os.WriteFile(filepath.Join(root, "cairn.json"), bytes.Replace(cb, []byte(`"spec":"0.1"`), []byte(`"spec":"0.2"`), 1), 0o644)
	out, err = exec.Command("sh", script, root).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "seq 1: prev") {
		t.Fatalf("swapped configuration: %v\n%s", err, out)
	}
}

// M1 acceptance (ROADMAP): P-2 the archive and P-7 the chain read on a store
// of ten thousand edges -- built through the store, rolled into segments
// with seals, then verified by the shell implementation alone. The shell
// pays one sha256sum process per line, about two minutes for ten thousand,
// so this arm runs when CAIRN_ACCEPTANCE is set (a milestone's close, a
// clear that wants it) and its reading is recorded in the roadmap; the
// same store is verified by the Go reader in internal/log on every run.
func TestP2AndP7OnTenThousandEdges(t *testing.T) {
	if os.Getenv("CAIRN_ACCEPTANCE") == "" {
		t.Skip("set CAIRN_ACCEPTANCE=1 to run the ten-thousand-edge archival reading (about two minutes)")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Fatal("jq is required for the archival arm")
	}
	root := filepath.Join(t.TempDir(), "s")
	s, err := store.Init(root, 0, 0, 512<<10)
	if err != nil {
		t.Fatal(err)
	}
	c, _, _ := s.NewConcept("the cairn")
	b, _, _ := s.Put(bytes.NewReader([]byte("one stone")))
	for i := 0; i < 10000; i++ {
		if _, err := s.Edge("is-about", b, c, map[string]any{"i": int64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	n, seal, err := s.Verify()
	if err != nil || n < 10000 || seal == 0 {
		t.Fatalf("verify: %v %d %d", err, n, seal)
	}
	script, _ := filepath.Abs("../tools/archive-check.sh")
	start := time.Now()
	out, err := exec.Command("sh", script, root).CombinedOutput()
	if err != nil {
		t.Fatalf("archive-check over ten thousand edges: %v\n%s", err, out)
	}
	t.Logf("P-2/P-7 reading: %s in %.1fs", strings.TrimSpace(string(out)), time.Since(start).Seconds())
}

// sealingKey reopens the store to borrow its mldsa65 key for an on-demand
// seal; the store package keeps the key private, so the test goes through
// the same door the command does.
func sealingKey(t *testing.T, root string) *writer.Key {
	t.Helper()
	kb, err := os.ReadFile(filepath.Join(root, ".keys", "mldsa65.key"))
	if err != nil {
		t.Fatal(err)
	}
	var kf struct{ Seed string }
	if err := json.Unmarshal(kb, &kf); err != nil {
		t.Fatal(err)
	}
	seed, _ := hex.DecodeString(kf.Seed)
	k, err := writer.FromSeed(writer.MLDSA65, seed)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// The script names its own missing tool and exits 3 without examining the
// store, rather than blaming the data (quill, coop main/1012 finding 10).
// PATH is reduced to a directory holding sh's needs but no jq.
func TestTheScriptNamesItsOwnMissingTool(t *testing.T) {
	root := filepath.Join(t.TempDir(), "s")
	if _, err := store.Init(root, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	for _, tool := range []string{"sha256sum", "cut", "ls", "sort", "basename", "dirname", "find", "wc", "mktemp", "rm", "printf", "cat"} {
		if p, err := exec.LookPath(tool); err == nil {
			os.Symlink(p, filepath.Join(bin, tool))
		}
	}
	cmd := exec.Command("sh", "../tools/archive-check.sh", root)
	cmd.Env = []string{"PATH=" + bin}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("the script passed without jq:\n%s", out)
	}
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 3 {
		t.Fatalf("exit %v, want 3:\n%s", err, out)
	}
	if !strings.Contains(string(out), "jq is not on PATH") || strings.Contains(string(out), "not JSON") {
		t.Fatalf("the refusal must name jq and not blame the store:\n%s", out)
	}
}
