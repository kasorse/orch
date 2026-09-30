// Package fakeexec provides helpers for the integration test harness described in
// MVP.md §29: temporary fake "claude"/"codex"/"gemini" executables that stand in for
// real AI provider CLIs, plus a real (throwaway) git fixture repository to run the
// orchestrator against.
package fakeexec

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// WriteExecutable writes script to dir/name as an executable file and returns its path.
func WriteExecutable(t *testing.T, dir, name, script string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("fakeexec: write executable %s: %v", path, err)
	}
	return path
}

// PrependPath puts dir at the front of PATH for the duration of the test.
func PrependPath(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// ProviderScript is the shared POSIX-sh body used for every fake "claude"/"codex"/
// "gemini" executable. It is entirely driven by environment variables so one script
// can serve all required integration scenarios:
//
//   - INVOCATIONS_LOG: if set, the resolved action name is appended to this file on
//     every invocation, so tests can assert exactly how many AI calls happened (and of
//     which kind), including "no further AI call happened after X".
//   - FAKE_RESPONSES_DIR: directory holding canned JSON responses, one file per
//     action ("translate.json", "decide-context.json", "decide-classify.json",
//     "research.json", "analyze.json", "plan.json", "implement.json", "review.json").
//     For "review", a numbered override
//     "review_<N>.json" (N = 1-based review call count) is preferred over the
//     unnumbered "review.json" when present, so a test can script review
//     fail-then-pass.
//   - FAKE_FAIL_ACTION / FAKE_FAIL_EXIT_CODE / FAKE_FAIL_STDERR / FAKE_FAIL_STDOUT: if
//     FAKE_FAIL_ACTION matches the resolved action, the script exits with
//     FAKE_FAIL_EXIT_CODE (default 1) after writing FAKE_FAIL_STDERR to stderr and/or
//     FAKE_FAIL_STDOUT to stdout (either or both may be set independently - real
//     `claude --print` writes backend/API errors to stdout, not stderr, which
//     FAKE_FAIL_STDOUT alone reproduces), instead of serving a canned response -
//     simulates a provider runtime failure.
//   - FAKE_SLEEP_ACTION / FAKE_SLEEP_SECONDS: if FAKE_SLEEP_ACTION matches the
//     resolved action (including "translate" and "decide-*"), the script sleeps for FAKE_SLEEP_SECONDS
//     (default 1) before doing anything else - simulates a hung provider call for
//     timeout scenarios.
//   - FAKE_PROMPTS_DIR: if set, the prompt of each call is saved to
//     <dir>/<action>.txt (the last call of an action wins), so tests can assert what
//     text a step was given.
//   - FAKE_ARGS_DIR: if set, the argv of each call is saved to <dir>/<action>.args, one
//     argument per line with the prompt replaced by "<prompt>" (the last call of an
//     action wins), so tests can assert the flags a step ran with.
//   - FAKE_ENV_DIR: if set, the environment of each call is saved to
//     <dir>/<action>.env (the output of env), so tests can assert what a provider CLI
//     inherits.
//   - FAKE_STATE_DIR: directory used to keep small counter files
//     (implement_calls, review_calls) across invocations of the same test.
//   - FAKE_IMPLEMENT_FIX_ON_CALL: 1-based implement call number (default 1) on
//     or after which the script overwrites FAKE_FIX_TARGET_FILE with the contents of
//     FAKE_FIX_CONTENT_FILE, simulating a real code fix landing in the target repo
//     (the process's working directory is the target repo root for every AI call).
//
// The action itself is determined by scanning every element of "$@" for the literal
// "ACTION: <name>" marker line that every real prompt builder emits as the first line
// of its prompt - necessary because the prompt is not always the last argv entry
// (e.g. codex/gemini put it second), so only scanning the last argument would miss it.
const ProviderScript = `#!/bin/sh

action=""
for arg in "$@"; do
  case "$arg" in
    "ACTION: translate"*)       action="translate" ;;
    "ACTION: decide-context"*)  action="decide-context" ;;
    "ACTION: decide-classify"*) action="decide-classify" ;;
    "ACTION: research"*)        action="research" ;;
    "ACTION: analyze"*)         action="analyze" ;;
    "ACTION: plan"*)            action="plan" ;;
    "ACTION: implement"*)       action="implement" ;;
    "ACTION: review"*)          action="review" ;;
  esac
  if [ -n "$action" ]; then
    break
  fi
done

if [ -z "$action" ]; then
  echo "fakeexec: no ACTION marker found in arguments" >&2
  exit 1
fi

if [ -n "${INVOCATIONS_LOG:-}" ]; then
  echo "$action" >> "$INVOCATIONS_LOG"
fi

if [ -n "${FAKE_PROMPTS_DIR:-}" ]; then
  for arg in "$@"; do
    case "$arg" in
      "ACTION: "*) printf '%s' "$arg" > "$FAKE_PROMPTS_DIR/$action.txt" ;;
    esac
  done
fi

if [ -n "${FAKE_ARGS_DIR:-}" ]; then
  : > "$FAKE_ARGS_DIR/$action.args"
  for arg in "$@"; do
    case "$arg" in
      "ACTION: "*) printf '%s\n' "<prompt>" >> "$FAKE_ARGS_DIR/$action.args" ;;
      *) printf '%s\n' "$arg" >> "$FAKE_ARGS_DIR/$action.args" ;;
    esac
  done
fi
if [ -n "${FAKE_ENV_DIR:-}" ]; then
  env > "$FAKE_ENV_DIR/$action.env"
fi
if [ -n "${FAKE_SLEEP_ACTION:-}" ] && [ "$FAKE_SLEEP_ACTION" = "$action" ]; then
  sleep "${FAKE_SLEEP_SECONDS:-1}"
fi

if [ -n "${FAKE_FAIL_ACTION:-}" ] && [ "$FAKE_FAIL_ACTION" = "$action" ]; then
  if [ -n "${FAKE_FAIL_STDERR:-}" ]; then
    printf '%s\n' "$FAKE_FAIL_STDERR" >&2
  fi
  if [ -n "${FAKE_FAIL_STDOUT:-}" ]; then
    printf '%s\n' "$FAKE_FAIL_STDOUT"
  fi
  exit "${FAKE_FAIL_EXIT_CODE:-1}"
fi

review_count=""

case "$action" in
  implement)
    if [ -n "${FAKE_STATE_DIR:-}" ]; then
      counter_file="$FAKE_STATE_DIR/implement_calls"
      count=0
      [ -f "$counter_file" ] && count=$(cat "$counter_file")
      count=$((count + 1))
      echo "$count" > "$counter_file"

      fix_on="${FAKE_IMPLEMENT_FIX_ON_CALL:-1}"
      if [ "$count" -ge "$fix_on" ] && [ -n "${FAKE_FIX_TARGET_FILE:-}" ] && [ -n "${FAKE_FIX_CONTENT_FILE:-}" ]; then
        cp "$FAKE_FIX_CONTENT_FILE" "$FAKE_FIX_TARGET_FILE"
      fi
    fi
    ;;
  review)
    if [ -n "${FAKE_STATE_DIR:-}" ]; then
      counter_file="$FAKE_STATE_DIR/review_calls"
      count=0
      [ -f "$counter_file" ] && count=$(cat "$counter_file")
      count=$((count + 1))
      echo "$count" > "$counter_file"
      review_count="$count"
    fi
    ;;
esac

response_file=""
if [ -n "${FAKE_RESPONSES_DIR:-}" ]; then
  if [ "$action" = "review" ] && [ -n "$review_count" ] && [ -f "$FAKE_RESPONSES_DIR/review_${review_count}.json" ]; then
    response_file="$FAKE_RESPONSES_DIR/review_${review_count}.json"
  elif [ -f "$FAKE_RESPONSES_DIR/${action}.json" ]; then
    response_file="$FAKE_RESPONSES_DIR/${action}.json"
  fi
fi

if [ -z "$response_file" ]; then
  echo "fakeexec: no response file configured for action $action" >&2
  exit 1
fi

cat "$response_file"
`

// InitGitFixture creates a temporary directory containing a tiny, deliberately-buggy Go
// module (fixture.Add subtracts instead of adding, so "go test ./..." fails until
// fixed), commits it with real git, and returns the directory. It uses exec.Command
// directly rather than the orchestrator's own executor abstraction, since this is test
// scaffolding, not code under test.
func InitGitFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "go.mod"), "module fixture\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "fixture.go"), buggyFixtureSource)
	writeFile(t, filepath.Join(dir, "fixture_test.go"), fixtureTestSource)

	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "fixture@example.com")
	runGit(t, dir, "config", "user.name", "Fixture")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-m", "initial fixture commit")

	return dir
}

const buggyFixtureSource = `package fixture

func Add(a, b int) int {
	return a - b
}
`

// FixedFixtureSource is the corrected version of fixture.go (Add actually adds), used by
// tests as the FAKE_FIX_CONTENT_FILE a fake "implement" call copies over the buggy file.
const FixedFixtureSource = `package fixture

func Add(a, b int) int {
	return a + b
}
`

const fixtureTestSource = `package fixture

import "testing"

func TestAdd(t *testing.T) {
	if got := Add(2, 3); got != 5 {
		t.Fatalf("Add(2, 3) = %d, want 5", got)
	}
}
`

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("fakeexec: write %s: %v", path, err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fakeexec: git %v failed: %v\n%s", args, err, out)
	}
}
