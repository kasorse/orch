package integration

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// jevKey is the API key the fake Jev accepts; tests put it in JEV_API_KEY.
const jevKey = "test-jev-key"

// jevScript is what the fake Jev answers (TASKS.md T13: a mock Jev that replies by
// question id). Stages not listed get 0.05.
type jevScript struct {
	PProject float64
	// Choice and Score are the complexity distributions (simple, medium, complex) of the
	// choice and score questions; Score defaults to Choice.
	Choice [3]float64
	Score  *[3]float64
	Stages map[string]float64
	// Fail lists statuses to answer the first requests with, before answering normally.
	Fail []int
	// Models is what GET /v1/models lists (TASKS.md T14); ModelsStatus, when set, is the
	// status that request gets instead.
	Models       []string
	ModelsStatus int
}

// scriptBugfixMedium decides analysis -> development -> testing -> review.
func scriptBugfixMedium() jevScript {
	return jevScript{
		PProject: 0.9,
		Choice:   [3]float64{0.1, 0.7, 0.2},
		Stages:   map[string]float64{"analysis": 0.9, "development": 0.95, "testing": 0.9, "review": 0.8},
	}
}

// scriptBugfixLow decides development -> testing -> review.
func scriptBugfixLow() jevScript {
	return jevScript{
		PProject: 0.9,
		Choice:   [3]float64{0.8, 0.15, 0.05},
		Stages:   map[string]float64{"development": 0.95, "testing": 0.9, "review": 0.8},
	}
}

type fakeJev struct {
	t      *testing.T
	script jevScript
	srv    *httptest.Server

	mu     sync.Mutex
	bodies []string
	// modelRequests counts GET /v1/models, which bodies and Fail leave out: they are about
	// the decision rounds.
	modelRequests int
}

// startJev runs a fake Jev and points JEV_API_KEY at the key it accepts.
func startJev(t *testing.T, script jevScript) *fakeJev {
	t.Helper()
	j := &fakeJev{t: t, script: script}
	j.srv = httptest.NewServer(http.HandlerFunc(j.handle))
	t.Cleanup(j.srv.Close)
	t.Setenv("JEV_API_KEY", jevKey)
	return j
}

func (j *fakeJev) URL() string { return j.srv.URL }

// Bodies returns every request body received, in order.
func (j *fakeJev) Bodies() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]string(nil), j.bodies...)
}

// ModelRequests returns how many times GET /v1/models was asked.
func (j *fakeJev) ModelRequests() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.modelRequests
}

func (j *fakeJev) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
		j.handleModels(w, r)
		return
	}
	raw, _ := io.ReadAll(r.Body)
	j.mu.Lock()
	j.bodies = append(j.bodies, string(raw))
	n := len(j.bodies)
	j.mu.Unlock()

	if r.Header.Get("Authorization") != "Bearer "+jevKey {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"Missing or invalid API key"}`)
		return
	}
	if n <= len(j.script.Fail) {
		w.WriteHeader(j.script.Fail[n-1])
		_, _ = io.WriteString(w, `{"error":"scripted failure"}`)
		return
	}

	var req struct {
		Model     string `json:"model"`
		Questions map[string]struct {
			Type     string          `json:"type"`
			Criteria json.RawMessage `json:"criteria"`
		} `json:"questions"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		w.WriteHeader(http.StatusUnprocessableEntity)
		return
	}

	answers := map[string]any{}
	for id, q := range req.Questions {
		switch {
		case id == "context_depth":
			answers[id] = choice(map[string]float64{"none": 1 - j.script.PProject, "project": j.script.PProject})
		case id == "complexity":
			answers[id] = choice(levels(j.script.Choice, "simple", "medium", "complex"))
		case id == "complexity_score":
			dist := j.script.Choice
			if j.script.Score != nil {
				dist = *j.script.Score
			}
			var legend []string
			_ = json.Unmarshal(q.Criteria, &legend)
			answers[id] = score(dist, legend)
		case strings.HasPrefix(id, "stage_"):
			p, ok := j.script.Stages[strings.TrimPrefix(id, "stage_")]
			if !ok {
				p = 0.05
			}
			answers[id] = map[string]any{"type": "noul", "noul": p}
		default:
			j.t.Errorf("fake jev: unexpected question %q", id)
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"model":   req.Model,
		"answers": answers,
		"usage":   map[string]int{"input_tokens": len(raw) / 4, "output_tokens": len(answers)},
	})
}

func levels(d [3]float64, names ...string) map[string]float64 {
	return map[string]float64{names[0]: d[0], names[1]: d[1], names[2]: d[2]}
}

func choice(probs map[string]float64) map[string]any {
	best, bestP := "", -1.0
	for k, p := range probs {
		if p > bestP {
			best, bestP = k, p
		}
	}
	return map[string]any{"type": "choice", "choice": best, "probabilities": probs, "confidence": bestP}
}

func score(d [3]float64, legend []string) map[string]any {
	probs, leg := map[string]float64{}, map[string]string{}
	s, conf := 0.0, 0.0
	for i, p := range d {
		k := strconv.Itoa(i)
		probs[k] = p
		if i < len(legend) {
			leg[k] = legend[i]
		}
		s += float64(i) * p
		conf = max(conf, p)
	}
	return map[string]any{"type": "score", "score": s, "legend": leg, "probabilities": probs, "confidence": conf}
}

func (j *fakeJev) handleModels(w http.ResponseWriter, r *http.Request) {
	j.mu.Lock()
	j.modelRequests++
	j.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+jevKey {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"Missing or invalid API key"}`)
		return
	}
	if j.script.ModelsStatus != 0 {
		w.WriteHeader(j.script.ModelsStatus)
		_, _ = io.WriteString(w, `{"error":"scripted failure"}`)
		return
	}
	models := []map[string]string{}
	for _, name := range j.script.Models {
		models = append(models, map[string]string{"name": name, "description": "", "release_date": "2026-01-01"})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"models": models})
}
