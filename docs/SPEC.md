# cairn specification

Version 0.1, draft, 2026-11-17. Normative where it says MUST, SHOULD and MAY,
in the sense of RFC 2119. The decisions behind each section are numbered in
[DECISIONS.md](DECISIONS.md) and cited as D-n. This specification is licensed
under the Open Web Foundation Agreement 1.0; attribution is to "cairn
specification 0.1".

## 1. Purpose and scope

cairn is a store of blobs joined by typed, append-only relations, presentable
as a filesystem whose paths are queries, replicable between machines, and
readable without cairn software. It is specified for a reader who was not
there when it was written: a later session, a different program, a person
with a shell and a dead disk.

In scope for 0.1: the blob format, the edge log, concepts and the core
relation types, retraction, writers and signatures, scopes and encryption,
the storage contract, the derived index, the query grammar, the mount, the
replication protocol, and the archival guarantee. Out of scope for 0.1, with
their attachment points named in section 14: natural-language resolution,
embedding indexes, multi-writer merge, hardware key custody.

## 2. Vocabulary

| Term | Meaning |
|---|---|
| **blob** | An immutable sequence of bytes, identified by its content hash. |
| **concept** | A node: a thing the store can hold relations about. Identified by an id, named by edges. |
| **edge** | One line of the log: a typed relation from a subject to an object, with writer, time and sequence. Edges are never modified or removed. |
| **relation type** | The kind of an edge. The core types are the engine's; others are an instance's. |
| **log** | The append-only sequence of edges. The log plus the blobs is the store. |
| **index** | Any structure derived from the log for query speed. Disposable; rebuilt from the log. |
| **writer** | A signing key. One principal holds one key per machine. |
| **principal** | An agent or person who answers for a writer's edges. A concept. |
| **scope** | A named recipient set. The unit of access control. A concept. |
| **store** | One log, its blobs, one writer at a time, any number of readers and replicas. |
| **mount** | A filesystem view of a store in which a path is a query. |

## 3. Blobs

3.1. A blob's **identity** is the lowercase hexadecimal SHA-256 of its bytes,
prefixed `sha256:`. For encrypted stores see section 9.4.

3.2. A blob MUST be stored at a path derived from its identity in the file
backend: `blobs/<first two hex>/<rest>`. A blob file MUST be written to a
temporary name and renamed into place; a reader that finds a blob whose hash
does not match its name MUST refuse it by name.

3.3. Blobs above a bound (default 8 MiB, an instance setting) MUST be stored
as a **manifest**: a blob in the format of section 3.4 listing the identities
of its chunks in order. The manifest's identity is the large blob's identity
for every purpose except byte retrieval. Chunk size is an instance setting;
the default is 4 MiB. Chunks are blobs.

3.4. A manifest is UTF-8 JSON: `{"cairn":"manifest","v":1,"size":N,
"chunks":["sha256:...", ...]}`. Keys MUST be emitted in that order.

3.5. A blob carries no metadata. Everything about a blob is edges.

## 4. The edge log

4.1. The log is a sequence of **edges**, one per line, UTF-8 JSON, newline
terminated, in the file backend at `log/<segment>.jsonl` where segments are
numbered from 000000 and rolled at an instance-set size (default 64 MiB).
Lines are appended; a line, once written, MUST never be altered or removed
by any operation other than a store-admin compaction that preserves every
edge (section 7.4).

4.2. An edge has these fields, keys emitted in this order:

| Key | Type | Meaning |
|---|---|---|
| `seq` | integer | The edge's position in this store's log, from 1, contiguous. |
| `t` | string | Wall time of writing, RFC 3339 with at least millisecond precision, UTC. |
| `w` | string | The writer's key id (section 8). |
| `rel` | string | The relation type. |
| `s` | string | The subject: a concept id, blob identity or edge reference. |
| `o` | string, optional | The object: a concept id, blob identity, edge reference or literal (`lit:` prefix). Absent for unary relations. |
| `a` | object, optional | Attributes: a flat map of string keys to JSON values. Instance-defined, except `via` (section 8.5) and `scope` (section 9). |
| `sig` | string | The writer's signature over the canonical form of the edge without `sig` (section 8.3). |

An edge reference is `edge:<seq>` within the same store. A concept id is
`c:<ulid>`, minted by the writer at creation. A literal is `lit:` followed
by a UTF-8 string.

