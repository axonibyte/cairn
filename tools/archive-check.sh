#!/bin/sh
# archive-check.sh -- SPEC §15.2, the archival guarantee, with no software
# but a shell, sha256sum and jq: list every edge in order, verify that each
# line is its own canonical form, that the chain holds from the founding
# hash through every line, find every blob by identity and verify its hash,
# and follow supersedes and retracts by reading lines. This is the second
# implementation of the format (TESTING tier 2); it is run by the Go suite
# on a store the command made, and it is the tool a reader with a dead disk
# and nothing else will have.
#
#   archive-check.sh <store root>            check a store
#   archive-check.sh --vectors <vectors.json> check the vector file's forms
#
# Exit 0 when everything holds; 1 with the first failure named; 2 usage;
# 3 when jq or sha256sum is missing, named, with the store unexamined.
set -u

# preflight: this script exists to be believed when nothing else is left, so
# a missing tool is named as its own failure and never blamed on the store
# (quill, coop main/1012 finding 10)
for tool in jq sha256sum; do
  command -v "$tool" >/dev/null 2>&1 || { echo "archive-check: FAIL: $tool is not on PATH; the store was not examined" >&2; exit 3; }
done

canon='{seq,t,w,prev,rel,s}
  + (if has("o") then {o} else {} end)
  + (if has("a") then {a: (.a | to_entries | sort_by(.key) | from_entries)} else {} end)
  + (if has("sig") then {sig} else {} end)'

sha() { sha256sum "$1" | cut -c1-64; }
shastr() { printf '%s' "$1" | sha256sum | cut -c1-64; }
fail() { echo "archive-check: FAIL: $*" >&2; exit 1; }

check_vectors() {
  f="$1"
  n=$(jq '.edges | length' "$f")
  i=0
  prev=$(jq -r .founding "$f")
  # jq -j writes the config's bytes exactly, trailing newline included, where
  # $(...) would strip it and change the hash
  [ "sha256:$(jq -j .config "$f" | sha256sum | cut -c1-64)" = "$prev" ] || fail "founding hash is not the config's"
  while [ "$i" -lt "$n" ]; do
    written=$(jq -r ".edges[$i].written_form" "$f")
    hash=$(jq -r ".edges[$i].hash" "$f")
    # the written form re-rendered by jq is the written form: §4.2a is realizable with jq
    re=$(printf '%s' "$written" | jq -c "$canon")
    [ "$re" = "$written" ] || fail "vector edge $((i+1)): jq re-rendering differs:
  jq:   $re
  file: $written"
    [ "sha256:$(shastr "$written")" = "$hash" ] || fail "vector edge $((i+1)): hash differs"
    [ "$(printf '%s' "$written" | jq -r .prev)" = "$prev" ] || fail "vector edge $((i+1)): prev is not the previous hash"
    prev=$hash
    i=$((i+1))
  done
  echo "archive-check: vectors: $n edges re-rendered and chained with jq and sha256sum"
}

check_store() {
  root="$1"
  [ -f "$root/cairn.json" ] || fail "no cairn.json under $root"
  prev="sha256:$(sha "$root/cairn.json")"
  seq=0
  seals=0
  lastseal=0
  tmp=$(mktemp -d) || fail "mktemp"
  trap 'rm -rf "$tmp"' EXIT
  for seg in $(ls "$root"/log/*.jsonl 2>/dev/null | sort); do
    # one jq pass re-renders every line of the segment into its canonical
    # form, and one more pulls the fields the chain and the seals need, so a
    # store of ten thousand edges costs two jq processes per segment and one
    # sha256sum per line rather than six processes per line
    jq -c "$canon" "$seg" > "$tmp/canon" 2>/dev/null || fail "$seg: not JSON at or after seq $((seq+1))"
    # an absent o or sig is written as "-", never empty: a tab-separated read
    # in sh collapses adjacent tabs and would shift the fields
    jq -r '[.seq, .prev, .rel, .s, (.o // "-"), (.sig // "-:" | split(":")[0])] | @tsv' "$seg" > "$tmp/fields" 2>/dev/null || fail "$seg: fields"
    exec 3< "$tmp/canon" 4< "$tmp/fields"
    while IFS= read -r line || [ -n "$line" ]; do
      seq=$((seq+1))
      IFS= read -r re <&3 || fail "seq $seq: no canonical rendering"
      IFS="$(printf '\t')" read -r s p rel sub obj suite <&4 || fail "seq $seq: no fields"
      [ "$re" = "$line" ] || fail "seq $seq: the line is not its own canonical form"
      [ "$s" = "$seq" ] || fail "seq $seq: the line says seq $s"
      [ "$p" = "$prev" ] || fail "seq $seq: prev $p, chain is at $prev"
      prev="sha256:$(shastr "$line")"
      case "$rel" in
        seal) seals=$((seals+1)); lastseal=$seq
              [ "$sub" = "edge:$((seq-1))" ] || fail "seq $seq: a seal's subject is edge:$((seq-1)), got $sub"
              [ "$suite" = "mldsa65" ] || fail "seq $seq: a seal must be signed by mldsa65" ;;
        supersedes) echo "  supersedes: $sub -> $obj" ;;
        retracts)   echo "  retracts:   $sub -> $obj" ;;
      esac
    done < "$seg"
    exec 3<&- 4<&-
  done
  blobs=0
  for f in $(find "$root/blobs" -type f 2>/dev/null); do
    blobs=$((blobs+1))
    name=$(basename "$f"); name=${name%.manifest}
    dir=$(basename "$(dirname "$f")")
    [ "$(sha "$f")" = "$dir$name" ] || fail "blob $dir$name: bytes do not hash to the name ($f)"
    case "$f" in *.manifest)
      size=$(jq -r .size "$f")
      got=0
      for c in $(jq -r '.chunks[]' "$f"); do
        hx=${c#sha256:}; cf="$root/blobs/$(printf '%s' "$hx" | cut -c1-2)/$(printf '%s' "$hx" | cut -c3-)"
        [ -f "$cf" ] || fail "manifest $dir$name: chunk $c missing"
        got=$((got + $(wc -c < "$cf")))
      done
      [ "$got" = "$size" ] || fail "manifest $dir$name: says size $size, chunks hold $got" ;;
    esac
  done
  echo "archive-check: $root: $seq edges chained from cairn.json, $seals seals (last at seq $lastseal), $blobs blob files verified"
}

case "${1:-}" in
  --vectors) [ -n "${2:-}" ] || { echo "usage: archive-check.sh --vectors <vectors.json>" >&2; exit 2; }; check_vectors "$2" ;;
  "") echo "usage: archive-check.sh <store root> | --vectors <vectors.json>" >&2; exit 2 ;;
  *) check_store "$1" ;;
esac
