package testreport

import (
	"encoding/json"
	"strings"
)

// goEvent is the part of a `go test -json` (test2json) event the parser reads.
type goEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
}

// goActions are the Actions test2json emits; a JSON line with another Action is not one of
// its events.
var goActions = map[string]bool{
	"start": true, "run": true, "pause": true, "cont": true, "pass": true, "bench": true,
	"fail": true, "output": true, "skip": true, "build-output": true, "build-fail": true,
}

// parseGoJSON reads `go test -json`: one JSON event per line, with other lines (build
// output, stderr) mixed in. A failed test is an event with Action "fail" and a Test; the
// package-level fail event has none.
func parseGoJSON(in input) ([]string, bool) {
	var names []string
	recognised := false
	for _, line := range in.lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var ev goEvent
		if json.Unmarshal([]byte(line), &ev) != nil || !goActions[ev.Action] {
			continue
		}
		recognised = true
		if ev.Action == "fail" && ev.Test != "" {
			names = append(names, joinName(".", ev.Package, ev.Test))
		}
	}
	return names, recognised
}