4.3. Ordering is by `seq`. `t` is informational: a reader MUST order by
`seq` and MAY use `t` to relate edges across stores. A writer MUST emit
`seq` values contiguous from the last edge in the log it has read; a store
MUST refuse an append whose `seq` is not the next value (D-4, single writer).

4.4. The log MUST be readable with no software but a line reader and a JSON
parser. Every byte of meaning in a store is in the log and the blobs.

## 5. Concepts and the core relations

5.1. A concept is created by a `names` edge from a new `c:` id to a literal.
A concept MAY have many names and a name MAY name many concepts; resolution
by name is a query, not an identity (D-6).

5.2. The **core relation types** are the engine's. An implementation MUST
support them with the semantics given, and an instance MUST NOT disable
them. Instances MAY define further types; their names MUST contain a `.`
(`axonibyte.decided-by`) so they cannot collide with the core set.

| Relation | Subject → object | Meaning |
|---|---|---|
| `names` | concept → literal | The concept is called this. |
| `is-about` | blob or concept → concept | The subject concerns the object. The relation a path query walks. |
| `cites` | blob or concept → blob or concept | The subject rests on the object as evidence. |
| `contradicts` | blob or concept → blob or concept | The subject disagrees with the object; both stand. |
| `supersedes` | blob or concept → blob or concept | The subject replaces the object as current; the object remains readable as history. |
| `retracts` | edge → edge | The subject withdraws the object edge (section 7). |
| `decided-by` | blob or concept → principal | The subject was decided by that principal. |
| `wrote` | writer → blob | The writer authored the blob. Emitted on every blob's first appearance. |
| `in-scope` | blob or concept → scope | The subject's recipient set (section 9). |
| `key-of` | writer → principal | The writer key belongs to the principal (section 8). |

5.3. Every edge's `w` and `t` are provenance; `wrote` and `decided-by` are
the explicit forms a reader queries. A blob with no `wrote` edge is an
anomaly a reader MUST report, not an error that stops reading.

5.4. **Currency.** Among a set of blobs related by `supersedes`, the current
one is the one no un-retracted `supersedes` edge names as object. The
chain, in both directions, is history and MUST remain queryable.

## 6. The storage contract

6.1. The engine speaks to storage through two contracts, so that backends
may change behind them (D-10).

**Store** (truth):
- `PutBlob(bytes) → identity`; idempotent.
- `GetBlob(identity) → bytes`; MUST verify the hash before returning.
- `Append(edge) → seq`; MUST refuse a non-contiguous `seq` and an invalid signature.
- `ReadLog(from seq) → stream of edges`.

**Index** (derived):
- `Rebuild(ReadLog(1))`.
- `Query(expression) → identities`, section 11.
- `At() → seq`: the log position the index reflects.

6.2. The **file backend** is the reference store and the **archival form**.
Every store, whatever backend it runs on, MUST be exportable to the file
backend's layout, and an export MUST be byte-identical in its log to the
source's log. A conformant implementation MUST read the file layout.

6.3. SQLite is the reference index. The index MUST be reconstructible from
the log alone and MUST be treated as disposable; if the index and the log
disagree, the log is right.

6.4. A cache is derived from either and MAY be discarded at any time. A
cached query result MUST carry the `seq` it was computed at and MUST be
treated as stale when the log has moved past it (D-11).

## 7. Retraction and removal

7.1. An edge is withdrawn by a `retracts` edge naming it. A reader MUST treat
a retracted edge as absent for currency and query, and MUST keep it
readable as history. Retraction is itself an edge: who retracted what, and
when, is in the log.

7.2. A blob is **unreferenced** when every edge naming it is retracted.

7.3. Physical removal of an unreferenced blob's bytes is a store-admin
operation, never a write. It MUST be recorded by an edge of core type
`removed` (writer: the admin's key; subject: the blob identity; attribute
`reason`), and MUST refuse a blob that is still referenced.

7.4. Compaction of the log MAY rewrite segment files but MUST preserve every
edge, every `seq` and every `sig`. A compaction MUST be recorded by an edge
of core type `compacted` carrying the hash of the log before and after.

## 8. Writers, principals and signatures

8.1. A **writer** is an Ed25519 key pair. Its id is `k:` followed by the
lowercase hex SHA-256 of the public key.

8.2. A **principal** is a concept with at least one `key-of` edge from a
writer. One principal holds one writer per machine (D-8); a principal that
writes from a second machine registers a second key. Key rotation is a new
`key-of` edge plus a `supersedes` edge from the new writer to the old.

