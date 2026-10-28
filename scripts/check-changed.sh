#!/bin/sh
# test.verify_command of this repository (TODO.md Q10): what run_tests runs inside an implement
# session. It builds and vets everything, like scripts/check.sh, but tests only the packages the
# run changed since $ORCH_BASE_REF: each changed file (Markdown and bench/ left out) counts for
# the nearest directory above it with Go files. A file with no such directory (go.mod, a file in
# the root), no base or no change runs ./.... ORCH_TEST_FILTER narrows go test to -run as in
# check.sh. The test step still runs check.sh in full.
# shortcut: packages that import a changed one are not tested; add `go list -deps` if a fix keeps
# breaking a dependent only the test step finds.
set -e
cd "$(dirname "$0")/.."
pkgs=
if [ -n "$ORCH_BASE_REF" ] &&
	files=$({ git diff --name-only "$ORCH_BASE_REF" -- && git ls-files -o --exclude-standard; } 2>/dev/null); then
	pkgs=$(printf '%s\n' "$files" | while IFS= read -r f; do
		case "$f" in '' | *.md | bench/*) continue ;; esac
		d=$(dirname "$f")
		while [ "$d" != . ] && ! ls "$d"/*.go >/dev/null 2>&1; do
			d=$(dirname "$d")
		done
		[ "$d" = . ] && echo ALL || echo "./$d"
	done | sort -u)
	case "$pkgs" in *ALL*) pkgs= ;; esac
fi
pkgs=$(echo ${pkgs:-./...})
echo "== go test $pkgs"
go build ./...
go vet ./...
# shellcheck disable=SC2086 # pkgs is a list of package paths without spaces.
go test $pkgs ${ORCH_TEST_FILTER:+-run "$ORCH_TEST_FILTER"}
unformatted=$(gofmt -l .)
if [ -n "$unformatted" ]; then
	echo "gofmt -l: these files need formatting:" >&2
	echo "$unformatted" >&2
	exit 1
fi
