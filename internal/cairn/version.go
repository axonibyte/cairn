// Package cairn holds what every command and every backend shares: today,
// only the version. The specification is in docs/SPEC.md and the engine's
// pieces arrive milestone by milestone (docs/ROADMAP.md); this package grows
// with them and never learns whose store it is serving.
package cairn

// Version is the implementation's version, bumped at the milestones the
// roadmap marks. It is the one constant a test may assert, because a
// version bump is the change the test exists to notice.
const Version = "0.0.0"

// SpecVersion is the version of docs/SPEC.md this implementation targets.
// An implementation and its specification move together: a bump here
// without a change to the specification, or the reverse, is a defect.
const SpecVersion = "0.1"