8.3. The **signature** is over the canonical form: the edge's JSON with keys
in the order of section 4.2, no whitespace, `sig` omitted, UTF-8. A store
MUST verify every appended signature against a `key-of`-registered writer
or, for the first edges of a new store, against the store's founding writer
named in its configuration.

8.4. A reader MUST be able to answer "which principal held this key, and
when" from the log alone.

8.5. Subagents and tools are not writers. They act under a writer's key and
MAY be named in the edge's `a.via` attribute. Responsibility is the
writer's.

8.6. Sessions of one principal on one machine share that machine's key, so a
later session is the same writer.

## 9. Scopes and encryption

9.1. A **scope** is a concept naming a recipient set. Membership is edges of
core type `member` from the scope to writers' public keys (as literals). A
scope's membership history is the log's.

9.2. Every blob and every concept in an encrypting store MUST have exactly
one `in-scope` edge current. A store MAY run unencrypted, in which case
`in-scope` is advisory and section 9.3 does not apply.

9.3. **Envelope encryption.** In an encrypting store a blob's bytes are
encrypted with a fresh random data key (XChaCha20-Poly1305; large blobs as
chunks under one data key with distinct nonces), and the data key is wrapped
to each member of the blob's scope at write time (X25519 from the member's
Ed25519 key, HKDF-SHA256). The wrapped keys are stored beside the blob in a
header the file backend writes as `blobs/<..>/<..>.keys`. A reader can read
a blob if and only if a data key is wrapped to a key it holds. **The
recipient set is the access control** (D-12); permission bits do not
survive replication and are not part of this specification.

9.4. In an encrypting store, a blob's identity is `hmac256:` followed by
HMAC-SHA256 over the plaintext under the store key, so that equality of two
blobs is visible only to holders of the store key (D-13). The ciphertext's
SHA-256 is stored beside it for integrity.

9.5. Revocation rotates the scope: a new `member` set, a `supersedes` edge
from the new scope concept to the old, and future blobs wrapped to the new
set. What was read cannot be unread; the log says when the set changed.

9.6. Edges are in the clear within a store by default. An instance MAY
encrypt the `o` and `a` fields of an edge to its scope; `rel`, `s`, `w`,
`t`, `seq` and `sig` MUST stay in the clear so the log's structure remains
auditable and replicable.

9.7. Auditability is a recipient. An organization that requires audit adds
its audit key to the scopes it governs; the choice is in the log as `member`
edges, readable by anyone who holds the log.

9.8. Key custody is the recipient's. 0.1 defines one recipient type, a
software key in a file. A hardware-sealed key (TPM 2.0, PKCS#11) is a second
recipient type with the same interface and is reserved for a later version
(section 14).

## 10. The derived index

10.1. The index holds, at minimum: concepts by id and by name, edges by
subject, by object, by relation and by writer, currency per `supersedes`
chain, retraction state, and blobs by scope. Its schema is an implementation
matter and is not part of this specification.

10.2. An index MUST expose `At()` and MUST refuse to answer a query with a
staler `At()` than the store's log head when asked for a consistent answer.

## 11. The query

11.1. A **query** is a conjunction of terms, each term a relation and an
object: `is-about=c:…`, `cites=sha256:…`, `in-scope=c:…`, or a name term
`name=<literal>` which resolves through `names`. A query returns the set of
subjects (blobs and concepts) for which every term holds on a current,
un-retracted edge.

11.2. A term MAY be followed by a traversal of bounded depth: `supersedes^`
(the current head of the chain), `supersedes*N` (ancestors to depth N),
`cites*N`. Depth is bounded; unbounded traversal is not in 0.1.

11.3. Textual form, used by the mount and the protocol: terms separated by
`/`, each `rel=object`, traversal suffixed; `name=` terms may be written as
the bare name when unambiguous. The same grammar is the mount's path grammar
(section 12).

11.4. Nothing in the query is probabilistic. A query is a fact about the
log at a `seq`.

## 12. The mount

12.1. A mount presents a store as a filesystem over FUSE. **The path is the
query**: `/<term>/<term>/…/` is a directory listing the blobs the
conjunction selects, each named by a stable, human-readable name derived from
its `names` edges or, lacking those, its identity. Directories exist for
every term that would narrow the listing; a directory with no results is
empty, not absent, so a traversal never fails for want of a path.

12.2. Reading a file reads the blob (through the manifest for large blobs;
through decryption in an encrypting store, if the mount holds a key).

12.3. **Writing** a new file under a path appends the blob and emits edges:
`wrote` from the mount's writer, and one `is-about` (or the term's relation)
edge for each term in the path (D-7). Writing over an existing name appends
a new blob and a `supersedes` edge to the old; nothing is overwritten.
Deleting emits `retracts` edges for the current `is-about` edges of that
blob under that path and nothing else.

