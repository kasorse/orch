# SPEC.md — Jev Integration for AI Orchestrator MVP

## 1. Purpose

Integrate **Jev** as the decision layer of the AI Orchestrator.

Jev is responsible only for:

1. deciding whether project context is required;
2. classifying task complexity;
3. estimating which workflow stages are required.

The Orchestrator remains responsible for context collection, policy evaluation, workflow construction, stage ordering, and execution.

---

## 2. Scope

### In Scope

- Jev decision provider
- project-context pre-check
- project context collection
- task complexity classification
- required-stage classification
- configurable decision thresholds
- deterministic workflow construction
- fallback decision provider
- logging, metrics, and tests

### Out of Scope

Jev must not:

- generate implementation plans;
- write or modify code;
- execute tools;
- select tools or models;
- define stage order;
- retry failed workflow stages;
- evaluate final implementation quality.

---

## 3. High-Level Flow

```text
User Request
    |
    v
Jev Pre-Check
    |
    +--> context_depth = NONE
    |         |
    |         v
    |     Final Classification
    |
    +--> context_depth = PROJECT
              |
              v
       Context Manager
              |
              v
        ProjectContext
              |
              v
       Final Classification
              |
              v
         Policy Engine
              |
              v
       Workflow Builder
              |
              v
       Workflow Runner
```

The pre-check and final classification are separate decision rounds because final classification may depend on collected project context.

---

## 4. Context Decision

The first Jev decision receives only the user request.

For MVP:

```go
type ContextDepth string

const (
    ContextDepthNone    ContextDepth = "none"
    ContextDepthProject ContextDepth = "project"
)
```

Meaning:

- `NONE` — the task can be classified without inspecting the repository.
- `PROJECT` — repository context is required for reliable classification.

Use a bounded Jev choice decision.

---

## 5. Context Manager

The Context Manager runs only when:

```text
context_depth == PROJECT
```

It must collect a compact project summary rather than sending the entire repository.

### Sources

Where available, inspect:

- `AGENTS.md`
- `CLAUDE.md`
- `README.md`
- `CONTRIBUTING.md`
- relevant `docs/*.md`
- dependency files such as `go.mod`
- repository structure
- relevant source files
- relevant tests
- git status

### Output

```go
type ProjectContext struct {
    Language      string
    ProjectType   string
    Rules         []string
    RelevantFiles []RelevantFile
    Dependencies  []string
    TestInfo      TestInfo
    Git           GitContext
}
```

The context should contain summaries and constraints, not unrestricted source dumps.

---

## 6. Final Classification

Final classification receives:

```go
type ClassificationInput struct {
    Request string
    Project *ProjectContext
}
```

`Project` is `nil` when `context_depth == NONE`.

Jev must produce:

1. task complexity;
2. probability for each supported stage.

---

## 7. Complexity

Supported values:

```go
type Complexity string

const (
    ComplexitySimple  Complexity = "simple"
    ComplexityMedium  Complexity = "medium"
    ComplexityComplex Complexity = "complex"
)
```

Use a bounded choice decision.

General interpretation:

- `SIMPLE` — local change, low uncertainty, low regression risk.
- `MEDIUM` — multiple components/files, requires understanding existing code and tests.
- `COMPLEX` — architectural or cross-component change, high uncertainty or regression risk.

---

## 8. Workflow Stages

MVP stages:

```go
type Stage string

const (
    StageResearch    Stage = "research"
    StageAnalysis    Stage = "analysis"
    StagePlanning    Stage = "planning"
    StageDevelopment Stage = "development"
    StageTesting     Stage = "testing"
    StageReview      Stage = "review"
)
```

Each stage must be evaluated independently.

Conceptual output:

```json
{
  "analysis": 0.92,
  "research": 0.21,
  "planning": 0.81,
  "development": 0.99,
  "testing": 0.95,
  "review": 0.76
}
```

Do not ask Jev to return only a final stage list. Preserve the probabilities so workflow policy can remain deterministic and configurable.

---

## 9. Classification Result

```go
type TaskClassification struct {
    ContextDepth ContextDepth
    Complexity   ComplexityDecision
    Stages       StageDecisions
}

type ComplexityDecision struct {
    Value      Complexity
    Confidence float64
}

type ProbabilityDecision struct {
    Probability float64
}

type StageDecisions struct {
    Research    ProbabilityDecision
    Analysis    ProbabilityDecision
    Planning    ProbabilityDecision
    Development ProbabilityDecision
    Testing     ProbabilityDecision
    Review      ProbabilityDecision
}
```

Jev-specific transport types must not leak outside the Jev adapter.

---

## 10. Decision Provider Abstraction

Workflow code must not depend directly on Jev.

```go
type DecisionProvider interface {
    ContextDepth(
        ctx context.Context,
        request string,
    ) (ContextDepthDecision, error)

    Classify(
        ctx context.Context,
        input ClassificationInput,
    ) (TaskClassification, error)
}
```

Implement:

```text
DecisionProvider
|
+-- JevDecisionProvider
+-- LLMDecisionProvider     // fallback
```

---

## 11. Jev Adapter

Recommended structure:

```text
internal/
  decision/
    provider.go
    types.go

    jev/
      client.go
      provider.go
      mapper.go
      config.go

  context/
    manager.go
    project/

  policy/
    engine.go
    config.go

  workflow/
    builder.go
    runner.go
    stage.go
```

The Jev adapter is responsible for:

- API communication;
- authentication;
- request construction;
- response validation;
- mapping Jev responses to domain types.

---

## 12. Policy Engine

Jev provides probabilities. The Orchestrator decides whether a stage is enabled.

Example:

