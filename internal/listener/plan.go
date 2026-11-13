package listener

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kasorse/orch/internal/domain"
)

// The pure part of an Epic's plan (specs/listener-story/SPEC.md §5–§7): what its labels name, the
// catalog the selection chooses from, the validation of the selection's answer, and the chain of
// one service's tasks. Nothing here calls a model or reads a repository.

// KindPlan is Entry.Kind of an Epic that is planned, not run.
const KindPlan = "plan"

// ErrManyRefs is an Epic with more reference labels than listener.story.max_refs. The loop maps it,
// like a bad label, to blocked (SPEC §9).
var ErrManyRefs = errors.New("too many reference repositories")

// Bounds of the catalog and the selection's answer (SPEC §5.1, §5.2).
const (
	// MaxCatalogDescription bounds one project's description in the catalog, in runes.
	MaxCatalogDescription = 300
	// MaxWhyRunes bounds the reason the selection gives for one service, in runes.
	MaxWhyRunes = 200
)

// projectLabels are the distinct project paths the labels prefix<group>/<path> name, in the order
// the Project rules read them, sorted. With an empty prefix there are none.
func projectLabels(labels []string, prefix string) ([]string, error) {
	if prefix == "" {
		return nil, nil
	}
	var found []string
	for _, l := range labels {
		raw, ok := strings.CutPrefix(l, prefix)
		if !ok {
			continue
		}
		path := strings.TrimSuffix(raw, ".git")
		if !projectPattern.MatchString(path) || hasDotSegment(path) {
			return nil, fmt.Errorf("%w: %q is not <group>/<project> of letters, digits, . _ - (no . or .. segment)", ErrInvalidProject, raw)
		}
		if !slices.Contains(found, path) {
			found = append(found, path)
		}
	}
	slices.Sort(found)
	return found, nil
}

// Refs are the reference repositories an Epic names: the labels prefix<group>/<path>, parsed as
// Project parses a ticket's gitlab: label (the same segment rules, ".git" stripped), distinct and
// sorted. More than max is an error, so is a label that is not a project path.
func Refs(labels []string, prefix string, max int) ([]string, error) {
	refs, err := projectLabels(labels, prefix)
	if err != nil {
		return nil, err
	}
	if len(refs) > max {
		return nil, fmt.Errorf("%w: %d (%s), at most %d", ErrManyRefs, len(refs), strings.Join(refs, ", "), max)
	}
	return refs, nil
}

// EpicProjects are the projects an Epic's gitlab: labels name, which an Epic may have several of
// (SPEC §5.2, A2), distinct and sorted. None means the services are selected.
func EpicProjects(labels []string, prefix string) ([]string, error) {
	return projectLabels(labels, prefix)
}

// ServiceName is the service a project path becomes: its last segment.
func ServiceName(path string) string {
	return path[strings.LastIndex(path, "/")+1:]
}

// Catalog is the projects the selection chooses from (SPEC §5.1): every list's projects,
// deduplicated by path, sorted by path, at most max of them (the first ones by path), each with its
// description on one line, its control characters removed and cut to MaxCatalogDescription runes.
// cut is how many projects the limit left out.
func Catalog(lists [][]domain.Project, max int) (catalog []domain.Project, cut int) {
	seen := map[string]bool{}
	var all []domain.Project
	for _, list := range lists {
		for _, p := range list {
			if p.Path == "" || seen[p.Path] {
				continue
			}
			seen[p.Path] = true
			p.Description = cleanDescription(p.Description)
			all = append(all, p)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Path < all[j].Path })
	if len(all) > max {
		cut = len(all) - max
		all = all[:max]
	}
	return all, cut
}

// cleanDescription is a description as the selection's prompt shows it: one line, no control
// character, at most MaxCatalogDescription runes.
func cleanDescription(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > MaxCatalogDescription {
		s = string([]rune(s)[:MaxCatalogDescription])
	}
	return s
}

// Choice is one service the selection chose: a catalog path and why.
type Choice struct {
	Path string `json:"path"`
	Why  string `json:"why"`
}

// SelectionError is a selection's answer that breaks a rule of SPEC §5.2. Field names the field the
// way the answer spells it (services[1].path); it is "" for the answer as a whole.
type SelectionError struct {
	Field string
	Msg   string
}

func (e *SelectionError) Error() string {
	if e.Field == "" {
		return e.Msg
	}
	return e.Field + ": " + e.Msg
}

func selectionErr(field, format string, args ...any) *SelectionError {
	return &SelectionError{Field: field, Msg: fmt.Sprintf(format, args...)}
}