12.4. `/.cairn/` is reserved: `log/`, `by-seq/`, `writers/`, `scopes/` and
`at` expose the store's own structure read-only.

12.5. A mount's executable bit is a mount option bound to a scope, off by
default (D-15). When on, the bit is presented from the attribute
`a.mode=exec` on a current `is-about` edge, and the mount MUST refuse to
present it for blobs outside the configured scope.

## 13. The protocol

13.1. Replication is HTTP and JSON. A store exposes:

- `GET /cairn/v1/head` → `{"seq":N, "store":"<id>"}`
- `GET /cairn/v1/log?from=N` → edges from `seq` N, newline-delimited JSON, streaming.
- `GET /cairn/v1/blob/<identity>` → bytes, with the keys header if encrypted.
- `POST /cairn/v1/append` → one edge; the store validates `seq`, `sig` and the writer's registration. Only the store's writer MAY append.
- `PUT /cairn/v1/blob/<identity>` → bytes; verified against the identity.

13.2. A replica pulls: it reads `head`, fetches `log?from=` its own head,
verifies every signature, appends locally, then fetches the blobs it lacks.
A replica is a reader; it never appends to the source. Single writer per
store, many replicas (D-4).

13.3. Authentication is mutual: requests are signed by the caller's writer
key (HTTP message signature over method, path, date and body hash) and the
server admits callers on an allow-list of keys that is itself `member`
edges of a reserved scope `readers`. Encryption is the first line of access
control; the allow-list is the second.

13.4. A store's id is the SHA-256 of its first edge's canonical form.

## 14. Out of scope for 0.1, with attachment points

- **Natural-language resolution** (describing a concept in English and
  getting blobs): a layer above the query that emits queries. It attaches
  at section 11 and MUST produce a query a reader can inspect; it MUST NOT
  write edges on its own.
- **Embedding indexes**: a derived index under section 10, rebuildable,
  never a source of relations.
- **Multi-writer merge**: not planned. Many stores and cross-store `cites`
  are the design's answer.
- **Hardware recipients** (TPM 2.0, PKCS#11): a second recipient type under
  section 9.8.
- **Block-level backends**: a store behind section 6.1, exportable to the
  file form.

## 15. Conformance and the archival guarantee

15.1. An implementation is conformant when it reads and writes the file
backend's layout, verifies hashes and signatures as specified, supports the
core relations with their semantics, answers the query of section 11 from a
rebuilt index, and exports any store it runs to the file layout with an
identical log.

15.2. **The archival guarantee.** Given the file layout and no software but
a shell, a reader MUST be able to list every edge in order, find every blob
by identity, verify every blob's hash, and follow `supersedes` and `retracts`
by reading lines. A conformant implementation MUST include a test that does
exactly this with `cat`, `sha256sum` and a JSON tool.

## 16. Predictions

Written before the first implementation so that the record can say whether
the design did what it claimed. Each fails on its own clause.

- **P-1, the fresh reader.** A session that has never seen the store, told
  only the mount point, finds a named fact (the store's own founding
  principal) and the current version of a superseded blob by traversal
  alone, without an index file written by hand. Fails if either needs a
  path a person had to remember.
- **P-2, the archive.** With cairn software removed, a shell script using
  `cat`, `sha256sum` and `jq` recovers every blob and every current edge of
  a store of ten thousand edges in under a minute on the reference machine.
  Fails if any edge or blob is unreachable.
- **P-3, the rebuild.** Deleting the index and rebuilding from the log
  yields identical answers to a fixed set of queries. Fails on one
  difference.
- **P-4, the replica.** A second machine pulling a store over the protocol
  answers the same queries as the source at the same `seq`, and refuses an
  edge with a forged signature. Fails on either.
- **P-5, encryption's cost.** Encrypting a store of ten thousand kilobyte
  blobs costs under 5 percent in bytes and under 10 percent in write wall
  time against the same store unencrypted on the same machine. Fails above
  either bound.
- **P-6, the mount's write.** Writing a file into `/is-about=X/is-about=Y/`
  yields exactly two `is-about` edges and one `wrote`, and writing it again
  yields one `supersedes` and no overwrite. Fails on any other count.
