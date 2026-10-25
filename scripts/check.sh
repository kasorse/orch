#!/bin/sh
# The repository's gates: build, vet, test and gofmt in the core module and in bench/, a
# separate module the root `go test ./...` does not include. CLAUDE.md, the operator and
# the orchestrator's test.command all run this one script. ORCH_TEST_FILTER, which only the
# implement session's run_tests sets, narrows `go test` to the tests it names (-run); the
# other gates still run in full.
set -e
cd "$(dirname "$0")/.."
for m in . bench; do
	echo "== $m"
	(
		cd "$m"
		go build ./...
		go vet ./...
		go test ./... ${ORCH_TEST_FILTER:+-run "$ORCH_TEST_FILTER"}
		unformatted=$(gofmt -l .)
		if [ -n "$unformatted" ]; then
			echo "gofmt -l: these files need formatting:" >&2
			echo "$unformatted" >&2
			exit 1
		fi
	)
done
