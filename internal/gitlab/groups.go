package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/domain"
)

const (
	groupProjectsPerPage = 100
	// maxGroupPages bounds how many pages one listing follows: 5000 projects.
	maxGroupPages = 50
)

// groupPattern is a group path: one or more slash-separated segments of letters, digits, _ . -.
var groupPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`)

// nextLinkPattern finds the target of rel="next" in a Link header.
var nextLinkPattern = regexp.MustCompile(`<([^>]*)>\s*;[^,]*\brel="?next"?`)

// wireProject is the part of GitLab's project answer the catalog uses; the rest is ignored.
type wireProject struct {
	Path          *string `json:"path_with_namespace"`
	Name          *string `json:"name"`
	Description   *string `json:"description"`
	DefaultBranch *string `json:"default_branch"`
}

// GroupProjects lists the projects of group and of its subgroups, not the archived ones
// (specs/listener-story/SPEC.md §5.1): GET /api/v4/groups/<group>/projects, page after page by the
// Link header's rel="next" or X-Next-Page, only within gitlab.base_url. group must be, or be
// under, one of gitlab.allowed_groups; nothing is requested otherwise. A project whose path is not
// under group, fails the project path rules, or whose name or default branch is not usable is
// dropped, and the count is reported through Options.Logf. The answer is validated strictly: a
// page that is not a list of project records is invalid. The rules of every other call hold:
// GET only, no redirect, the token redacted, transient failures retried.
func (c *Client) GroupProjects(ctx context.Context, group string) ([]domain.Project, error) {
	group = strings.Trim(group, "/")
	what := "the projects of group " + group
	if err := c.checkGroup(group, what); err != nil {
		return nil, err
	}
	base, err := url.Parse(c.opts.BaseURL)
	if err != nil || base.Host == "" {
		return nil, c.fail(what, apperr.GitLabInvalidResponse, 0, "gitlab.base_url is not a URL", "", nil)
	}

	q := url.Values{"include_subgroups": {"true"}, "archived": {"false"}, "per_page": {strconv.Itoa(groupProjectsPerPage)}}
	next := apiPath + "/groups/" + url.PathEscape(group) + "/projects?" + q.Encode()
	var out []domain.Project
	var seen []string
	dropped := 0
	for pages := 0; next != ""; pages++ {
		if pages == maxGroupPages {
			return nil, c.fail(what, apperr.GitLabInvalidResponse, http.StatusOK, fmt.Sprintf("more than %d pages", maxGroupPages), "", nil)
		}
		if slices.Contains(seen, next) {
			return nil, c.fail(what, apperr.GitLabInvalidResponse, http.StatusOK, "the next page is one already read", "", nil)
		}
		seen = append(seen, next)
		raw, header, gerr := c.getWithHeader(ctx, next, what)
		if gerr != nil {
			return nil, gerr
		}
		var list []wireProject
		if err := json.Unmarshal(raw, &list); err != nil || list == nil {
			return nil, c.fail(what, apperr.GitLabInvalidResponse, http.StatusOK, "not a list of projects", "", nil)
		}
		for i, w := range list {
			p, ok, detail := toProject(w, group)
			if detail != "" {
				return nil, c.fail(what, apperr.GitLabInvalidResponse, http.StatusOK, fmt.Sprintf("project %d of the page: %s", i, detail), "", nil)
			}
			if !ok {
				dropped++
				continue
			}
			out = append(out, p)
		}
		target, detail := c.nextPage(base, header, next)
		if detail != "" {
			return nil, c.fail(what, apperr.GitLabInvalidResponse, http.StatusOK, detail, "", nil)
		}
		next = target
	}
	if dropped > 0 && c.opts.Logf != nil {
		c.opts.Logf("gitlab: %d projects of group %s dropped: outside the group or not usable", dropped, group)
	}
	return out, nil
}

// checkGroup refuses a group that is not under gitlab.allowed_groups before any request.
func (c *Client) checkGroup(group, what string) error {
	ok := groupPattern.MatchString(group) && !slices.ContainsFunc(strings.Split(group, "/"), func(s string) bool { return s == "." || s == ".." })
	if ok {
		ok = false
		for _, g := range c.opts.AllowedGroups {
			if g = strings.Trim(g, "/"); g != "" && (group == g || strings.HasPrefix(group, g+"/")) {
				ok = true
				break
			}
		}
	}
	if !ok {
		return &apperr.GitLabError{What: what, Kind: apperr.GitLabNotFound,
			Detail: c.opts.TokenEnv, Err: fmt.Errorf("%s is not under gitlab.allowed_groups; nothing was requested", truncateText(group, 80))}
	}
	return nil
}

// toProject validates one record. detail is set for a record that is not a project record at
// all (the answer is invalid); ok is false for a well-formed one that the catalog does not take.
func toProject(w wireProject, group string) (p domain.Project, ok bool, detail string) {
	if w.Path == nil || w.Name == nil {
		return p, false, "path_with_namespace or name missing"
	}
	path := *w.Path
	if !validProject(path) || !strings.HasPrefix(path, group+"/") {
		return p, false, ""
	}
	name := oneLine(*w.Name)
	if name == "" {
		return p, false, ""
	}
	branch := ""
	if w.DefaultBranch != nil {
		branch = *w.DefaultBranch
		if branch != "" && !plainRef(branch) {
			return p, false, ""
		}
	}
	desc := ""
	if w.Description != nil {
		desc = oneLine(*w.Description)
	}
	return domain.Project{Path: path, Name: name, Description: desc, DefaultBranch: branch}, true, ""
}

// nextPage is the path and query of the page after the one just read, "" for none. A Link
// header's rel="next" must stay on gitlab.base_url (same scheme and host, below its path, in the
// groups API); X-Next-Page is a page number of the same listing.
func (c *Client) nextPage(base *url.URL, header http.Header, current string) (string, string) {
	for _, h := range header.Values("Link") {
		m := nextLinkPattern.FindStringSubmatch(h)
		if m == nil {
			continue
		}
		u, err := url.Parse(m[1])
		if err != nil {
			return "", "the Link header's next page is not a URL"
		}
		if u.IsAbs() || u.Host != "" {
			if u.Scheme != base.Scheme || !strings.EqualFold(u.Host, base.Host) || u.User != nil {
				return "", "the Link header's next page is not on gitlab.base_url"
			}
		}
		prefix := base.Path + apiPath + "/groups/"
		if !strings.HasPrefix(u.Path, prefix) || u.Fragment != "" {
			return "", "the Link header's next page is not in the groups API of gitlab.base_url"
		}
		return strings.TrimPrefix(u.EscapedPath(), base.Path) + "?" + u.RawQuery, ""
	}
	n := strings.TrimSpace(header.Get("X-Next-Page"))
	if n == "" {
		return "", ""
	}
	page, err := strconv.Atoi(n)
	if err != nil || page <= 0 || strconv.Itoa(page) != n {
		return "", "X-Next-Page is not a page number"
	}
	path, query, _ := strings.Cut(current, "?")
	v, err := url.ParseQuery(query)
	if err != nil {
		return "", "the current page's query cannot be read"
	}
	v.Set("page", n)
	return path + "?" + v.Encode(), ""
}

// oneLine puts s on one line: control characters and line breaks become spaces, runs of space one.
func oneLine(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "..."
}
