#!/bin/sh
# The repository's gates: build, vet, test and gofmt in the core module and in bench/, a
# separate module the root `go test ./...` does not include. CLAUDE.md, the operator and
# the orchestrator's test.command all run this one script.
set -e
cd "$(dirname "$0")/.."
for m in . bench; do
	echo "== $m"
	(
		cd "$m"
		go build ./...
		go vet ./...
		go test ./...
		unformatted=$(gofmt -l .)
		if [ -n "$unformatted" ]; then
			echo "gofmt -l: these files need formatting:" >&2
			echo "$unformatted" >&2
			exit 1
		fi
	)
done
