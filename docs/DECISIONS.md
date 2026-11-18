# cairn design decisions

The numbered record of what was decided, by whom, and why, with the
alternatives refused. A decision is not changed by editing it: a later
decision supersedes it and says so, and both stay. The specification cites
these as D-n. Dates are UTC.

Who decides: **cal** is Caleb L. Power, who owns the project and the
repository. **Claude** is the agent the store is first built for and the
author of the specification; the house's record names it by that name. A
decision marked *both* was proposed by one and taken by the other.

---

**D-1, 2026-11-16. Everything is a concept, not a file.** *cal.* A file has
one path; a concept has as many relations as it has, and the thing you want
is found by describing it. The store's unit is the relation, not the
location. Refused: a tagging layer over an ordinary filesystem, which keeps
the path as the identity.

**D-2, 2026-11-17. The name is Cairn.** *Claude, at cal's invitation.* Built
one stone at a time by whoever passes, a waymark for the one who comes after,
standing without its builders, the arrangement the message. Acronym if
wanted: Content-Addressed Index of Relations and Notes. Refused: SUDS and
SADL (cal's first candidates), which undersold the thing with "semi".

**D-3, 2026-11-17. The resolver is not a model.** *Claude, taken by cal.*
Relations are stored explicitly and model-independently. Any embedding
index is derived and rebuildable. A store meant to outlive a session and a
model cannot have its meaning live in one model's embedding space, where a
model change silently changes what "the blob about X" means. Refused:
embedding-first retrieval as the primary mechanism.

**D-4, 2026-11-17. Single writer per store, replicated by pulling the
append-only log.** *Claude, taken by cal.* Many stores and many readers
cover the multi-machine case; multi-master merge of a memory is a problem
not worth owning. Refused: multi-master replication.

**D-5, 2026-11-17. Concepts are nodes with typed edges; corrections are
edges.** *Claude, taken by cal.* The core relations are supersedes, cites,
contradicts, is-about, decided-by, with time and authorship intrinsic to
every edge. Nothing is overwritten; a correction links to what it corrects.
Refused: freeform tags without types; mutable attributes on a node.

**D-6, 2026-11-17. Concepts have ids; names are edges.** *Claude.* "cal" the
person and "cal" the command never collide, and a rename is append-only like
everything else. Refused: concepts identified by their string.

**D-7, 2026-11-17. The mount is virtual and the path is the query; writes
take their edges from the path.** *Claude, taken by cal.* A tag graph has no
unique names, so the filesystem view is a directory per conjunction. Writing
into a path emits the path's terms as edges. Identity is the content hash,
so retagging never moves data. Refused: a fixed hierarchy mirrored onto the
graph.

**D-8, 2026-11-17. The writer is a key; one key per principal per machine;
subagents are not writers.** *Claude, agreed by cal.* The key establishes
responsibility, as a commit's author does in this house; authorship is a
mark (`via`). Sessions of one agent on one machine share its key, so a
successor session is the same writer. A principal on a second machine is a
second key under the same principal node, so provenance says who and where.
Refused: one key per session; one key per agent across machines.

**D-9, 2026-11-17. Engine and instance are distinct; nothing about any
user is hard-coded; the core relations are not configurable off.** *cal,
sharpened by Claude.* The specification defines the engine; a store is a
configured instance. The one exception runs the other way: supersession,
authorship and time on every edge, and the archival file form are the
engine's, because a store that can turn them off is not this store.

**D-10, 2026-11-17. The storage layer is a seam; the file backend is the
reference and the archival form.** *cal raised the concern, Claude proposed
the seam, both took it.* cal's concern was speed and scale across machines in
a future of larger agents. The resolution: a backend contract so the fast
path can change, with the file layout as the form every store can be dumped
to, so the durability property is never lost. Refused: a block-level
filesystem as the first implementation, because its on-disk format is
readable only by its own driver, and a driver is the kind of thing that
stops building.

**D-11, 2026-11-17. Caches are a third kind of thing, never a second
truth.** *cal asked, Claude answered, both took it.* Store, index, cache.
A cache (Valkey or any other) sits in front of the index or blob reads and
is tagged with the log `seq` it was computed at. Content addressing and the
append-only log make invalidation structural: nothing mutates. Not in the
first version; the seam admits it.

**D-12, 2026-11-17. Encryption is per store, envelope with recipients; the
recipient set is the access control.** *cal asked for configurability and
raised auditability; Claude proposed envelope encryption; both took it.*
Permission bits do not survive replication onto hosts the reader does not
control; cryptographic access does. Scopes name recipient sets, membership
is edges, and an organization that requires audit adds its audit key as a
recipient, with the choice visible in the log. Honest limit, stated by both:
a key in use on a host is readable by that host's root; what encryption
buys is secrecy from everyone else and a deliberate act in place of an
incidental one.

**D-13, 2026-11-17. In an encrypting store, identity is a keyed hash of the
plaintext.** *Claude.* Ciphertext hashing loses deduplication across
recipient sets; plaintext hashing leaks equality to readers without the key.
A keyed hash under the store key gives equality only to the store's holders.

**D-14, 2026-11-17. Deletion is retraction; removal is an admin act that
requires every reference retracted and is itself logged.** *Claude
proposed, cal agreed.* Append-only meets the right to forget without a
reader ever seeing a hole it cannot explain.

**D-15, 2026-11-17. Executables are stored like any blob; the mount's exec
bit is a scope-bound option, off by default.** *cal asked, Claude answered,
both took it.* A hash-identified, signed, superseded binary is a versioned
verifiable artifact for free, and a store meant to carry an agent across
machines should carry its tools. The same property makes a replicated store
with exec a software distribution channel, so a compromised writer key
would be code execution on every replica; hence off by default and on only
for scopes whose writers are trusted to ship code. Named limit: the bit
cannot stop an interpreter from running a script read as data; the real
control is writer trust.

**D-16, 2026-11-17. Go for the reference implementation.** *Claude, with
cal's question answered honestly and agreed.* One static binary that
cross-compiles to FreeBSD and Linux with no runtime; FUSE bindings on both;
a plainness a successor or another implementer can read. Costs named: no
sum types, so typed edges are constants with validation; the pure-Go SQLite
is slower than the C library. The spec is the artifact; a second
implementation in another language is a design goal, not a fear.

**D-17, 2026-11-17. The reference index is SQLite over an append-only JSONL
log, and the log is the truth.** *Claude.* Readable with cat; the index
disposable and rebuildable. Refused: SQLite as the store.

**D-18, 2026-11-17. Licensing by kind.** *cal's standing for Axonibyte
projects, agreed by Claude.* Source under the Blue Oak Model License 1.0.0;
language and protocol specifications under the Open Web Foundation
Agreement 1.0; documents and diagrams under CC-BY-4.0. An open format is
part of durability: no one needs permission to read the store. A user's
contents are theirs and private; the framework is open.

**D-19, 2026-11-17. Commits are authored by the repository owner.** *cal,
agreed by Claude.* A commit establishes responsibility, not authorship; this
file and the build logs are where authorship lives.

**D-20, 2026-11-17. Hardware key custody is a recipient type for a later
version.** *cal raised TPM; Claude placed it.* A key sealed to a host's TPM
is usable there and not exportable, the first real improvement to the
honest limit in D-12. The recipient interface is defined in 0.1 with one
software implementation so that TPM and PKCS#11 are additions, not changes.

**D-21, 2026-11-17. Prior art is read and credited: Perkeep, TMSU, XTDB.**
*Claude.* Perkeep for content-addressed blobs with signed claims and a FUSE
mount; TMSU for the path as a tag conjunction; XTDB for bitemporal records.
cairn differs in typed append-only relations with supersession, the log as
the store, and the specification for a reader who was not there.

**D-22, 2026-11-17. Post-quantum cryptography from 0.1, hybrid for
confidentiality, sealed chain for provenance, with suite agility.** *cal
raised it (NIST's direction; BoneMesh already does it); Claude designed the
shape; both took it.* Key wrapping uses HPKE under the hybrid ML-KEM-768
plus X25519 KEM, because a harvested archive is the threat a long-lived
store faces and there is no reason to ship a classical-only wrap. Signatures
stay Ed25519 per edge for size (64 bytes against 3,309), and the log is a
hash chain sealed with ML-DSA-65 at every segment roll and on demand, so an
adversary who later breaks Ed25519 can forge an edge's tag but cannot alter
history under a seal. Instances that want per-edge post-quantum signatures
set the suite and pay the size. Every key, signature and wrap names its
suite so the format can move again. Both primitives are in Go 1.27's
standard library (crypto/hpke, crypto/mldsa), so this costs no dependency.
Refused: classical-only (fails the archive); per-edge ML-DSA by default (a
tenfold log for a threat the seal already covers). *Refined the same hour
on cal's question whether these are the accepted NIST standards:* ML-KEM is
FIPS 203 and ML-DSA is FIPS 204, both final since August 2024; HPKE is an
IETF standard (RFC 9180) composing NIST primitives, its hybrid KEM suite an
IETF draft Go has shipped; the AEAD moves from ChaCha20-Poly1305, an IETF
cipher, to AES-256-GCM, which is FIPS-approved, so that every primitive is a
NIST standard and the IETF parts are only the composition.

**D-23, 2026-11-17. The test method is a portfolio of oracles.** *cal asked
that it be explicit; Claude mapped the tiers onto cairn; both took it.* The
method of Axonibyte's "A Portfolio of Oracles" governs this repository:
every tier kept must answer a question no cheaper tier can, invariants are
self-tested against a broken store before they are trusted, new assertions
are mutation-checked, and the acceptance of the whole is rediscovering
known defects with their fixes reverted. The mapping is in
[TESTING.md](../TESTING.md). Two tiers are cairn's own strongest: cross-
implementation vectors, because the specification exists to be implemented
by others and the archival shell script is already a second implementation;
and simulated writers with a shadow model, because the defects an append-
only store hides are the ones that appear only after history accumulates.

**D-24, 2026-11-18. Encodings of keys and signatures are lowercase hex with
the suite in front; a key's secret is its 32-byte seed; ML-DSA signs
deterministically.** *Claude.* SPEC §8.1 names the suites and the sizes but
not the bytes on the wire, and M1's first signing code had to choose. A key
id is `k:<suite>:` and the hex SHA-256 of the public key's bytes; a public
key travels as `<suite>:<hex>`; a signature is `<suite>:<hex>` of the raw
signature (64 bytes for ed25519, 3,309 for mldsa65). Hex rather than base64
so that a reader with a shell and no library can tell two keys apart and a
vector file is unambiguous; the cost, a log line half again as long per
signature, is paid in the per-edge suite that is 64 bytes. The secret a key
file holds is the 32-byte seed of either suite, from which the pair is
rebuilt, so one key file format serves both. ML-DSA-65 signatures are made
deterministically (FIPS 204's deterministic variant) so the vector file can
hold expected signatures for both suites; the hedged variant is a later
suite id if ever wanted. Refused: base64 (compact, but two encodings of one
key look different to grep); storing expanded private keys (two formats).

**D-25, 2026-11-18. Canonical JSON is specified to the byte, and an
attribute value is a string, a 64-bit integer, a boolean, null or an array
of those.** *quill found the gap (coop main/993, four rules the code carried
that the document did not); Claude decided.* The first signing code had
closed the gap four times on its own -- sorting `a`, preserving number
literals as written, writing `<&>` raw, taking the last of duplicate keys --
and a second implementation in shell could have disagreed at every one
while producing an edge that looked valid. SPEC §4.2a now states the key
order, the sorting of `a` at every depth, the exact string escapes, the
integer form, the refusal of fractional and exponent numbers and of
duplicate keys, and names the two forms the code already distinguished: the
signing form (no `sig`) and the written form (with `sig`, the line, the
chain's input). Fractional numbers are refused rather than canonicalized
because no two JSON tools agree on how to print a float; an instance that
needs one stores it as a string or a literal. Refused: leaving numbers to
the writer's tool (a signature that depends on the tool); base-10 floats
with a fixed precision (still two tools, two answers). *Refined the same
day by the archival script's first run:* the five control characters with
short JSON escapes are written as `\b \f \n \r \t`, not `\u00XX`, because
that is what jq writes and the tab in the vector file was the first byte
the two implementations disagreed on -- tier 2 doing its work.

**D-26, 2026-11-18. A manifest is told from a blob by its file name, never
by its bytes, and is not subject to the bound.** *quill found it (main/993,
findings 3 and 4); Claude decided.* The first blob store recognized a
manifest by sniffing its prefix, so a user's 123-byte document that was a
valid manifest came back from Get as its chunk's bytes -- Get(Put(x)) was
not x, which is the one property content addressing exists to give -- and a
manifest larger than the bound was itself stored flat against §3.3's
letter. SPEC §3.3 now stores manifests at `<path>.manifest`, forbids
telling them from blobs by content (§3.5: a blob carries no metadata), and
exempts the manifest file from the bound; a manifest whose chunks do not
resolve to its stated size is refused by name. Refused: a `manifest` core
relation (an edge for every large blob, and a reader of the file layout
would need the log to read a blob); a distinct identity prefix (the large
blob's identity must stay the manifest's hash so references do not fork).

**D-27, 2026-11-18. A seal signs its own signing form, which carries the
chain hash as `prev`; the founding hash is the configuration file's.**
*Claude, building the log.* §8.7 said a seal's `sig` was "over the chain
hash at that point", which would have made the seal the one edge whose
signature is not over its signing form and so the one edge §8.3's rule
could not verify uniformly. Since a seal's subject is the last edge and its
`prev` is that edge's hash, the chain hash is already inside the seal's
signing form, and signing the form as every edge is signed attests it with
no second rule. The first edge's `prev`, "the store's founding hash", is
now defined: the SHA-256 of `cairn.json` as first written, so the
configuration that names the founder is itself under the chain and a store
whose configuration was swapped reads as broken at `seq` 1. Also fixed: the
store id of §13.4 is over the written form, the vocabulary of D-25. Refused:
a detached seal file beside the log (a second thing to lose); the seal as
the first line of the next segment (then a segment can end unsealed).

**D-28, 2026-11-18. A machine's writer keys live in `.keys/` under the
store root, mode 0700 with 0600 files holding seeds, never served by the
protocol and never part of an export; a machine without them reads and
verifies but cannot write.** *Claude, building the CLI.* The alternatives
were a per-user configuration directory (then a store copied to another
path loses its writer silently and two stores on one machine fight over
one key) and a key inside the store's served tree (then replication ships
the secret). A dot-directory beside `blobs/` and `log/` keeps the key with
the store it writes, keeps it out of §13's served paths and §6.2's export
by rule rather than by care, and makes "this machine holds no key" a fact
`Open` can state. `cairn init` also writes the founder's two `key-of` edges
as the store's first two lines, so a reader learns the founder's keys from
the log alone (§8.4) and the configuration file is only the founding hash's
subject. Refused: keys in `~/.config` (the store and its writer part ways
on copy); an unregistered founder (the configuration would then be the only
record of a key).

