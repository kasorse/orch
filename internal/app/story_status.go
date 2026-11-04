package app

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"path/filepath"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/execution"
	"github.com/kasorse/orch/internal/logging"
	"github.com/kasorse/orch/internal/persistence"
	"github.com/kasorse/orch/internal/story"
)

// storyStatus is `orchestrator story status <dir>` (specs/story/SPEC.md §10): the README's table
// on the terminal, from story.json and the task files. It only reads: no lock is taken, no
// configuration is loaded and nothing is written, so it can be run while a story run works.
func storyStatus(ctx context.Context, dirArg string, stdout io.Writer) error {
	loc, err := locateStory(ctx, execution.NewOSProcessExecutor(), dirArg)
	if err != nil {
		return err
	}
	spec, err := readStorySpec(loc.specDir, loc.slug)
	if err != nil {
		return err
	}

	status := story.StatusPlanned
	var states map[string]story.TaskState
	rec, err := persistence.LoadStoryRecord(filepath.Join(loc.storyDir, persistence.StoryRecordFile))
	switch {
	case err == nil:
		status, states = rec.Status, storyStates(rec.Tasks)
	case errors.Is(err, fs.ErrNotExist):
	default:
		return &apperr.ConfigError{Msg: "cannot use the story record", Err: err}
	}
	logging.NewConsoleReporter(stdout).StoryStatusTable(story.RenderREADME(spec.title, status, spec.tasks, states))
	return nil
}
