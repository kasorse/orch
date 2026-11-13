package listener

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/kasorse/orch/internal/domain"
)

// The labels of a child and the bodies of the Epic's comments (specs/listener-story/SPEC.md §8.1,
// §9). Like the other comments they are Go's wiki templates: every text a model wrote is escaped.

// ChildLabels are the labels of a child ticket (SPEC §8.1): the queue's label, the service's label,
// the project's label, then the labels of jira.create.labels, without a duplicate and in that order.
// A label whose prefix is empty is left out.
func ChildLabels(queryLabel, servicePrefix, service, projectPrefix, path string, extra []string) []string {
	var labels []string
	add := func(l string) {
		if strings.TrimSpace(l) != "" && !containsString(labels, l) {
			labels = append(labels, l)
		}
	}
	add(queryLabel)
	if servicePrefix != "" {
		add(servicePrefix + service)
	}
	if projectPrefix != "" {
		add(projectPrefix + path)
	}
	for _, l := range extra {
		add(l)
	}
	return labels
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// PlanService is a service of a plan as the comment names it: its name and, when the selection chose
// it, why.
type PlanService struct{ Name, Why string }

// PlanChild is a child ticket of a plan: its key, its service, its title, and the key of the child
// it waits for ("" for none).
type PlanChild struct{ Key, Service, Title, BlockedBy string }

// PlanComment is what the Epic's comment says of a plan whose children were created.
type PlanComment struct {
	Epic     string
	Services []PlanService
	Children []PlanChild
	// References are the paths of the reference repositories.
	References []string
	// Warning is a line of its own, such as the children's status that the poll will not pick (§8.1).
	Warning string
	// Cost is the plan's cost as printed, "" when it is not known.
	Cost string
}

// keyText is an issue key as it is when it has the shape of one, else escaped.
func keyText(esc Escape, key string) string {
	if domain.ValidTicketKey(key) {
		return key
	}
	return esc(oneLine(key))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// childTable is the table of children. With blockedBy the last column names what each waits for.
func childTable(esc Escape, children []PlanChild, blockedBy bool) []string {
	head := "||Issue||Service||Title||"
	if blockedBy {
		head += "Blocked by||"
	}
	rows := []string{head}
	for _, c := range children {
		row := "|" + keyText(esc, c.Key) + "|" + text(esc, c.Service) + "|" + text(esc, c.Title) + "|"
		if blockedBy {
			by := "—"
			if c.BlockedBy != "" {
				by = keyText(esc, c.BlockedBy)
			}
			row += by + "|"
		}
		rows = append(rows, row)
	}
	return rows
}

// PlanCreated is the comment of an Epic whose children were created (SPEC §9): the services with
// their reasons, the table of children, the references, a warning about the children's status, and
// the plan's cost.
func PlanCreated(esc Escape, c PlanComment) []string {
	n := len(c.Children)
	body := []string{fmt.Sprintf("Planned: %d child %s created under %s.", n, plural(n, "ticket", "tickets"), keyText(esc, c.Epic))}
	if len(c.Services) > 0 {
		body = append(body, "", "Services:")
		for _, s := range c.Services {
			line := "* " + text(esc, s.Name)
			if strings.TrimSpace(s.Why) != "" {
				line += ": " + text(esc, s.Why)
			}
			body = append(body, line)
		}
	}
	body = append(body, "", "Children:")
	body = append(body, childTable(esc, c.Children, true)...)
	if len(c.References) > 0 {
		body = append(body, "", "References (read only, no task):")
		for _, r := range c.References {
			body = append(body, "* "+text(esc, r))
		}
	}
	if strings.TrimSpace(c.Warning) != "" {
		body = append(body, "", text(esc, c.Warning))
	}
	if c.Cost != "" {
		body = append(body, "", "Planning cost: "+esc(oneLine(c.Cost)))
	}
	return body
}

// PlanFailed is the comment of an Epic whose plan stopped after, or before, it made children
// (SPEC §9): the reason, and the children created so far.
func PlanFailed(esc Escape, reason string, created []PlanChild) []string {
	if strings.TrimSpace(reason) == "" {
		reason = "the plan stopped"
	}
	body := []string{"The plan did not finish: " + text(esc, reason)}
	if len(created) > 0 {
		body = append(body, "", "Created before it stopped:")
		body = append(body, childTable(esc, created, false)...)
	}
	return body
}

// maxExistingKeys bounds the keys ExistingChildren names.
const maxExistingKeys = 20

// ExistingChildren is the reason an Epic that has children already is blocked (SPEC §9, A9): their
// keys, and the label that plans it again.
func ExistingChildren(epic string, keys []string, restartLabel string) string {
	shown := keys
	more := ""
	if len(keys) > maxExistingKeys {
		shown = keys[:maxExistingKeys]
		more = " and " + strconv.Itoa(len(keys)-maxExistingKeys) + " more"
	}
	reason := fmt.Sprintf("%s has %s created by the listener already: %s%s.", epic, plural(len(keys), "a child ticket", "child tickets"), strings.Join(shown, ", "), more)
	if restartLabel != "" {
		reason += " Add the label " + restartLabel + " to plan it again."
	}
	return reason
}
