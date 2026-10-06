package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"orchestrator/internal/apperr"
	"orchestrator/internal/decision"
	"orchestrator/internal/domain"
)

// Provider is the Jev decision provider. It renders the shared questions
// (internal/decision) as Jev questions, sends each decision round as one request, and
// maps the answers back to domain types.
type Provider struct {
	client  Client
	model   string
	sources []domain.ComplexitySource
}

var _ decision.Provider = (*Provider)(nil)

// NewProvider builds the provider. model is the Jev model sent with every request.
// sources are the complexity questions to ask (TASKS.md В1): the primary one, plus the
// other when shadow is on. The provider does not know which is primary - it returns
// every distribution it asked for and the policy picks.
func NewProvider(client Client, model string, sources []domain.ComplexitySource) (*Provider, error) {
	if client == nil {
		return nil, fmt.Errorf("jev provider: client is required")
	}
	if model == "" {
		return nil, fmt.Errorf("jev provider: model is required")
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("jev provider: at least one complexity source is required")
	}
	seen := make(map[domain.ComplexitySource]bool, len(sources))
	for _, src := range sources {
		if _, err := domain.ParseComplexitySource(string(src)); err != nil {
			return nil, fmt.Errorf("jev provider: %w", err)
		}
		if seen[src] {
			return nil, fmt.Errorf("jev provider: complexity source %q listed twice", src)
		}
		seen[src] = true
	}
	return &Provider{client: client, model: model, sources: append([]domain.ComplexitySource(nil), sources...)}, nil
}

func (p *Provider) Name() string { return ProviderName }

func (p *Provider) Source() domain.DecisionSource { return domain.DecisionSourceJev }

func (p *Provider) ContextDepth(ctx context.Context, request string) (domain.ContextDepthDecision, error) {
	resp, err := p.client.decide(ctx, p.contextDepthRequest(request))
	if err != nil {
		return domain.ContextDepthDecision{}, err
	}
	d, err := mapContextDepth(resp)
	if err != nil {
		return domain.ContextDepthDecision{}, withUsage(err, resp)
	}
	d.Usage = responseUsage(resp)
	return d, nil
}

func (p *Provider) Classify(ctx context.Context, input domain.ClassificationInput) (domain.TaskClassification, error) {
	resp, err := p.client.decide(ctx, p.classificationRequest(input))
	if err != nil {
		return domain.TaskClassification{}, err
	}
	tc, err := mapClassification(resp, input, p.sources)
	if err != nil {
		return domain.TaskClassification{}, withUsage(err, resp)
	}
	tc.Usage = responseUsage(resp)
	return tc, nil
}

// responseUsage is the tokens Jev reports for one request (it reports no cost). The
// offline mock's numbers are invented, so a mock answer records none.
func responseUsage(resp systemOneResponse) *domain.Usage {
	if strings.HasPrefix(resp.Model, MockModelPrefix) {
		return nil
	}
	return &domain.Usage{InputTokens: int64(resp.Usage.InputTokens), OutputTokens: int64(resp.Usage.OutputTokens)}
}

// withUsage puts the usage of an answer that failed mapping on its DecisionError: the
// request was still paid for.
func withUsage(err error, resp systemOneResponse) error {
	var derr *apperr.DecisionError
	if errors.As(err, &derr) {
		derr.Usage = responseUsage(resp)
	}
	return err
}

func (p *Provider) contextDepthRequest(request string) systemOneRequest {
	q := decision.ContextDepthQuestion()
	return systemOneRequest{
		Model:     p.model,
		State:     decision.PreCheckState{Request: request},
		Questions: map[string]question{q.ID: choiceQuestion(q)},
	}
}

func (p *Provider) classificationRequest(input domain.ClassificationInput) systemOneRequest {
	questions := make(map[string]question, len(p.sources)+len(domain.StageOrder()))
	for _, src := range p.sources {
		switch src {
		case domain.ComplexitySourceChoice:
			q := decision.ComplexityChoiceQuestion()
			questions[q.ID] = choiceQuestion(q)
		case domain.ComplexitySourceScore:
			q := decision.ComplexityScoreQuestion()
			questions[q.ID] = scoreQuestion(q)
		}
	}
	for _, q := range decision.StageQuestions() {
		questions[q.ID] = yesNoQuestion(q)
	}

	return systemOneRequest{Model: p.model, State: decision.NewClassificationState(input), Questions: questions}
}

func choiceQuestion(q decision.ChoiceQuestion) question {
	return question{Type: questionChoice, Instructions: q.Instructions, Criteria: orderedCriteria(q.Options)}
}

// orderedCriteria is a choice question's option -> description object, encoded in the
// questions' own order (simple, medium, complex) rather than a Go map's alphabetical
// one: JSON objects are unordered, but the model reads the text in sequence.
type orderedCriteria []decision.Option

func (c orderedCriteria) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, o := range c {
		if i > 0 {
			b.WriteByte(',')
		}
		k, err := json.Marshal(o.Value)
		if err != nil {
			return nil, err
		}
		v, err := json.Marshal(o.Description)
		if err != nil {
			return nil, err
		}
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func scoreQuestion(q decision.ScoreQuestion) question {
	return question{Type: questionScore, Instructions: q.Instructions, Criteria: append([]string(nil), q.Levels...)}
}

func yesNoQuestion(q decision.YesNoQuestion) question {
	return question{Type: questionNoul, Instructions: q.Instructions, Criteria: map[string]string{
		"true":  q.True,
		"false": q.False,
	}}
}
