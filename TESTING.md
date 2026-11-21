# Testing cairn: a portfolio of oracles

This repository tests by the method of Axonibyte's
[A Portfolio of Oracles](https://blog.axonibyte.com/a-portfolio-of-oracles)
(D-23). The pyramid describes cost; the portfolio describes capability.
Every tier kept here answers a question no cheaper tier can answer, and a
tier that cannot is deleted. The rules that matter more than any test are
in [CONTRIBUTING.md](CONTRIBUTING.md): never weaken a check to pass, every
narrowing carries its exact reason, every fix ships with the test that
would have caught it, a pre-existing failure is proven pre-existing, new
assertions are mutation-checked, fix the cause.

The acceptance test for the whole portfolio, above everything below: take
defects found and fixed by hand, revert the fixes, and confirm the harness
rediscovers them. Until it does, the suite is green and measuring nothing.

## The tiers, and the question only each can answer

| Tier | The question only it can answer | In cairn |
|---|---|---|
| 1. Pure unit | Is this calculation right, at its boundaries? | Canonical form, hashes, chain hashes, `seq` contiguity, manifest chunking at the bound and the bound plus one, query term parsing, currency over `supersedes` chains (empty, one, many, a cycle). |
| 2. Cross-implementation vectors | Do two independent implementations agree? | A committed vector file: inputs, canonical forms, SHA-256 and HMAC identities, chain hashes, Ed25519 and ML-DSA-65 signatures, HPKE wraps. Consumed by the Go suite and by the archival shell script, which is the second implementation the specification promises (SPEC §15.2). Run twice: once normally, once under a hostile locale and a non-UTF-8 default, because the canonical form must not depend on either. The index, since M2: the memory index and the SQLite index rebuilt from one log answer one query set identically, errors and history included, with a one-edge control (`internal/sqlindex`). |
| 3. Source-as-data | Does the code's structure still hold its claims? | The core relation constants equal SPEC §5.2's table; every `MUST` in SPEC names a test that holds it; DECISIONS numbers are contiguous and every D-n cited in SPEC exists; every suite id in code is in SPEC §8.0; no production path reads a user's name (D-9). |
| 4. Contract | Do the authorization and refusal rules hold, in isolation? | The protocol's matrix: every caller key state (writer, reader, unknown, revoked) times every endpoint times every request shape, as a table; every refusal by name and never by a bare status. |
| 5. Client against a fake server | What does the client do when a request fails? | `cairn pull` against a fake server that returns a truncated log, a forged edge, a 500, a stale head, a blob whose bytes do not hash. The fake imports the real verifier so it cannot drift into testing itself. |
| 6. Full stack, staged, hostile | Does it work against real storage, a real mount, a real second machine? | The reaper battery: a FreeBSD guest, a store on ZFS, a FUSE mount, a replica over the protocol, started hostile (a full disk at the segment roll, a clock behind the source, a locale that is not UTF-8). Idempotence asserted: a second rebuild changes nothing, a second pull fetches nothing. No backdoors: state is built through `cairn` and the mount, never by writing files under `blobs/`. |
| 7. Seeded fuzz | Does any ordinary-but-untried input crash it? | Malformed edges (missing keys, wrong types, bad `seq`, bad `prev`, bad `sig`), malformed manifests, paths at the mount, protocol bodies. Oracle: no panic, every refusal well-formed and by name, the process still answering. Seed printed, replayable from the environment, corpus shared with tier 9's nemesis. |
| 8. Concurrency | Does the invariant survive simultaneous actors? | Two writers appending at once: exactly one succeeds per `seq` and the log reads back contiguous. Readers during a rebuild: every answer at a consistent `At()`. Oracle: the store's own state read back, never the count of 200s. |
| 9. Simulated writers | What breaks only after history accumulates? | A seeded generator of blobs, edges, supersessions, retractions, scope changes and key rotations over hundreds of actions by several writers across several stores; a shadow model of currency and membership; a checker diffing model against the store and against a rebuilt index and a replica; a nemesis (kill mid-append, truncate a segment, corrupt a blob, skew the clock, replay a stale replica); a shrinker that bisects the prefix and removes action kinds. Fixed seeds committed; any seed that finds a defect is promoted for good. |
| 10. Live audit over an accumulated world | Does the mount hold up over a store somebody else built? | The mount and the query driven over a tier-9 world: listing at depth, currency after many supersessions, a traversal that never fails for want of a path. |
| 11. Human evidence | Does this make sense to a newcomer? | P-1, the fresh reader: a session that has never seen the store, told only the mount point, with its transcript kept verbatim. Evidence, not a verdict. |

## Self-testing the oracle

An invariant that never fires is indistinguishable from a passing suite.
Before any tier-9 invariant is trusted, it is fed stores a broken
implementation would produce, modeled on the defects that motivated it (a
`supersedes` edge that does not change currency, a retraction the query
still sees, a rebuilt index that disagrees with the log, a replica one edge
short), and each must complain. This runs with no store at all, in under a
second, and it is the check that makes the expensive tiers mean anything.

## Two oracles for one claim

Where a claim carries the project, assert it from both sides. The archival
guarantee (SPEC §15.2) is checked by the Go reader and by a shell script
with `cat`, `sha256sum` and `jq`; neither alone is sufficient, and the
script is the one a reader with a dead disk will have. Currency is checked
by the index and by walking the log by hand.

## Cross-cutting

- One hostile corpus, consumed by tiers 7 and 9 and by the mount's tests.
- Determinism and replay wherever randomness appears: print the seed, read
  it back from the environment, keep anything that must vary out of the
  seeded stream.
- Assert the absence of things with time allowed to pass: a killed process
  leaves nothing behind is proven by looking after a wait, and the process
  count is asserted before the success line.
- Name what a test does not prove, in the file, beside the test that proves
  the stronger claim.
- Match effort to change: a comment does not warrant the reaper battery; a
  format change does, and it is not optional. Say which suites were skipped
  and why.
- Pipelines report the last command's status. A verdict is never chained to
  `head`, `grep -c` or any command that cannot fail.
- `go test -short` is not the suite. Two arms skip under it by design, the
  40 MiB manifest round trip and the ten-thousand-edge log, because they
  cost seconds; a clear is given on `go test ./...` without `-short`, and a
  reading that says "green" says which it ran. One arm more runs only with
  `CAIRN_ACCEPTANCE=1`: the ten-thousand-edge store under the archival shell
  script, about two minutes, run at a milestone's close with its reading
  recorded in the roadmap.

## Order of return, starting from zero

Pure unit on the canonical form and the chain; the archival script as the
first second implementation; seeded fuzz on the log reader; the contract
matrix; the reaper battery started hostile; source-as-data once the spec and
code can drift; concurrency for the single-writer rule; simulated writers
last, with the oracle self-test written first.
