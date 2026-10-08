package results

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// ManifestName is the file in a results directory that says what produced it.
const ManifestName = "manifest.json"

// ManifestSchema is the version of manifest.json.
const ManifestSchema = 1

// Build identifies the orchestrator under test: the output of `orchestrator version` (which may
// not name a commit until TODO P9) and the SHA-256 of the binary, which does.
type Build struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

// Manifest is manifest.json (SPEC.md §13.8): what the results of a directory were made with.
// `run` compares it with the current build on every invocation, so that one directory never
// mixes results of two builds unless the operator allows it.
type Manifest struct {
	Schema       int    `json:"schema"`
	Orchestrator Build  `json:"orchestrator"`
	ClaudeCLI    string `json:"claude_cli"`
	// Configs and Tasks map a name to the hash of its file; they grow as configs and tasks
	// are added to the directory.
	Configs     map[string]string `json:"configs"`
	Tasks       map[string]string `json:"tasks"`
	Contributor string            `json:"contributor"`
	Created     string            `json:"created"`
	Updated     string            `json:"updated"`
	// Mixed has one entry for every time --allow-mixed continued the directory with something
	// that differed.
	Mixed []MixedEntry `json:"mixed,omitempty"`
}

// MixedEntry records a continuation that --allow-mixed accepted: when, with what, and what
// differed from the manifest then.
type MixedEntry struct {
	At           string   `json:"at"`
	Orchestrator Build    `json:"orchestrator"`
	ClaudeCLI    string   `json:"claude_cli"`
	Contributor  string   `json:"contributor"`
	Differences  []string `json:"differences"`
}

// Mismatch is one way the current build differs from a manifest.
type Mismatch struct {
	What string
	Was  string
	Now  string
}

func (m Mismatch) String() string {
	return fmt.Sprintf("%s: was %s, now %s", m.What, quoteOrDash(m.Was), quoteOrDash(m.Now))
}

func quoteOrDash(s string) string {
	if s == "" {
		return "—"
	}
	return fmt.Sprintf("%q", s)
}

// MismatchError is the error of a manifest that does not fit the current build.
type MismatchError struct {
	List []Mismatch
}

func (e *MismatchError) Error() string {
	parts := make([]string, len(e.List))
	for i, m := range e.List {
		parts[i] = m.String()
	}
	return "the results directory was made with something else: " + strings.Join(parts, "; ")
}

// Compare lists what in cur differs from old: the orchestrator's version or binary, the claude
// CLI, the contributor, and the hash of a config or a task old already has. A config or a task
// old does not know is new, not a mismatch.
func Compare(old, cur Manifest) []Mismatch {
	var out []Mismatch
	add := func(what, was, now string) {
		if was != now {
			out = append(out, Mismatch{What: what, Was: was, Now: now})
		}
	}
	add("orchestrator version", old.Orchestrator.Version, cur.Orchestrator.Version)
	add("orchestrator binary sha256", old.Orchestrator.SHA256, cur.Orchestrator.SHA256)
	add("claude CLI", old.ClaudeCLI, cur.ClaudeCLI)
	add("contributor", old.Contributor, cur.Contributor)

	for _, set := range []struct {
		kind     string
		old, cur map[string]string
	}{{"config", old.Configs, cur.Configs}, {"task", old.Tasks, cur.Tasks}} {
		for _, name := range sortedKeys(set.cur) {
			if was, ok := set.old[name]; ok {
				add(fmt.Sprintf("%s %s sha256", set.kind, name), was, set.cur[name])
			}
		}
	}
	return out
}

// Merge is the manifest to save when a directory described by old is continued with cur: cur's
// build, CLI and contributor, the configs and tasks of both (cur's hash wins), the creation
// time of old, and, when the continuation differed, an entry in Mixed.
func (old Manifest) Merge(cur Manifest, mismatches []Mismatch, now time.Time) Manifest {
	merged := cur
	merged.Schema = ManifestSchema
	merged.Created = old.Created
	merged.Updated = now.UTC().Format(time.RFC3339)
	merged.Configs = mergeHashes(old.Configs, cur.Configs)
	merged.Tasks = mergeHashes(old.Tasks, cur.Tasks)
	merged.Mixed = append([]MixedEntry(nil), old.Mixed...)

	if len(mismatches) > 0 {
		diffs := make([]string, len(mismatches))
		for i, m := range mismatches {
			diffs[i] = m.String()
		}
		merged.Mixed = append(merged.Mixed, MixedEntry{
			At:           merged.Updated,
			Orchestrator: cur.Orchestrator,
			ClaudeCLI:    cur.ClaudeCLI,
			Contributor:  cur.Contributor,
			Differences:  diffs,
		})
	}
	return merged
}

// New stamps a first manifest with the time it was made.
func (m Manifest) New(now time.Time) Manifest {
	m.Schema = ManifestSchema
	m.Created = now.UTC().Format(time.RFC3339)
	m.Updated = m.Created
	return m
}

func mergeHashes(old, cur map[string]string) map[string]string {
	out := make(map[string]string, len(old)+len(cur))
	for k, v := range old {
		out[k] = v
	}
	for k, v := range cur {
		out[k] = v
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	return slices.Sorted(maps.Keys(m))
}

// LoadManifest reads <dir>/manifest.json. found is false when the directory has none.
func LoadManifest(dir string) (m Manifest, found bool, err error) {
	path := filepath.Join(dir, ManifestName)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Manifest{}, false, nil
	}
	if err != nil {
		return Manifest{}, false, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, false, fmt.Errorf("%s: %w", path, err)
	}
	if m.Schema != ManifestSchema {
		return Manifest{}, false, fmt.Errorf("%s: schema %d, want %d", path, m.Schema, ManifestSchema)
	}
	return m, true, nil
}

// SaveManifest writes <dir>/manifest.json through a temporary file and a rename, so that a
// reader or a crash sees the old manifest or the new one.
func SaveManifest(dir string, m Manifest) (err error) {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, ".manifest-*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()

	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, ManifestName))
}
