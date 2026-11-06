package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// APISpec keeps a project's OpenAPI document fresh and gives the agents the part a task needs.
// The decision and clarify steps have no tools, so the contract of every endpoint the task or
// its ticket names goes into the task text; the full file stays in the repository (Path) for
// the steps that read code.
type APISpec struct {
	URL    string `yaml:"url"`
	Path   string `yaml:"path"`    // relative to local.dir, git-ignored, e.g. swagger-cache/swagger.json
	MaxAge string `yaml:"max_age"` // refresh when older, default 1h
	// MaxBytes caps the contract added to a task, default 12000.
	MaxBytes int `yaml:"max_bytes"`
}

func (a *APISpec) file(dir string) string {
	if filepath.IsAbs(a.Path) {
		return a.Path
	}
	return filepath.Join(dir, a.Path)
}

// Refresh downloads the document when the copy is older than MaxAge. A failed download keeps
// the old copy and is reported, not fatal.
func (a *APISpec) Refresh(ctx context.Context, dir string) (fresh bool, err error) {
	maxAge := time.Hour
	if d, err := time.ParseDuration(a.MaxAge); err == nil {
		maxAge = d
	}
	path := a.file(dir)
	if st, err := os.Stat(path); err == nil && time.Since(st.ModTime()) < maxAge {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("%s: HTTP %d", a.URL, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return false, err
	}
	var probe struct {
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if json.Unmarshal(body, &probe) != nil || len(probe.Paths) == 0 {
		return false, fmt.Errorf("%s is not an OpenAPI document", a.URL)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return false, err
	}
	return true, os.Rename(tmp, path)
}

// endpointRE finds api-like paths in text: "shop/orders/item/query", "{{shop/x}}",
// "/api/v1/orders/{id}".
var endpointRE = regexp.MustCompile(`[A-Za-z0-9_{}.-]+(?:/[A-Za-z0-9_{}.-]+)+`)

// Contract renders the operations whose path ends with an endpoint named in text, with their
// schemas resolved, as Markdown for the task. "" when none matches.
func (a *APISpec) Contract(dir, text string) (string, error) {
	raw, err := os.ReadFile(a.file(dir))
	if err != nil {
		return "", err
	}
	var doc struct {
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", err
	}
	var wanted []string
	seen := map[string]bool{}
	for _, m := range endpointRE.FindAllString(text, -1) {
		m = strings.Trim(strings.ToLower(m), "/.{}")
		if strings.Contains(m, "://") || strings.Count(m, "/") < 1 {
			continue
		}
		for p := range doc.Paths {
			if !seen[p] && strings.HasSuffix(strings.ToLower(p), "/"+m) {
				seen[p] = true
				wanted = append(wanted, p)
			}
		}
	}
	if len(wanted) == 0 {
		return "", nil
	}
	sort.Strings(wanted)
	r := schemaRenderer{schemas: doc.Components.Schemas}
	var b strings.Builder
	fmt.Fprintf(&b, "API contract (from %s, the full OpenAPI document is %s):\n", a.URL, a.Path)
	for _, p := range wanted {
		for _, method := range []string{"get", "post", "put", "patch", "delete"} {
			op, ok := doc.Paths[p][method]
			if !ok {
				continue
			}
			var o struct {
				Parameters []struct {
					Name, In string
					Required bool
				} `json:"parameters"`
				RequestBody struct {
					Content map[string]struct {
						Schema json.RawMessage `json:"schema"`
					} `json:"content"`
				} `json:"requestBody"`
				Responses map[string]struct {
					Content map[string]struct {
						Schema json.RawMessage `json:"schema"`
					} `json:"content"`
				} `json:"responses"`
			}
			_ = json.Unmarshal(op, &o)
			fmt.Fprintf(&b, "\n%s %s\n", strings.ToUpper(method), p)
			for _, prm := range o.Parameters {
				if prm.In == "header" {
					continue
				}
				fmt.Fprintf(&b, "  param %s (%s)%s\n", prm.Name, prm.In, map[bool]string{true: " required"}[prm.Required])
			}
			if c, ok := o.RequestBody.Content["application/json"]; ok {
				fmt.Fprintf(&b, "  request: %s\n", r.render(c.Schema, 0))
			}
			if res, ok := o.Responses["200"]; ok {
				if c, ok := res.Content["application/json"]; ok {
					fmt.Fprintf(&b, "  response 200: %s\n", r.render(c.Schema, 0))
				}
			}
		}
	}
	out := b.String()
	limit := a.MaxBytes
	if limit <= 0 {
		limit = 12000
	}
	if len(out) > limit {
		out = out[:limit] + "\n… (cut; see " + a.Path + ")\n"
	}
	return out, nil
}

type schemaRenderer struct{ schemas map[string]json.RawMessage }

// render writes a schema as a compact TypeScript-like type, resolving $ref a few levels deep.
func (r schemaRenderer) render(raw json.RawMessage, depth int) string {
	var s struct {
		Ref        string                     `json:"$ref"`
		Type       string                     `json:"type"`
		Format     string                     `json:"format"`
		Nullable   bool                       `json:"nullable"`
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
		Items      json.RawMessage            `json:"items"`
		Enum       []any                      `json:"enum"`
		Desc       string                     `json:"description"`
		AllOf      []json.RawMessage          `json:"allOf"`
	}
	if json.Unmarshal(raw, &s) != nil {
		return "unknown"
	}
	if s.Ref != "" {
		name := s.Ref[strings.LastIndex(s.Ref, "/")+1:]
		if depth >= 8 {
			return name
		}
		def, ok := r.schemas[name]
		if !ok {
			return name
		}
		return r.render(def, depth+1)
	}
	if len(s.AllOf) == 1 {
		return r.render(s.AllOf[0], depth)
	}
	t := s.Type
	switch {
	case len(s.Enum) > 0:
		var v []string
		for _, e := range s.Enum {
			v = append(v, fmt.Sprintf("%v", e))
		}
		t = strings.Join(v, "|")
	case s.Type == "array" && s.Items != nil:
		t = r.render(s.Items, depth) + "[]"
	case len(s.Properties) > 0:
		req := map[string]bool{}
		for _, n := range s.Required {
			req[n] = true
		}
		names := make([]string, 0, len(s.Properties))
		for n := range s.Properties {
			names = append(names, n)
		}
		sort.Strings(names)
		var f []string
		for _, n := range names {
			opt := "?"
			if req[n] {
				opt = ""
			}
			f = append(f, n+opt+": "+r.render(s.Properties[n], depth+1))
		}
		t = "{" + strings.Join(f, "; ") + "}"
	case s.Type == "integer" || s.Type == "number":
		t = "number"
	case s.Type == "":
		t = "unknown"
	}
	if strings.HasPrefix(s.Desc, "Possible values: ") && s.Type == "string" {
		t = strings.ReplaceAll(strings.TrimPrefix(s.Desc, "Possible values: "), "; ", "|")
	}
	if s.Nullable {
		t += " | null"
	}
	return t
}