// ParseSelection checks the answer of the select call (SPEC §5.2) against the catalog: one JSON
// object {"services": [{"path": "...", "why": "..."}]} and nothing else, 1..max items, each path one
// of catalog, none a reference, none twice, no two with the same last segment (the service's name),
// each why one line of at most MaxWhyRunes runes. Any violation is a *SelectionError.
func ParseSelection(obj []byte, catalog, references []string, max int) ([]Choice, error) {
	var a struct {
		Services []Choice `json:"services"`
	}
	trimmed := bytes.TrimSpace(obj)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, selectionErr("", "the answer is not a JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return nil, selectionErr("", "the answer is not the expected JSON: %v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, selectionErr("", "the answer has data after its JSON object")
	}
	switch {
	case len(a.Services) == 0:
		return nil, selectionErr("services", "is empty")
	case len(a.Services) > max:
		return nil, selectionErr("services", "has %d items, more than listener.story.max_services (%d)", len(a.Services), max)
	}
	names := map[string]string{}
	seen := map[string]bool{}
	for i, c := range a.Services {
		field := func(name string) string { return "services[" + strconv.Itoa(i) + "]." + name }
		switch {
		case !slices.Contains(catalog, c.Path):
			return nil, selectionErr(field("path"), "%q is not a project of the catalog", oneLineCut(c.Path, 120))
		case slices.Contains(references, c.Path):
			return nil, selectionErr(field("path"), "%q is a reference repository, which is not changed and gets no task", c.Path)
		case seen[c.Path]:
			return nil, selectionErr(field("path"), "%q is chosen twice", c.Path)
		}
		seen[c.Path] = true
		name := strings.ToLower(ServiceName(c.Path))
		if other, dup := names[name]; dup {
			return nil, selectionErr(field("path"), "%q and %q have the same last segment, which is the service's name", other, c.Path)
		}
		names[name] = c.Path
		if !ValidService(ServiceName(c.Path)) {
			return nil, selectionErr(field("path"), "%q does not end in a service name", c.Path)
		}
		why := c.Why
		switch {
		case strings.TrimSpace(why) == "":
			return nil, selectionErr(field("why"), "is empty")
		case strings.ContainsFunc(why, func(r rune) bool { return unicode.IsControl(r) }):
			return nil, selectionErr(field("why"), "must be one line")
		case utf8.RuneCountInString(why) > MaxWhyRunes:
			return nil, selectionErr(field("why"), "is %d characters, over the %d limit", utf8.RuneCountInString(why), MaxWhyRunes)
		}
	}
	return a.Services, nil
}

func oneLineCut(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > n {
		return string([]rune(s)[:n]) + "..."
	}
	return s
}

// ChainTask is a task of a plan as the chain sees it: its ID and the service it changes.
type ChainTask struct {
	ID      string
	Service string
}

// Link says task Blocked waits for task Blocker, both of the same service (SPEC §7.2).
type Link struct {
	Blocked string `json:"blocked"`
	Blocker string `json:"blocker"`
}

// Chain is the links of a plan (SPEC §7.2, L4): the tasks of each service are chained in order, each
// blocked by the previous one of its service. order is the story's order of the task IDs
// (story.Order); a task not in it has no place in a chain. Tasks of different services are not
// linked. The links come in the order of their Blocked task in order.
func Chain(tasks []ChainTask, order []string) []Link {
	service := make(map[string]string, len(tasks))
	for _, t := range tasks {
		service[t.ID] = t.Service
	}
	last := map[string]string{}
	var links []Link
	for _, id := range order {
		s, ok := service[id]
		if !ok {
			continue
		}
		if prev, ok := last[s]; ok {
			links = append(links, Link{Blocked: id, Blocker: prev})
		}
		last[s] = id
	}
	return links
}

// PlanDir is the directory of a plan, relative to the listener's state directory, slash-separated:
// plans/<KEY>.<n>.
func PlanDir(key string, n int) string { return "plans/" + key + "." + strconv.Itoa(n) }

// ValidPlanDir reports whether dir is PlanDir(key, n) for some n of at least 1.
func ValidPlanDir(key, dir string) bool {
	rest, ok := strings.CutPrefix(dir, "plans/"+key+".")
	if !ok || rest == "" || len(rest) > 9 || strings.Trim(rest, "0123456789") != "" || rest[0] == '0' {
		return false
	}
	return true
}

// ServicesOfChoices are the paths of choices, in their order.
func ServicesOfChoices(choices []Choice) []string {
	paths := make([]string, len(choices))
	for i, c := range choices {
		paths[i] = c.Path
	}
	return paths
}
