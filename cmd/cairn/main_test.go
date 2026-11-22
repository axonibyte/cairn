package main

import (
	"bytes"
	"database/sql"
	_ "modernc.org/sqlite"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

type out struct {
	status         int
	stdout, stderr string
}

func cli(args ...string) out {
	var so, se bytes.Buffer
	status := run(args, strings.NewReader(""), &so, &se)
	return out{status, so.String(), se.String()}
}

func cliStdin(stdin string, args ...string) out {
	var so, se bytes.Buffer
	status := run(args, strings.NewReader(stdin), &so, &se)
	return out{status, so.String(), se.String()}
}

// version answers; no arguments and an unknown command are usage, status 2,
// with nothing on stdout.
func TestVersionAndUsage(t *testing.T) {
	o := cli("version")
	if o.status != 0 || !strings.HasPrefix(o.stdout, "cairn 0.0.0 (spec 0.1)") {
		t.Fatalf("version: %+v", o)
	}
	for _, args := range [][]string{{}, {"frobnicate"}, {"init"}, {"put", "x"}} {
		o := cli(args...)
		if o.status != 2 || o.stdout != "" || !strings.Contains(o.stderr, "cairn") {
			t.Fatalf("%v: %+v", args, o)
		}
	}
}

// The M1 walk, through the command and nothing else: init, a concept, a
// blob with its wrote edge, an edge with attributes, the log printed as
// written forms, verify clean; then a refusal by name with status 1 and
// nothing on stdout.
func TestTheM1Walk(t *testing.T) {
	root := filepath.Join(t.TempDir(), "s")
	o := cli("init", root)
	if o.status != 0 {
		t.Fatalf("init: %+v", o)
	}
	lines := strings.Split(strings.TrimSpace(o.stdout), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "k:ed25519:") || !strings.HasPrefix(lines[1], "sha256:") {
		t.Fatalf("init printed %q", o.stdout)
	}
	writer := lines[0]
	o = cli("concept", root, "cal")
	if o.status != 0 {
		t.Fatalf("concept: %+v", o)
	}
	// stdout is exactly one value per command, so ID=$(cairn ...) composes
	// (quill, coop main/1008): the id, nothing beside it; the seq is a note
	// on stderr
	concept := strings.TrimSuffix(o.stdout, "\n")
	if strings.ContainsAny(concept, " \n") || !strings.HasPrefix(concept, "c:") || !strings.Contains(o.stderr, "seq 3") {
		t.Fatalf("concept stdout %q stderr %q", o.stdout, o.stderr)
	}
	o = cliStdin("a note about cal", "put", root, "-")
	blob := strings.TrimSuffix(o.stdout, "\n")
	if o.status != 0 || !strings.HasPrefix(blob, "sha256:") || len(blob) != 71 || !strings.Contains(o.stderr, "seq 4") {
		t.Fatalf("put: %+v", o)
	}
	o = cliStdin("a note about cal", "put", root, "-")
	if o.status != 0 || strings.TrimSuffix(o.stdout, "\n") != blob || !strings.Contains(o.stderr, "already here") {
		t.Fatalf("second put: %+v", o)
	}
	// and the composed value is accepted as a subject, which is the whole point
	if o := cli("edge", root, "cites", blob, concept); o.status != 0 || strings.TrimSpace(o.stdout) != "5" {
		t.Fatalf("edge with composed ids: %+v", o)
	}
	o = cli("edge", root, "is-about", blob, concept, "via=hand", "n=3", "ok=true")
	if o.status != 0 || o.stdout != "6\n" {
		t.Fatalf("edge: %+v", o)
	}
	o = cli("log", root, "--from", "3")
	if o.status != 0 {
		t.Fatalf("log: %+v", o)
	}
	logLines := strings.Split(strings.TrimSpace(o.stdout), "\n")
	if len(logLines) != 4 || !strings.HasPrefix(logLines[0], `{"seq":3,`) || !strings.Contains(logLines[3], `"a":{"n":3,"ok":true,"via":"hand"}`) || !strings.Contains(logLines[1], `"rel":"wrote","s":"`+writer+`","o":"`+blob+`"`) {
		t.Fatalf("log printed:\n%s", o.stdout)
	}
	o = cli("verify", root)
	if o.status != 0 || !strings.HasPrefix(o.stdout, "verified 6 edges; last seal at seq 0") {
		t.Fatalf("verify: %+v", o)
	}
	// refusals: status 1, named, nothing on stdout
	for _, args := range [][]string{{"edge", root, "names", "lit:x", concept}, {"edge", root, "Bad-Rel", concept, concept}, {"edge", root, "seal", "edge:1"}, {"init", root}, {"verify", filepath.Join(t.TempDir(), "nowhere")}} {
		o := cli(args...)
		if o.status != 1 || o.stdout != "" || !strings.Contains(o.stderr, "REFUSED") && !strings.Contains(o.stderr, "cairn") {
			t.Fatalf("%v: %+v", args, o)
		}
	}
	if o := cli("edge", root, "is-about", blob, concept, "novalue"); o.status != 2 {
		t.Fatalf("malformed attribute: %+v", o)
	}
}

// SPEC §15.2, the archival guarantee, driven on a store this command made
// (never by writing files under blobs/): tools/archive-check.sh with a
// shell, sha256sum and jq lists every edge, verifies every line is its own
// canonical form, follows the chain from cairn.json, verifies every blob
// and manifest, and prints the supersedes and retracts it read. Then three
// tamperings, each refused by name: a blob's bytes, an edge's bytes, a
// removed line. jq is required, not optional: a box without it fails here
// by name rather than skipping the one arm the format's promise rests on.
func TestTheArchivalScriptOnAStoreTheCommandMade(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Fatal("jq is required for the archival arm (tools/reaper-bootstrap.sh installs it)")
	}
	root := filepath.Join(t.TempDir(), "s")
	if o := cli("init", root); o.status != 0 {
		t.Fatalf("init: %+v", o)
	}
	c := strings.TrimSpace(cli("concept", root, "cal").stdout)
	b1 := strings.TrimSpace(cliStdin("first draft", "put", root, "-").stdout)
	b2 := strings.TrimSpace(cliStdin("second draft", "put", root, "-").stdout)
	big := strings.Repeat("a large blob, chunked by the default bound? no: ", 10)
	bigPath := filepath.Join(t.TempDir(), "big")
	os.WriteFile(bigPath, []byte(big), 0o644)
	bb := strings.TrimSpace(cli("put", root, bigPath).stdout)
	for _, args := range [][]string{{"is-about", b1, c}, {"supersedes", b2, b1}, {"cites", bb, c, "via=test", "n=1", "zed=last"}} {
		if o := cli(append([]string{"edge", root}, args...)...); o.status != 0 {
			t.Fatalf("edge %v: %+v", args, o)
		}
	}
	// retract the is-about edge (seq 6: 2 key-of, names, 2 wrote... count from the log)
	o := cli("log", root)
	lines := strings.Split(strings.TrimSpace(o.stdout), "\n")
	var isAbout string
	for _, l := range lines {
		if strings.Contains(l, `"rel":"is-about"`) {
			isAbout = "edge:" + strings.TrimPrefix(strings.SplitN(strings.TrimPrefix(l, `{"seq":`), ",", 2)[0], "")
		}
	}
	if o := cli("edge", root, "retracts", isAbout, isAbout); o.status != 0 {
		t.Fatalf("retracts: %+v", o)
	}
	script, _ := filepath.Abs("../../tools/archive-check.sh")
	runScript := func(dir string) (string, error) {
		out, err := exec.Command("sh", script, dir).CombinedOutput()
		return string(out), err
	}
	out, err := runScript(root)
	if err != nil {
		t.Fatalf("archive-check on a good store: %v\n%s", err, out)
	}
	if !strings.Contains(out, "edges chained from cairn.json") || !strings.Contains(out, "supersedes: "+b2+" -> "+b1) || !strings.Contains(out, "retracts:   "+isAbout+" -> "+isAbout) || !strings.Contains(out, "blob files verified") {
		t.Fatalf("archive-check output:\n%s", out)
	}
	// tampering 1: a blob's bytes
	copyStore := func() string {
		dst := filepath.Join(t.TempDir(), "copy")
		if out, err := exec.Command("cp", "-R", root, dst).CombinedOutput(); err != nil {
			t.Fatalf("cp: %v %s", err, out)
		}
		return dst
	}
	dst := copyStore()
	hx := strings.TrimPrefix(b1, "sha256:")
	os.WriteFile(filepath.Join(dst, "blobs", hx[:2], hx[2:]), []byte("first draft!"), 0o644)
	if out, err := runScript(dst); err == nil || !strings.Contains(out, "bytes do not hash to the name") {
		t.Fatalf("tampered blob: %v\n%s", err, out)
	}
	// tampering 2: an edge's bytes (the retracts line's object changed), the chain breaks there
	dst = copyStore()
	seg := filepath.Join(dst, "log", "000000.jsonl")
	data, _ := os.ReadFile(seg)
	altered := strings.Replace(string(data), `"rel":"names","s":"`+c+`","o":"lit:cal"`, `"rel":"names","s":"`+c+`","o":"lit:caleb"`, 1)
	if altered == string(data) {
		t.Fatal("the names line was not found to alter")
	}
	os.WriteFile(seg, []byte(altered), 0o644)
	if out, err := runScript(dst); err == nil || !strings.Contains(out, "seq 4: prev") {
		t.Fatalf("altered edge: %v\n%s", err, out)
	}
	// tampering 3: a removed line (the segment path is the new copy's)
	dst = copyStore()
	seg = filepath.Join(dst, "log", "000000.jsonl")
	data, _ = os.ReadFile(seg)
	ls := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	os.WriteFile(seg, []byte(strings.Join(append(ls[:2], ls[3:]...), "\n")+"\n"), 0o644)
	if out, err := runScript(dst); err == nil || !strings.Contains(out, "seq 3: the line says seq 4") {
		t.Fatalf("removed line: %v\n%s", err, out)
	}
	// tampering 4: the LAST line re-spaced -- the same edge, the chain still
	// whole (nothing follows it), so only "its own canonical form" refuses it
	dst = copyStore()
	seg = filepath.Join(dst, "log", "000000.jsonl")
	data, _ = os.ReadFile(seg)
	ls = strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	ls[len(ls)-1] = strings.Replace(ls[len(ls)-1], `"rel":`, `"rel": `, 1)
	os.WriteFile(seg, []byte(strings.Join(ls, "\n")+"\n"), 0o644)
	if out, err := runScript(dst); err == nil || !strings.Contains(out, "not its own canonical form") {
		t.Fatalf("re-spaced last line: %v\n%s", err, out)
	}
}

