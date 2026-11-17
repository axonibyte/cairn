# Contributing

cairn is specification-driven. The specification in `docs/SPEC.md` is
normative; code implements it, tests prove it, and a disagreement between
the two is a defect in whichever one the decisions record says is wrong.
This file is how work lands.

## The boundary: engine and instance

cairn the engine is what the specification defines: the blob and log
formats, the core relations, the query, the mount's rules, the protocol, the
storage contract. A store is an instance of it, configured. Nothing about
any particular user, including the agent the store was first built for, is
hard-coded. If a thing can be a setting, it is a setting; if a thing is a
property every store must have for the record to stay honest (append-only
edges, supersession, authorship and time on every edge, the archival file
form), it is the engine's and is not configurable off.

## Commits

- **Author.** Every commit is authored `Caleb L. Power <cpower@axonibyte.com>`,
  the repository's configured user. In this house a commit establishes
  responsibility, not authorship; who designed and wrote what is in
  `docs/DECISIONS.md` and the build logs, which are the record a reader
  trusts. Never pass `--author`; never bypass hooks.
- **No generated trailers.** No `Co-Authored-By`, no session links, nothing
  appended by a tool.
- **A cut is a push.** A branch goes to the forge at the cut, before its
  suites run, so a tree lost between commit and green costs the suite's
  minutes and not the work. Red may be pushed; it may not be merged.
- **Green before merge.** Every suite that can run on the platform is green
  before a head is cleared. A failure that also reproduces on a clean
  checkout is named in the report, not routed around.
- **Every fix ships with the test that would have caught it.** Where a change
  is untestable in isolation (documentation, a version bump, a comment), the
  commit message says so instead of inventing a test that asserts a constant.
- **Never weaken a check to pass.** No skipped tests, lowered thresholds,
  swallowed errors or narrowed suites to route around a defect. A narrowing
  carries its reason, and the reason covers exactly what it narrows.
- **One head, one thing.** A commit's message says what changed and why, and
  the subject names the component.
- **American English** in code, comments, commit messages and documentation.

## Tests

The method is a portfolio of oracles; the tiers and what each one alone can
see are in [TESTING.md](TESTING.md). The rules below are the ones that
matter on the day a test turns red.

- **Drive, do not read.** A claim about behavior is made by a test that
  fails without the change and passes with it, not by reading the code.
- **Mutants.** New arms are checked by mutation: the defect they guard
  against is planted and the arm is seen to fire. A mutant that does not
  compile, does not run, or does not do what it says proves nothing; assert
  the subject before the verdict.
- **Vacuity controls.** A suite carries a control that proves its
  instruments can see what they then fail to find.
- **Pipelines report the last command's status.** Never chain a verdict to
  `head`, `grep -c` or any command that cannot fail; read bare exit codes.
- **Both directions.** A guard is shown to refuse what it should and to admit
  what it should; a measurement that moves with run order is taken in both
  orders.

## Pull requests

A pull request carries a plain-language paragraph saying what the change
does, written from the diff and not from the design, before any link. It may
end with `:robot: AI-assisted engineering`. Merge is fast-forward and tags
are signed; both are the repository owner's hand.

## Specifications and documents

Changes to `docs/SPEC.md` are decisions and are recorded in
`docs/DECISIONS.md` with their reasoning and the alternatives refused.
Specifications are licensed under the Open Web Foundation Agreement 1.0 and
documents under CC-BY-4.0; see `LICENSE`.
