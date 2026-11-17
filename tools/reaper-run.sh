#!/bin/sh
# The run half of the reaper loop: the whole suite, as CI runs it, with the
# result written where `reaper` collects it. The exit status is the suite's.
set -eu
export GOMODCACHE="${REAPER_CACHE_GOMOD:-$HOME/go/pkg/mod}"
export GOCACHE="${REAPER_CACHE_GOBUILD:-$HOME/.cache/go-build}"
mkdir -p out
go test -count=1 -timeout 30m ./... > out/test.log 2>&1 || rc=$?
cat out/test.log
exit "${rc:-0}"
