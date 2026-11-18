#!/bin/sh
# The guest's one-time setup, run by `reaper build` as root on the FreeBSD
# guest. Idempotent. The template ships no Go toolchain, so this installs it
# from the FreeBSD package mirror into the cache that survives runs, and
# refuses by name when the toolchain is still missing or too old afterwards:
# a template gap must read as a template gap, never as a failure of the tree.
set -eu
log() { printf '%s bootstrap: %s\n' "$(date -u +%H:%M:%SZ)" "$*"; }

log "host: $(uname -rs) $(freebsd-version -u 2>/dev/null || true)"
PKG_CACHE="${REAPER_CACHE_PKG:-/reaper/cache/pkg}"
mkdir -p "$PKG_CACHE"
export PKG_CACHEDIR="$PKG_CACHE"
export ASSUME_ALWAYS_YES=yes
if ! command -v pkg >/dev/null 2>&1 || ! pkg -N >/dev/null 2>&1; then
  log "bootstrapping pkg"; pkg bootstrap -y
fi

# go.mod names the minimum; the guest's go must meet it. FreeBSD ships one
# package per Go minor (lang/go127 is /usr/local/bin/go127) beside a `go`
# metaport that may lag it, so the versioned package named by go.mod is
# what gets installed, and `go` is pointed at it when the metaport's is
# older or absent. Read versions as major.minor and compare numerically,
# so 1.27 is not read as older than 1.9.
want=$(sed -n 's/^go \([0-9]*\.[0-9]*\).*/\1/p' go.mod)
wmaj=${want%%.*}; wmin=${want#*.}
versioned="go${wmaj}${wmin}"
meets() { # meets <go binary>: true when its version is at least go.mod's
  have=$("$1" version 2>/dev/null | sed -n 's/.*go\([0-9]*\.[0-9]*\).*/\1/p')
  [ -n "$have" ] || return 1
  hmaj=${have%%.*}; hmin=${have#*.}
  [ "$hmaj" -gt "$wmaj" ] || { [ "$hmaj" -eq "$wmaj" ] && [ "$hmin" -ge "$wmin" ]; }
}
if ! command -v go >/dev/null 2>&1 || ! meets go; then
  log "installing $versioned (go.mod wants $want)"
  pkg update -q >/dev/null 2>&1 || true
  pkg install -y "$versioned" >/dev/null || pkg install -y go >/dev/null || true
  if command -v "$versioned" >/dev/null 2>&1 && ! meets go 2>/dev/null; then
    ln -sf "$(command -v "$versioned")" /usr/local/bin/go
    log "go -> $versioned"
  fi
fi
command -v go >/dev/null 2>&1 || { log "REFUSED: no go after pkg install $versioned; the package mirror or the template is the gap"; exit 2; }
# the archival script (SPEC §15.2) is part of the suite and needs jq
if ! command -v jq >/dev/null 2>&1; then
  log "installing jq"
  pkg install -y jq >/dev/null
fi
command -v jq >/dev/null 2>&1 || { log "REFUSED: no jq after pkg install jq; the archival arm cannot run without it"; exit 2; }
log "jq: $(jq --version)"
meets go || { log "REFUSED: $(go version) on the guest is older than go.mod's $want and $versioned is not in the mirror"; exit 2; }
log "go: $(go version)"
log "done"
