package main

import (
	"bytes"
	"path/filepath"
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
