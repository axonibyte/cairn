package canon

import "testing"

// TestLiteral: Literal and IsLiteral are inverses, and a non-literal is not
// read as one. The form itself is pinned by tests/source_test.go, which
// refuses the prefix as a string anywhere but this package.
func TestLiteral(t *testing.T) {
	for _, text := range []string{"alpha", "", "a=b", "with spaces", "lit:nested"} {
		got, ok := IsLiteral(Literal(text))
		if !ok || got != text {
			t.Fatalf("round trip of %q: %q %v", text, got, ok)
		}
	}
	for _, v := range []string{"", "c:01HXYZ", "sha256:ab", "lit", "li:x", "LIT:x"} {
		if _, ok := IsLiteral(v); ok {
			t.Fatalf("%q read as a literal", v)
		}
	}
}