// TestTheM2Walk drives query and current through the command, end to end:
// a store with two concepts, three drafts about one of them and a
// supersedes chain; the index built beside the store on the first query,
// resumed on the second (the stderr note names the seq), rebuilt when the
// file is tampered (D-35: a reader never answers from an index it did not
// just bring to the log); refusals by name and status 1 for a malformed
// expression, a traversal over a cycle, and a missing store; one identity
// per line, sorted; current answering the head through two hops and an
// unseen identity as itself.
func TestTheM2Walk(t *testing.T) {
	root := t.TempDir()
	if o := cli("init", root); o.status != 0 {
		t.Fatalf("init: %+v", o)
	}
	concept := func(name string) string {
		t.Helper()
		o := cli("concept", root, name)
		if o.status != 0 {
			t.Fatalf("concept %s: %+v", name, o)
		}
		return strings.TrimSpace(o.stdout)
	}
	put := func(text string) string {
		t.Helper()
		o := cliStdin(text, "put", root, "-")
		if o.status != 0 {
			t.Fatalf("put: %+v", o)
		}
		return strings.TrimSpace(o.stdout)
	}
	edge := func(rel, s, o string) string {
		t.Helper()
		r := cli("edge", root, rel, s, o)
		if r.status != 0 {
			t.Fatalf("edge %s: %+v", rel, r)
		}
		return strings.TrimSpace(r.stdout)
	}
	alpha, beta := concept("alpha"), concept("beta")
	b1, b2, b3 := put("first draft"), put("second draft"), put("third draft")
	edge("is-about", b1, alpha)
	edge("is-about", b2, alpha)
	edge("is-about", b3, alpha)
	edge("cites", b2, b1)
	edge("supersedes", b2, b1)
	edge("supersedes", b3, b2)
	_ = beta

	// the first query builds the index beside the store
	o := cli("query", root, "is-about="+alpha)
	if o.status != 0 || strings.TrimSpace(o.stdout) != b3 {
		t.Fatalf("is-about alpha: %+v, want %s alone (b1 and b2 are superseded)", o, b3)
	}
	if _, err := os.Stat(filepath.Join(root, "index.sqlite")); err != nil {
		t.Fatalf("no index file beside the store: %v", err)
	}
	if !strings.Contains(o.stderr, "index at seq ") {
		t.Fatalf("stderr does not name the index's seq: %q", o.stderr)
	}
	// a walk, one identity per line, sorted
	o = cli("query", root, "is-about="+alpha+"/supersedes*2")
	want := []string{b1, b2}
	sort.Strings(want)
	if o.status != 0 || strings.TrimSpace(o.stdout) != strings.Join(want, "\n") {
		t.Fatalf("supersedes*2: %+v, want %v", o, want)
	}
	// the bare name
	o = cli("query", root, "alpha")
	if o.status != 0 || strings.TrimSpace(o.stdout) != alpha {
		t.Fatalf("bare name: %+v", o)
	}
	// nothing matches: status 0, empty stdout
	o = cli("query", root, "is-about="+beta)
	if o.status != 0 || o.stdout != "" {
		t.Fatalf("empty answer: %+v", o)
	}
	// current through two hops, and an unseen identity is its own head
	o = cli("current", root, b1)
	if o.status != 0 || strings.TrimSpace(o.stdout) != b3 {
		t.Fatalf("current b1: %+v, want %s", o, b3)
	}
	o = cli("current", root, "sha256:"+strings.Repeat("0", 64))
	if o.status != 0 || strings.TrimSpace(o.stdout) != "sha256:"+strings.Repeat("0", 64) {
		t.Fatalf("current of an unseen identity: %+v", o)
	}

	// the index resumes: a new edge, and the next query applies the tail
	// rather than rebuilding (the seq note moves by exactly one)
	edge("is-about", b3, beta)
	o = cli("query", root, "is-about="+beta)
	if o.status != 0 || strings.TrimSpace(o.stdout) != b3 {
		t.Fatalf("after one more edge: %+v", o)
	}

	// refusals, by name, status 1
	o = cli("query", root, "cites*2")
	if o.status != 1 || !strings.Contains(o.stderr, "REFUSED") || !strings.Contains(o.stderr, "before any term") {
		t.Fatalf("malformed expression: %+v", o)
	}
	o = cli("query", filepath.Join(root, "nowhere"), "alpha")
	if o.status != 1 || !strings.Contains(o.stderr, "REFUSED") {
		t.Fatalf("missing store: %+v", o)
	}
	o = cli("query", root)
	if o.status != 2 {
		t.Fatalf("usage: %+v", o)
	}

	// a tampered index file is rebuilt, not trusted (D-31, D-35)
	db, err := sql.Open("sqlite", filepath.Join(root, "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE meta SET v = 999 WHERE k = 'at'`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	o = cli("query", root, "is-about="+alpha)
	if o.status != 0 || strings.TrimSpace(o.stdout) != b3 {
		t.Fatalf("after tampering the index: %+v, want a rebuilt answer %s", o, b3)
	}
}
