#!/bin/sh
# The build half of the reaper loop: bootstrap the guest (idempotent; installs
# Go from packages and refuses by name if it is still missing or too old),
# then build and vet.
set -eu
sh tools/reaper-bootstrap.sh
export GOMODCACHE="${REAPER_CACHE_GOMOD:-$HOME/go/pkg/mod}"
export GOCACHE="${REAPER_CACHE_GOBUILD:-$HOME/.cache/go-build}"
go build ./...
go vet ./...
