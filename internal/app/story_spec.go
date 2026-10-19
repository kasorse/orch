package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"orchestrator/internal/apperr"
	"orchestrator/internal/execution"
	"orchestrator/internal/persistence"
	"orchestrator/internal/repository"
	"orchestrator/internal/story"
)

// maxTaskFileBytes bounds a task file read: the text is at most 16 KiB, and the As built
// sections Go appends are a few lines each.
const maxTaskFileBytes = 1 << 20

// storyLocation is where a story is: the spec directory the operator named, the repository that
// holds it (the story home) and the story's own directory. The directory decides the state, not
// the working directory (specs/story/SPEC.md §10.1).
type storyLocation struct {
	slug string
	// specDir is the spec directory's absolute path, symlinks resolved; specRel is it relative to
	// home, slash-separated, as story.json records it.
	specDir string
	specRel string
	home    string
	// storyDir is <home>/.orchestrator/stories/<slug>, derived and never read from a file.
	storyDir string
}

// locateStory resolves the spec directory dirArg to its story home with git rev-parse
// --show-toplevel. The slug is the directory's name.
func locateStory(ctx context.Context, executor execution.ProcessExecutor, dirArg string) (storyLocation, error) {
	abs, err := filepath.Abs(dirArg)
	if err != nil {
		return storyLocation{}, &apperr.ConfigError{Msg: "cannot resolve the spec directory " + dirArg, Err: err}
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return storyLocation{}, &apperr.ConfigError{Msg: "cannot read the spec directory " + dirArg, Err: err}
	}
	if info, err := os.Stat(resolved); err != nil || !info.IsDir() {
		return storyLocation{}, &apperr.ConfigError{Msg: dirArg + " is not a directory"}
	}
	repo, err := repository.Open(ctx, resolved, executor)
	if err != nil {
		return storyLocation{}, err
	}
	top, err := repo.TopLevel(ctx)
	if err != nil {
		return storyLocation{}, err
	}
	// git reports the top level with symlinks resolved, as resolved is.
	home := top
	if r, err := filepath.EvalSymlinks(top); err == nil {
		home = r
	}
	rel, err := filepath.Rel(home, resolved)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return storyLocation{}, &apperr.ConfigError{Msg: fmt.Sprintf("%s is not a directory inside the repository %s", dirArg, home)}
	}
	slug := filepath.Base(resolved)
	storyDir, err := persistence.StoryDir(home, slug)
	if err != nil {
		return storyLocation{}, &apperr.ConfigError{Msg: fmt.Sprintf("%s: the name of a spec directory is its story's slug: %v", dirArg, err)}
	}
	return storyLocation{slug: slug, specDir: resolved, specRel: filepath.ToSlash(rel), home: home, storyDir: storyDir}, nil
}

// storySpec is a spec directory read: the title and the background sections of SPEC.md and the
// task files, each with its path and the bytes that were read.
type storySpec struct {
	title     string
	approach  string
	contracts string
	tasks     []story.Task
	files     map[string]string // task ID -> path
	data      map[string][]byte // task ID -> file content
}

// readStorySpec reads <specDir>/SPEC.md (optional: a hand-written spec may lack it, and the slug
// is then the title) and every tasks/TASK-*.md, which must parse (story.ParseTaskFile). Nothing
// is written.
func readStorySpec(specDir, slug string) (storySpec, error) {
	sp := storySpec{title: slug, files: map[string]string{}, data: map[string][]byte{}}
	specData, err := os.ReadFile(filepath.Join(specDir, "SPEC.md"))
	switch {
	case err == nil:
		title, approach, contracts := story.ParseSpec(specData)
		if title != "" {
			sp.title = title
		}
		sp.approach, sp.contracts = approach, contracts
	case !errors.Is(err, fs.ErrNotExist):
		return storySpec{}, &apperr.ConfigError{Msg: "cannot read SPEC.md", Err: err}
	}

	tasksDir := filepath.Join(specDir, "tasks")
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		return storySpec{}, &apperr.ConfigError{Msg: "cannot read the task files", Err: err}
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "TASK-") || !strings.HasSuffix(name, ".md") {
			continue
		}
		path := filepath.Join(tasksDir, name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return storySpec{}, &apperr.ConfigError{Msg: path + " is not a regular file"}
		}
		if info.Size() > maxTaskFileBytes {
			return storySpec{}, &apperr.ConfigError{Msg: fmt.Sprintf("%s is %d bytes, over %d", path, info.Size(), maxTaskFileBytes)}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return storySpec{}, &apperr.ConfigError{Msg: "cannot read " + path, Err: err}
		}
		t, err := story.ParseTaskFile(name, data)
		if err != nil {
			return storySpec{}, &apperr.ConfigError{Msg: err.Error()}
		}
		if prev, dup := sp.files[t.ID]; dup {
			return storySpec{}, &apperr.ConfigError{Msg: fmt.Sprintf("task ID %s is in two files: %s and %s", t.ID, prev, path)}
		}
		sp.tasks = append(sp.tasks, t)
		sp.files[t.ID] = path
		sp.data[t.ID] = data
	}
	return sp, nil
}

// storyStates is the per-task state the README shows, from a story record: a task that was
// never dispatched has none, and a pending one shows no branch.
func storyStates(tasks map[string]persistence.StoryTask) map[string]story.TaskState {
	states := make(map[string]story.TaskState, len(tasks))
	for id, t := range tasks {
		st := story.TaskState{Status: t.Status}
		if t.Status != story.TaskPending {
			st.Branch = t.Branch
		}
		states[id] = st
	}
	return states
}
