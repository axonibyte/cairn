package main

import (
	"os"
	"strings"
	"testing"
)

// TestVersionIsTheOnlyCommandAndSaysSo drives run both ways: the one
// command answers with status 0 and the version line, and anything else is
// refused with status 2 and a sentence naming what this build does not do,
// so a reader is never told a store exists where none does.
func TestVersionIsTheOnlyCommandAndSaysSo(t *testing.T) {
	out := capture(t, func(stdout, stderr *os.File) int { return run([]string{"version"}, stdout, stderr) })
	if out.status != 0 {
		t.Fatalf("version: status %d, want 0; stderr %q", out.status, out.stderr)
	}
	if !strings.HasPrefix(out.stdout, "cairn 0.0.0 (spec 0.1)") {
		t.Fatalf("version printed %q", out.stdout)
	}

	out = capture(t, func(stdout, stderr *os.File) int { return run([]string{"put", "x"}, stdout, stderr) })
	if out.status != 2 {
		t.Fatalf("unknown command: status %d, want 2", out.status)
	}
	if !strings.Contains(out.stderr, "stores nothing") {
		t.Fatalf("the refusal must say this build stores nothing; got %q", out.stderr)
	}
	if out.stdout != "" {
		t.Fatalf("a refusal writes nothing to stdout; got %q", out.stdout)
	}
}

type captured struct {
	status         int
	stdout, stderr string
}

// capture runs f with real files for stdout and stderr so the production
// signature stays *os.File, and reads both back. The files are the test's
// temp files, removed with it.
func capture(t *testing.T, f func(stdout, stderr *os.File) int) captured {
	t.Helper()
	so, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	se, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	status := f(so, se)
	read := func(fh *os.File) string {
		if _, err := fh.Seek(0, 0); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(fh.Name())
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	return captured{status: status, stdout: read(so), stderr: read(se)}
}