```yaml
workflow:
  thresholds:
    research: 0.70
    analysis: 0.60
    planning: 0.65
    development: 0.60
    testing: 0.60
    review: 0.70
```

Rule:

```text
stage_probability >= configured_threshold
    -> stage enabled
```

Complexity-based deterministic rules may also be applied.

Example:

```yaml
workflow:
  complexity_policy:
    complex:
      force_stages:
        - analysis
        - planning
        - review
```

---

## 13. Stage Ordering

Jev must not determine workflow order.

Ordering is deterministic:

```go
var stageOrder = []Stage{
    StageResearch,
    StageAnalysis,
    StagePlanning,
    StageDevelopment,
    StageTesting,
    StageReview,
}
```

The Workflow Builder filters this ordered list using the Policy Engine result.

---

## 14. Classification Service

Expected orchestration:

```go
func (s *Service) Classify(
    ctx context.Context,
    request string,
) (TaskClassification, error) {

    depth, err := s.decisions.ContextDepth(ctx, request)
    if err != nil {
        return TaskClassification{}, err
    }

    var project *ProjectContext

    if depth.Value == ContextDepthProject {
        project, err = s.context.Build(ctx, request)
        if err != nil {
            project = nil
        }
    }

    return s.decisions.Classify(
        ctx,
        ClassificationInput{
            Request: request,
            Project: project,
        },
    )
}
```

Context collection failure should not automatically abort classification. Classification may continue with limited context and must record that limitation.

---

## 15. Configuration

Example:

```yaml
decision:
  provider: jev

  jev:
    base_url: https://api.typesafe.ai
    api_key_env: JEV_API_KEY
    model: <model>
    timeout: 5s

  fallback:
    enabled: true
    provider: llm

workflow:
  thresholds:
    research: 0.70
    analysis: 0.60
    planning: 0.65
    development: 0.60
    testing: 0.60
    review: 0.70
```

API keys must be read from the environment and must not be stored in repository configuration.

---

## 16. Startup Validation

When Jev is configured as the active provider, validate:

- API key exists;
- base URL is valid;
- configured model is valid/available;
- timeout is valid;
- required workflow thresholds exist and are within `[0,1]`.

Invalid configuration must fail startup with a clear error.

---

## 17. Fallback

Jev failure must not necessarily stop the Orchestrator.

Fallback applies to:

- timeout;
- connection failure;
- temporary provider failure;
- invalid provider response;
- server-side error.

Flow:

```text
Jev
 |
 +-- success --> decision
 |
 +-- failure --> LLMDecisionProvider
```

For MVP, perform at most one fallback attempt.

The fallback provider must return the same domain models as Jev.

---

## 18. Security

The Context Manager must not send secrets to Jev.

At minimum, exclude:

```text
.env
.env.*
*.pem
*.key
credentials*
secrets*
```

Repository traversal should respect `.gitignore` where possible.

Do not send arbitrary binary files or unrestricted repository contents.

---

## 19. Observability

Structured logs should include:

```text
decision_provider
context_depth
project_context_used
context_limited
complexity
complexity_confidence
stage_probabilities
final_stages
fallback_used
latency
```

Minimum metrics:

```text
decision_requests_total
decision_errors_total
decision_fallback_total
decision_latency_seconds
project_context_requested_total
project_context_collection_seconds
```

Avoid high-cardinality labels such as request text, user IDs, or file paths.

---

## 20. Testing

### Unit Tests

Cover:

- `NONE` / `PROJECT` mapping;
- complexity mapping;
- stage threshold behavior;
- deterministic stage ordering;
- complexity policies;
- Jev response validation;
- Jev error -> fallback;
- context collection failure -> limited-context classification;
- configuration validation.

### Integration Test

Mock Jev and execute:

```text
request
  -> pre-check
  -> project context collection
  -> final classification
  -> policy
  -> workflow
```

Verify final stages and ordering.

---

## 21. Acceptance Criteria

The implementation is complete when:

1. Jev implements `DecisionProvider`.
2. Every request performs a context-depth decision first.
3. MVP supports `NONE` and `PROJECT`.
4. `PROJECT` invokes the Context Manager.
5. Context collection includes project rules, metadata, relevant files, dependencies, and tests.
6. Jev classifies complexity as `SIMPLE`, `MEDIUM`, or `COMPLEX`.
7. Jev independently evaluates all supported stages.
8. Stage probabilities are preserved.
9. Policy Engine converts probabilities into enabled stages.
10. Stage ordering is deterministic and not controlled by Jev.
11. Jev transport types remain isolated inside the Jev adapter.
12. Fallback to an LLM decision provider is supported.
13. Secrets are excluded from project context.
14. Logs and metrics are implemented.
15. Unit and integration tests pass.

---

## 22. Definition of Done

- Jev client implemented.
- Jev provider implemented.
- Decision provider abstraction implemented.
- Context-depth pre-check implemented.
- Context Manager implemented.
- ProjectContext model implemented.
- Complexity classification implemented.
- Stage classification implemented.
- Policy Engine implemented.
- Workflow Builder integrated.
- Deterministic stage ordering implemented.
- Fallback provider implemented.
- Startup validation implemented.
- Security exclusions implemented.
- Logs and metrics implemented.
- Unit tests implemented.
- Integration test implemented.
- Configuration documentation updated.

---

## 23. MVP Responsibility Boundary

```text
Jev
  -> decide required context
  -> classify complexity
  -> estimate required stages

Context Manager
  -> collect relevant repository context

Policy Engine
  -> convert probabilities into deterministic decisions

Workflow Builder
  -> order selected stages

Workflow Runner
  -> execute stages using configured AI providers and tools
```

The core rule is:

> **Jev provides bounded probabilistic decisions; the Orchestrator retains deterministic control over workflow construction and execution.**
