package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLiteralFormHasOneReader is a tier-3 arm (TESTING.md: source as data):
// the literal prefix of SPEC §4.2 appears as a string in internal/canon and
// nowhere else in the engine's code, because three sites in two packages
// spelling it separately is the shape that let the index's name term look
// up the bare word while the store wrote the prefixed one (D-33). Test files
// may spell it, since a test that pins the form is the point.
func TestLiteralFormHasOneReader(t *testing.T) {
	var offenders []string
	err := filepath.WalkDir("../internal", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if strings.Contains(filepath.ToSlash(path), "/internal/canon/") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(b), "\"lit:") {
			offenders = append(offenders, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("the literal prefix is spelled outside internal/canon: %v", offenders)
	}
	// the control: canon itself does spell it, so the walk can see the string
	b, err := os.ReadFile("../internal/canon/canon.go")
	if err != nil || !strings.Contains(string(b), "\"lit:") {
		t.Fatalf("control: canon.go does not spell the prefix; the arm would pass over anything: %v", err)
	}
}
