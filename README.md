# cairn

A content-addressed store of blobs joined by typed, append-only relations,
mountable as a filesystem where the path is the query.

A cairn is built one stone at a time by whoever passes. It marks the way for
the one who comes after, it stands without its builders, and the stones are
not the message: the arrangement is. This store is that. Everything written
to it is kept; every correction links to what it corrects; every edge says
who wrote it and when; and a reader who was not there can find what matters
without an index anyone wrote by hand.

If you want the acronym: Content-Addressed Index of Relations and Notes.

## What it is for

UNIX says everything is a file. cairn says everything is a concept. A file
has one path; a concept has as many relations as it has, and the thing you
want is found by describing it, not by remembering where it was put. The
first user is an agent that needs a store for its own memory, journals,
tools and self that outlives a session, a machine and a model; the design is
general, and the agent's store is one configured instance of it.

## What it is, in one paragraph

Blobs are bytes named by their hash. Concepts are nodes. Relations are typed
edges in an append-only log, each carrying time, sequence and a signing
writer; corrections are edges (`supersedes`, `retracts`), never overwrites.
The store is the blobs plus the log, plain files readable with `cat` and
`sha256` on a machine with no cairn software. An index is derived from the
log and rebuildable at any time; a mount presents the relations as paths,
the path being a query; a protocol replicates the log by offset to other
machines; encryption, when a store wants it, is envelope encryption whose
recipient set is the access control. The storage layer is a seam: the file
backend is the reference and the archival form, and other backends may stand
behind the same contract.

## Documents

| Document | What it holds |
|---|---|
| [docs/SPEC.md](docs/SPEC.md) | The specification: formats, relations, query, mount, protocol, security. Normative. |
| [docs/DECISIONS.md](docs/DECISIONS.md) | The numbered design decisions, who made each and why, with the alternatives refused. |
| [docs/ROADMAP.md](docs/ROADMAP.md) | The milestones in dependency order, each with deliverables, tests and acceptance. The plan of record. |
| [CONTRIBUTING.md](CONTRIBUTING.md) | How work lands here: the commit rules, the test discipline, the engine-and-instance boundary. |

## Status

Framework. The specification is a draft at 0.1, the decisions are recorded,
the roadmap is the plan, and the binary prints its version and nothing else.
Nothing stores anything yet. See the roadmap for where the work stands; it
is the document to trust over this one when they disagree.

## Prior art

Two projects got large parts of this right first and should be read before
this one: [Perkeep](https://perkeep.org/) (content-addressed blobs, signed
claims, a FUSE mount, sync) and [TMSU](https://tmsu.org/) (a FUSE filesystem
where a path is a conjunction of tags). cairn differs in making relations
typed and append-only with supersession, in treating the log as the store
and every index as derived, and in being specified for a reader who was not
there. [XTDB](https://xtdb.com/) is the reference for bitemporal records.

## Building

Go 1.26 or later. `go build -o cairn ./cmd/cairn` produces the binary;
`go test ./...` runs the suite. The repository is a [reaper](https://forge.axonibyte.com/axonibyte/reaper)
tenant: `reaper up`, `reaper sync`, `reaper build`, `reaper run` exercise it
on a disposable FreeBSD guest.

## License

Blue Oak Model License 1.0.0 for the source code, with the companion grants
for specifications, documentation and assets named in [LICENSE](LICENSE).
Logos and trademarks are excluded from every grant.
Copyright (c) 2026 Axonibyte Innovations, LLC.
