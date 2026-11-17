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

if ! command -v go >/dev/null 2>&1; then
  log "installing go"
  pkg update -q >/dev/null 2>&1 || true
  pkg install -y go >/dev/null
fi
command -v go >/dev/null 2>&1 || { log "REFUSED: no go after pkg install go; the package mirror or the template is the gap"; exit 2; }

# go.mod names the minimum; the guest's go must meet it. Read both as
# major.minor and compare numerically, so 1.27 is not read as older than 1.9.
want=$(sed -n 's/^go \([0-9]*\.[0-9]*\).*/\1/p' go.mod)
have=$(go version | sed -n 's/.*go\([0-9]*\.[0-9]*\).*/\1/p')
wmaj=${want%%.*}; wmin=${want#*.}; hmaj=${have%%.*}; hmin=${have#*.}
if [ "$hmaj" -lt "$wmaj" ] || { [ "$hmaj" -eq "$wmaj" ] && [ "$hmin" -lt "$wmin" ]; }; then
  log "REFUSED: go $have on the guest is older than go.mod's $want"; exit 2
fi
log "go: $(go version)"
log "done"
