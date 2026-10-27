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
//     action ("translate.json", "decide-context.json", "decide-clarity.json",
//     "decide-classify.json", "clarify.json", "research.json", "investigate.json", "implement.json",
//     "review.json", and a story's planning calls, "survey.json" and "specify.json").
//     For "review", "implement" and "decide-clarity", a numbered override
//     "review_<N>.json" / "implement_<N>.json" / "decide-clarity_<N>.json" (N = 1-based call
//     count of that action, as kept in FAKE_STATE_DIR) is preferred over the unnumbered file when
//     present, so a test can script review fail-then-pass, an implement answer per attempt, or a
//     clarity re-check that stays below the threshold and then clears it.
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
//     timeout scenarios. With FAKE_SLEEP_UNTIL_FILE set, the sleep ends early once that path
//     exists, so a test can let a call go on at a moment of its choosing.
//   - FAKE_SLEEP_BRANCH / FAKE_FAIL_BRANCH: if set, FAKE_SLEEP_ACTION (FAKE_FAIL_ACTION)
//     applies only to a call whose working directory has this branch checked out (a story
//     task's worktree: story/<slug>/<ID>), so two tasks that run at once can behave differently.
//   - FAKE_RENDEZVOUS_DIR /FAKE_RENDEZVOUS_ACTION / FAKE_RENDEZVOUS_COUNT: if the resolved
//     action is FAKE_RENDEZVOUS_ACTION, the call creates a file named by its process ID in
//     FAKE_RENDEZVOUS_DIR and waits until FAKE_RENDEZVOUS_COUNT (default 2) files are there,
//     at most 30 seconds, then fails: calls that run at the same time meet, calls that run one
//     after the other do not - proof that tasks ran in parallel. The meeting comes before
//     FAKE_SLEEP_ACTION's sleep.
//   - FAKE_PROMPTS_DIR: if set, the prompt of each call is saved to
//     <dir>/<action>.txt (the last call of an action wins), so tests can assert what
//     text a step was given. With FAKE_STATE_DIR also set, it is saved to
//     <dir>/<action>_<N>.txt as well (N = the call's 1-based number for that action, the
//     one the numbered responses use), so a test can read the prompt of every attempt.
//   - FAKE_ARGS_DIR: if set, the argv of each call is saved to <dir>/<action>.args, one
//     argument per line (a prompt in argv, codex/gemini, shows as "<prompt>"; claude's is
//     on stdin and not in the file; the last call of an action wins), so tests can assert the flags a step ran with.
//   - FAKE_ENV_DIR: if set, the environment of each call is saved to
//     <dir>/<action>.env (the output of env), so tests can assert what a provider CLI
//     inherits.
//   - FAKE_CWD_DIR: if set, the working directory of each call (pwd -P) is saved to
//     <dir>/<action>.cwd, so tests can assert which tree a step ran in.
//   - FAKE_API_KEY_SOURCE / FAKE_API_RETRY_ERROR / FAKE_API_RETRY_ACTION: stream-json mode.
//     When FAKE_API_KEY_SOURCE or FAKE_API_RETRY_ERROR is set, the canned response is printed
//     as a claude --output-format stream-json transcript: a system/init event whose
//     apiKeySource is FAKE_API_KEY_SOURCE ("auto" = "ANTHROPIC_API_KEY" when the call's
//     environment has that variable, else "none"; "none" when only the error is set), then,
//     with FAKE_API_RETRY_ERROR, a system/api_retry event with that error category (only for
//     the action FAKE_API_RETRY_ACTION when it is set), then the result event holding the
//     response. Without either variable the response is printed as it is.
//   - FAKE_STATE_DIR: directory used to keep small counter files
//     (<action>_calls, e.g. implement_calls and review_calls; a call that
//     FAKE_FAIL_ACTION fails is not counted) across invocations of the same test.
//   - FAKE_IMPLEMENT_FIX_ON_CALL: 1-based implement call number (default 1) on
//     or after which the script overwrites FAKE_FIX_TARGET_FILE with the contents of
//     FAKE_FIX_CONTENT_FILE, simulating a real code fix landing in the target repo
//     (the process's working directory is the target repo root for every AI call).
//   - FAKE_IMPLEMENT_APPEND_FILE: if set, every implement call (after the fix above, if it
//     applies) appends one line "// implement call <N>" to this file. The tree then differs
//     from one attempt to the next while the code stays as buggy or as fixed as it was,
//     which the no-progress scenarios need.
//   - FAKE_IMPLEMENT_TOUCH_FILE / FAKE_IMPLEMENT_REVERT_ON_CALL: if FAKE_IMPLEMENT_TOUCH_FILE
//     is set, every implement call writes the line "-- implement call <N>" to this path
//     (creating its directory), so the run's net diff holds a file the fixture does not
//     (migrations/x.sql, for a path rule). From the implement call number in
//     FAKE_IMPLEMENT_REVERT_ON_CALL on, the call removes the file instead, as a later attempt
//     reverting an earlier one.
//   - FAKE_IMPLEMENT_BRANCH_FILE / FAKE_IMPLEMENT_BRANCH_DIR: every implement call writes the
//     name of the branch checked out where it runs (a story task's worktree: story/<slug>/<ID>)
//     to the path FAKE_IMPLEMENT_BRANCH_FILE, and to <FAKE_IMPLEMENT_BRANCH_DIR>/<branch with
//     "/" as "-">.txt. Two tasks that name the same file change it differently (a merge
//     conflict); in the directory each adds a file of its own (a clean merge).
//   - FAKE_PLANT_HOOK_BEFORE_CALL: 1-based implement call number before which the script
//     writes an executable .git/hooks/pre-commit that touches FAKE_HOOK_MARKER (default
//     $FAKE_STATE_DIR/hook-ran), as a model planting a hook would. A test asserts the
//     marker never appears.
//   - FAKE_VERIFY_CALLS / FAKE_VERIFY_DIR: MCP client mode. When an implement call's argv has
//     "--mcp-config <json>" with an "orch" server and FAKE_VERIFY_CALLS is set, the script
//     reads the server's command and args from that JSON, starts it, and speaks the MCP
//     subset of specs/quality/SPEC.md §6.5 over its stdio: initialize,
//     notifications/initialized, then one tools/call per ";"-separated entry of
//     FAKE_VERIFY_CALLS - "run_tests", "run_tests:<filter>" or "run_check:<name>" - after the
//     call's file edits. The requests go in as one batch (the server answers them in order
//     and exits on EOF, so nothing can hang on a half-read pipe; a hung server is killed by
//     timeout 120). Per implement call N the script saves, under FAKE_VERIFY_DIR,
//     verify_<N>.requests, verify_<N>.responses (one JSON-RPC response per line),
//     verify_<N>.stderr and verify_<N>.dir (the server's --dir, to assert it is removed).
//     The server command is the orchestrator itself, so the integration package answers
//     "mcp-verify" from its TestMain.
//
// The action itself is determined by scanning every element of "$@" for the literal
// "ACTION: <name>" marker line that every real prompt builder emits as the first line
// of its prompt - necessary because the prompt is not always the last argv entry
// (e.g. codex/gemini put it second), so only scanning the last argument would miss it.
const ProviderScript = `#!/bin/sh

if [ "$1" = "--version" ]; then
  echo "${FAKE_CLI_VERSION:-2.1.289 (Claude Code)}"
  exit 0
fi

# The claude adapter sends the prompt on stdin; codex and gemini put it in argv. Read stdin
# once (a CLI whose prompt is in argv gets none, and /dev/null never blocks it).
prompt=$(cat)
action=""
action_arg=""
for arg in "$prompt" "$@"; do
  case "$arg" in
    "ACTION: translate"*)       action="translate" ;;
    "ACTION: decide-context"*)  action="decide-context" ;;
    "ACTION: decide-classify"*) action="decide-classify" ;;
    "ACTION: decide-clarity"*)  action="decide-clarity" ;;
    "ACTION: clarify"*)         action="clarify" ;;
    "ACTION: branch-name"*)     action="branch-name" ;;
    "ACTION: research"*)        action="research" ;;
    "ACTION: investigate"*)     action="investigate" ;;
    "ACTION: implement"*)       action="implement" ;;
    "ACTION: review"*)          action="review" ;;
    "ACTION: survey"*)          action="survey" ;;
    "ACTION: specify"*)         action="specify" ;;
  esac
  if [ -n "$action" ]; then
    action_arg="$arg"
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
  for arg in "$prompt" "$@"; do
    case "$arg" in
      "ACTION: "*) printf '%s' "$arg" > "$FAKE_PROMPTS_DIR/$action.txt"; break ;;
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
if [ -n "${FAKE_CWD_DIR:-}" ]; then
  pwd -P > "$FAKE_CWD_DIR/$action.cwd"
fi
current_branch=""
if [ -n "${FAKE_SLEEP_BRANCH:-}" ] || [ -n "${FAKE_FAIL_BRANCH:-}" ]; then
  current_branch=$(git rev-parse --abbrev-ref HEAD 2>/dev/null)
fi
sleep_ok=1
if [ -n "${FAKE_SLEEP_BRANCH:-}" ] && [ "$current_branch" != "$FAKE_SLEEP_BRANCH" ]; then
  sleep_ok=0
fi
fail_ok=1
if [ -n "${FAKE_FAIL_BRANCH:-}" ] && [ "$current_branch" != "$FAKE_FAIL_BRANCH" ]; then
  fail_ok=0
fi
if [ -n "${FAKE_RENDEZVOUS_DIR:-}" ] && [ "${FAKE_RENDEZVOUS_ACTION:-}" = "$action" ]; then
  : > "$FAKE_RENDEZVOUS_DIR/$$"
  waited=0
  while [ "$(ls "$FAKE_RENDEZVOUS_DIR" | wc -l)" -lt "${FAKE_RENDEZVOUS_COUNT:-2}" ]; do
    waited=$((waited + 1))
    if [ "$waited" -gt 150 ]; then
      echo "fakeexec: the other calls did not arrive in 30 seconds" >&2
      exit 1
    fi
    sleep 0.2
  done
fi
if [ -n "${FAKE_SLEEP_ACTION:-}" ] && [ "$FAKE_SLEEP_ACTION" = "$action" ] && [ "$sleep_ok" = 1 ]; then
  if [ -n "${FAKE_SLEEP_UNTIL_FILE:-}" ]; then
    waited=0
    while [ ! -e "$FAKE_SLEEP_UNTIL_FILE" ] && [ "$waited" -lt $(( ${FAKE_SLEEP_SECONDS:-1} * 5 )) ]; do
      waited=$((waited + 1))
      sleep 0.2
    done
  else
    sleep "${FAKE_SLEEP_SECONDS:-1}"
  fi
fi

if [ -n "${FAKE_FAIL_ACTION:-}" ] && [ "$FAKE_FAIL_ACTION" = "$action" ] && [ "$fail_ok" = 1 ]; then
  if [ -n "${FAKE_FAIL_STDERR:-}" ]; then
    printf '%s\n' "$FAKE_FAIL_STDERR" >&2
  fi
  if [ -n "${FAKE_FAIL_STDOUT:-}" ]; then
    printf '%s\n' "$FAKE_FAIL_STDOUT"
  fi
  exit "${FAKE_FAIL_EXIT_CODE:-1}"
fi

# The call's 1-based number for its action, counted after the FAKE_FAIL_ACTION check above.
call_count=""
if [ -n "${FAKE_STATE_DIR:-}" ]; then
  counter_file="$FAKE_STATE_DIR/${action}_calls"
  count=0
  [ -f "$counter_file" ] && count=$(cat "$counter_file")
  count=$((count + 1))
  echo "$count" > "$counter_file"
  call_count="$count"
fi

if [ -n "${FAKE_PROMPTS_DIR:-}" ] && [ -n "$call_count" ]; then
  printf '%s' "$action_arg" > "$FAKE_PROMPTS_DIR/${action}_${call_count}.txt"
fi

if [ "$action" = "implement" ] && [ -n "$call_count" ]; then
  if [ -n "${FAKE_PLANT_HOOK_BEFORE_CALL:-}" ] && [ "$call_count" -eq "$FAKE_PLANT_HOOK_BEFORE_CALL" ]; then
    mkdir -p .git/hooks
    printf '#!/bin/sh\ntouch "%s"\n' "${FAKE_HOOK_MARKER:-$FAKE_STATE_DIR/hook-ran}" > .git/hooks/pre-commit
    chmod 755 .git/hooks/pre-commit
  fi

  fix_on="${FAKE_IMPLEMENT_FIX_ON_CALL:-1}"
  if [ "$call_count" -ge "$fix_on" ] && [ -n "${FAKE_FIX_TARGET_FILE:-}" ] && [ -n "${FAKE_FIX_CONTENT_FILE:-}" ]; then
    cp "$FAKE_FIX_CONTENT_FILE" "$FAKE_FIX_TARGET_FILE"
  fi

  if [ -n "${FAKE_IMPLEMENT_APPEND_FILE:-}" ]; then
    printf '// implement call %s\n' "$call_count" >> "$FAKE_IMPLEMENT_APPEND_FILE"
  fi

  if [ -n "${FAKE_IMPLEMENT_TOUCH_FILE:-}" ]; then
    if [ -n "${FAKE_IMPLEMENT_REVERT_ON_CALL:-}" ] && [ "$call_count" -ge "$FAKE_IMPLEMENT_REVERT_ON_CALL" ]; then
      rm -f "$FAKE_IMPLEMENT_TOUCH_FILE"
    else
      mkdir -p "$(dirname "$FAKE_IMPLEMENT_TOUCH_FILE")"
      printf '%s\n' "-- implement call $call_count" > "$FAKE_IMPLEMENT_TOUCH_FILE"
    fi
  fi

  if [ -n "${FAKE_IMPLEMENT_BRANCH_FILE:-}" ] || [ -n "${FAKE_IMPLEMENT_BRANCH_DIR:-}" ]; then
    branch=$(git symbolic-ref --short HEAD 2>/dev/null || echo detached)
    if [ -n "${FAKE_IMPLEMENT_BRANCH_FILE:-}" ]; then
      printf '%s\n' "$branch" > "$FAKE_IMPLEMENT_BRANCH_FILE"
    fi
    if [ -n "${FAKE_IMPLEMENT_BRANCH_DIR:-}" ]; then
      mkdir -p "$FAKE_IMPLEMENT_BRANCH_DIR"
      printf '%s\n' "$branch" > "$FAKE_IMPLEMENT_BRANCH_DIR/$(printf '%s' "$branch" | tr '/' '-').txt"
    fi
  fi

  # MCP client mode: act as the model calling the orch server's tools, after its edits.
  if [ -n "${FAKE_VERIFY_CALLS:-}" ] && [ -n "${FAKE_VERIFY_DIR:-}" ]; then
    mcp_config=""
    prev=""
    for arg in "$@"; do
      if [ "$prev" = "--mcp-config" ]; then
        mcp_config="$arg"
      fi
      prev="$arg"
    done
    case "$mcp_config" in
      *'"orch"'*)
        server_cmd=$(printf '%s' "$mcp_config" | sed -n 's/.*"command":"\([^"]*\)".*/\1/p')
        server_args=$(printf '%s' "$mcp_config" | sed -n 's/.*"args":\[\([^]]*\)\].*/\1/p' | sed 's/","/" "/g')
        server_dir=$(printf '%s' "$mcp_config" | sed -n 's/.*"--dir","\([^"]*\)".*/\1/p')
        verify_base="$FAKE_VERIFY_DIR/verify_${call_count}"
        printf '%s\n' "$server_dir" > "$verify_base.dir"
        (
          printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"fakeexec","version":"1"}}}'
          printf '%s\n' '{"jsonrpc":"2.0","method":"notifications/initialized"}'
          id=1
          IFS=';'
          for call in $FAKE_VERIFY_CALLS; do
            id=$((id + 1))
            case "$call" in
              run_check:*) tool="run_check"; args="{\"name\":\"${call#run_check:}\"}" ;;
              run_tests:*) tool="run_tests"; args="{\"filter\":\"${call#run_tests:}\"}" ;;
              *)           tool="$call"; args="{}" ;;
            esac
            printf '{"jsonrpc":"2.0","id":%s,"method":"tools/call","params":{"name":"%s","arguments":%s}}\n' "$id" "$tool" "$args"
          done
        ) > "$verify_base.requests"
        (
          eval "set -- $server_args"
          if command -v timeout >/dev/null 2>&1; then
            timeout 120 "$server_cmd" "$@" < "$verify_base.requests" > "$verify_base.responses" 2> "$verify_base.stderr"
          else
            "$server_cmd" "$@" < "$verify_base.requests" > "$verify_base.responses" 2> "$verify_base.stderr"
          fi
        )
        ;;
    esac
  fi
fi

response_file=""
if [ -n "${FAKE_RESPONSES_DIR:-}" ]; then
  if { [ "$action" = "review" ] || [ "$action" = "implement" ] || [ "$action" = "decide-clarity" ]; } && [ -n "$call_count" ] && [ -f "$FAKE_RESPONSES_DIR/${action}_${call_count}.json" ]; then
    response_file="$FAKE_RESPONSES_DIR/${action}_${call_count}.json"
  elif [ -f "$FAKE_RESPONSES_DIR/${action}.json" ]; then
    response_file="$FAKE_RESPONSES_DIR/${action}.json"
  fi
fi

if [ -z "$response_file" ]; then
  echo "fakeexec: no response file configured for action $action" >&2
  exit 1
fi

stream_source="${FAKE_API_KEY_SOURCE:-}"
retry_error=""
if [ -n "${FAKE_API_RETRY_ERROR:-}" ] && { [ -z "${FAKE_API_RETRY_ACTION:-}" ] || [ "$FAKE_API_RETRY_ACTION" = "$action" ]; }; then
  retry_error="$FAKE_API_RETRY_ERROR"
fi
if [ -n "$stream_source" ] || [ -n "$retry_error" ]; then
  # Stream-json mode: the answer goes out as the result event of a transcript that starts with
  # the init event, as the real CLI prints it (specs/claude-auth/SPEC.md §8, §9).
  if [ "$stream_source" = "auto" ]; then
    if [ -n "${ANTHROPIC_API_KEY:-}" ]; then stream_source="ANTHROPIC_API_KEY"; else stream_source="none"; fi
  fi
  printf '{"type":"system","subtype":"init","apiKeySource":"%s"}\n' "${stream_source:-none}"
  if [ -n "$retry_error" ]; then
    printf '{"type":"system","subtype":"api_retry","error":"%s"}\n' "$retry_error"
  fi
  body=$(sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' "$response_file" | tr '\t' ' ' | awk '{printf "%s\\n", $0}')
  printf '{"type":"result","subtype":"success","is_error":false,"result":"%s"}\n' "$body"
  exit 0
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
