// Package classifier turns a raw task description into a structured domain.Classification
// via a single AI call, with strict parsing/validation (malformed output is a hard
// error, not something the workflow retries).
package classifier

import (
	"context"
	"encoding/json"
	"errors"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/provider"
)

var errNoJSONObject = errors.New("no JSON object found in classifier output")

type Classifier struct {
	Client provider.Client
	Model  string
}

func New(client provider.Client, model string) *Classifier {
	return &Classifier{Client: client, Model: model}
}

func (c *Classifier) Classify(ctx context.Context, task string) (domain.Classification, error) {
	prompt := BuildClassifyPrompt(task)

	result, err := c.Client.Execute(ctx, provider.Request{Prompt: prompt, Model: c.Model})
	if err != nil {
		return domain.Classification{}, err
	}

	raw := extractJSONObject(result.Output)
	if raw == "" {
		return domain.Classification{}, &apperr.ClassificationError{
			Raw: result.Output,
			Err: errNoJSONObject,
		}
	}

	var classification domain.Classification
	if err := json.Unmarshal([]byte(raw), &classification); err != nil {
		return domain.Classification{}, &apperr.ClassificationError{Raw: result.Output, Err: err}
	}

	if err := classification.Validate(); err != nil {
		return domain.Classification{}, &apperr.ClassificationError{Raw: result.Output, Err: err}
	}

	return classification, nil
}

// extractJSONObject finds the first top-level {...} object in s, tolerant of markdown
// code fences or leading/trailing prose the model might add despite instructions.
func extractJSONObject(s string) string {
	start := -1
	depth := 0
	for i, r := range s {
		switch r {
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			if depth > 0 {
				depth--
				if depth == 0 && start != -1 {
					return s[start : i+1]
				}
			}
		}
	}
	return ""
}
