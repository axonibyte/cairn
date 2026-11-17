#!/bin/sh
# The build half of the reaper loop. Refuses by name when the guest lacks Go,
# because a missing toolchain must read as a template gap and not as a
# failure of the tree.
set -eu
if ! command -v go >/dev/null 2>&1; then
  echo "cairn: no go toolchain on this guest (pkg install go); template gap, not a tree failure" >&2
  exit 3
fi
export GOMODCACHE="${REAPER_CACHE_GOMOD:-$HOME/go/pkg/mod}"
export GOCACHE="${REAPER_CACHE_GOBUILD:-$HOME/.cache/go-build}"
go build ./...
go vet ./...
