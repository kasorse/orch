package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/domain"
)

const (
	// maxListenerTaskFileBytes bounds the task file read: a description of 1 MiB, which JSON can
	// spell in up to six times as many bytes, is cut off well before that.
	maxListenerTaskFileBytes = 2 << 20
	// maxListenerDescriptionBytes bounds the rendered ticket in the task file.
	maxListenerDescriptionBytes = 1 << 20
	// maxListenerScrubEnv bounds the credential variable names in the task file.
	maxListenerScrubEnv = 16
	// listenerTaskVersion is the "v" of the task file.
	listenerTaskVersion = 1
)

// scrubEnvNameRE is what a name in scrub_env looks like.
var scrubEnvNameRE = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// listenerTaskGone is why parseArgs refuses --listener-task: the flag was how the listener's child
// run got its ticket, and the listener now calls the run in its own process (specs/embed/SPEC.md §2).
const listenerTaskGone = "--listener-task is gone: the Jira listener runs a ticket in its own process and passes the task as a value, there is no child run to hand a file to (specs/embed)"

// listenerTask is the ticket the listener rendered (specs/jira-listener/SPEC.md §7.2, specs/embed/
// SPEC.md §2.1), with the variables the run must not pass on. The listener passes it to runTask as a
// value and records it as tasks/<KEY>.<attempt>.json before the run: the file is the record and what
// a restart reads back. The run makes no Jira call; everything it knows of the ticket is here.
type listenerTask struct {
	V           int       `json:"v"`
	Key         string    `json:"key"`
	URL         string    `json:"url"`
	FetchedAt   time.Time `json:"fetched_at"`
	Description string    `json:"description"`
	ScrubEnv    []string  `json:"scrub_env"`
}

// RunListenerTask makes the call the listener makes for one ticket, without the listener around it:
// the run of the task in taskFile (a file as the listener records it, validated as it validates
// it), in dir, under the configuration at configPath, --mode auto. It writes the result line to
// stdout as `run --output json` did, and returns what the run returned. It exists for the
// integration tests, which live in another package and drive the run itself (specs/embed/SPEC.md
// §7); the process's working directory plays no part.
func RunListenerTask(ctx context.Context, dir, configPath, taskFile string, stdout, stderr io.Writer) error {
	ctx, rec := withResultRecorder(ctx)
	task, err := readListenerTask(taskFile)
	if err == nil {
		err = runTask(ctx, dir, configPath, "", "", "", &task, nil, false, "", domain.ModeAuto, "", "", false,
			strings.NewReader(""), plainWriter{stderr}, stderr)
	}
	writeResult(stdout, rec, err)
	return err
}

// readListenerTask reads and validates a task file the listener wrote. It reads at most
// maxListenerTaskFileBytes, so a wrong path to a large file costs nothing.
func readListenerTask(path string) (listenerTask, error) {
	f, err := os.Open(path)
	if err != nil {
		return listenerTask{}, &apperr.ConfigError{Msg: "cannot read the listener task file", Err: err}
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxListenerTaskFileBytes+1))
	if err != nil {
		return listenerTask{}, &apperr.ConfigError{Msg: "cannot read the listener task file " + path, Err: err}
	}
	if len(data) > maxListenerTaskFileBytes {
		return listenerTask{}, &apperr.ConfigError{Msg: fmt.Sprintf("the listener task file %s is over %d bytes", path, maxListenerTaskFileBytes)}
	}
	in, err := parseListenerTask(data)
	if err != nil {
		return listenerTask{}, &apperr.ConfigError{Msg: fmt.Sprintf("%s: %v", path, err)}
	}
	return in, nil
}

// parseListenerTask decodes a task file strictly: one JSON object, no other key, and every field
// as SPEC §7.2 says.
func parseListenerTask(data []byte) (listenerTask, error) {
	var in listenerTask
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return listenerTask{}, errors.New("the listener task is not a JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) {
			return listenerTask{}, fmt.Errorf("the listener task: %s has the wrong type (a JSON %s)", typeErr.Field, typeErr.Value)
		}
		if name, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
			if unquoted, uerr := strconv.Unquote(name); uerr == nil {
				name = unquoted
			}
			return listenerTask{}, fmt.Errorf("the listener task: %q is not a field of it", name)
		}
		return listenerTask{}, fmt.Errorf("the listener task is not valid JSON: %v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return listenerTask{}, errors.New("the listener task has data after its JSON object")
	}

	switch {
	case in.V != listenerTaskVersion:
		return listenerTask{}, fmt.Errorf("the listener task: v is %d, want %d", in.V, listenerTaskVersion)
	case !domain.ValidTicketKey(in.Key):
		return listenerTask{}, fmt.Errorf("the listener task: key %q is not an issue key like PAY-12", in.Key)
	case !absoluteHTTPS(in.URL):
		return listenerTask{}, fmt.Errorf("the listener task: url %q is not an absolute https URL", in.URL)
	case in.FetchedAt.IsZero():
		return listenerTask{}, errors.New("the listener task: fetched_at is not set")
	case strings.TrimSpace(in.Description) == "":
		return listenerTask{}, errors.New("the listener task: description is empty")
	case len(in.Description) > maxListenerDescriptionBytes:
		return listenerTask{}, fmt.Errorf("the listener task: description is %d bytes, over the %d-byte limit", len(in.Description), maxListenerDescriptionBytes)
	case len(in.ScrubEnv) > maxListenerScrubEnv:
		return listenerTask{}, fmt.Errorf("the listener task: scrub_env has %d names, more than %d", len(in.ScrubEnv), maxListenerScrubEnv)
	}
	for i, name := range in.ScrubEnv {
		if !scrubEnvNameRE.MatchString(name) {
			return listenerTask{}, fmt.Errorf("the listener task: scrub_env[%d] %q is not a variable name (^[A-Z_][A-Z0-9_]*$)", i, name)
		}
	}
	return in, nil
}

// absoluteHTTPS reports whether s is an https URL with a host and no credentials.
func absoluteHTTPS(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
}

// listenerTaskSource is the task.source a listener task records: the ticket it was rendered from,
// marked as handed over by the listener.
func listenerTaskSource(in listenerTask) *domain.TaskSource {
	return &domain.TaskSource{Kind: domain.TaskSourceJira, Key: in.Key, URL: in.URL, FetchedAt: in.FetchedAt, Listener: true}
}
