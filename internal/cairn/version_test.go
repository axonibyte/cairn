package cairn

import "testing"

// TestTheVersionsArePinnedTogether is the one place a constant is asserted
// on purpose: a bump to either version is a decision, and this arm makes the
// decision visible in the diff of a test rather than only in a comment.
func TestTheVersionsArePinnedTogether(t *testing.T) {
	if Version != "0.0.0" {
		t.Fatalf("Version is %q; a bump here lands at a roadmap milestone and updates this arm with it", Version)
	}
	if SpecVersion != "0.1" {
		t.Fatalf("SpecVersion is %q; a bump here means docs/SPEC.md changed version and this arm with it", SpecVersion)
	}
}
