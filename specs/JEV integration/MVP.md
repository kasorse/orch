# MVP Specification: Jev Integration into AI Orchestrator

## 1. Цель

Добавить **Jev** в AI Orchestrator как специализированный **Decision Engine**, который используется для:

1. определения необходимости анализа текущего проекта;
2. определения сложности пользовательской задачи;
3. определения stages, необходимых для выполнения задачи.

Jev не выполняет stages самостоятельно и не заменяет Claude, Codex, Gemini или другие generative models.

Основная ответственность Jev:

```text
Request
   ↓
Need project context?
   ↓
Task complexity
   ↓
Required stages
```

Основная ответственность Orchestrator:

```text
collect context
   ↓
apply policies
   ↓
build workflow
   ↓
execute stages
```

---

# 2. Основной принцип

Архитектура должна следовать принципу:

```text
Jev             → decides
Context Manager → collects information
Orchestrator    → builds and controls workflow
LLM             → reasons / generates
Tools           → execute actions
```

Jev не должен иметь прямого доступа к filesystem, git, shell или другим tools.

Jev получает только тот state, который явно передал ему Orchestrator.

---

# 3. MVP Flow

Основной flow:

```text
                         USER REQUEST
                              │
                              ▼
                    ┌──────────────────┐
                    │   JEV PRE-CHECK  │
                    │                  │
                    │ Does this task   │
                    │ require project  │
                    │ context?         │
                    └────────┬─────────┘
                             │
                       context_depth
                             │
                  ┌──────────┴──────────┐
                  │                     │
                NONE                 PROJECT
                  │                     │
                  │                     ▼
                  │           ┌──────────────────┐
                  │           │ Context Manager  │
                  │           └────────┬─────────┘
                  │                    │
                  │              inspect project
                  │                    │
                  │        ┌───────────┼─────────────┐
                  │        ▼           ▼             ▼
                  │    project docs  metadata    relevant code
                  │                                   │
                  │        ┌───────────┴───────────────┘
                  │        │
                  │        ▼
                  │   PROJECT CONTEXT
                  │        │
                  └────────┼─────────────────┐
                           │                 │
                           ▼                 │
                  ┌─────────────────┐        │
                  │ JEV CLASSIFIER  │◄───────┘
                  │                 │
                  │ complexity      │
                  │ required stages │
                  └────────┬────────┘
                           │
                           ▼
                   ┌───────────────┐
                   │ Policy Engine │
                   └───────┬───────┘
                           │
                           ▼
                  ┌─────────────────┐
                  │Workflow Builder │
                  └────────┬────────┘
                           │
                           ▼
              analysis → planning → development
                       → testing → review
```

---

# 4. Stage 1 — Jev Pre-Check

При получении запроса Orchestrator сначала вызывает Jev только с текстом пользовательского запроса.

Цель первого вызова:

> Определить, требуется ли информация о текущем repository для корректной классификации задачи.

Для MVP используется `Choice`:

```text
context_depth:

NONE
PROJECT
```

## NONE

Означает, что задача может быть классифицирована без чтения текущего проекта.

Примеры:

```text
Explain what a goroutine is.
```

```text
What does Bitmap Heap Scan mean?
```

```text
Compare REST and gRPC.
```

Flow:

```text
request
   ↓
Jev
   ↓
context_depth = NONE
   ↓
skip Context Manager
   ↓
Jev classification
```

---

## PROJECT

Означает, что сложность и необходимые stages зависят от текущего codebase.

Примеры:

```text
Add caching to ProductRepository.
```

```text
Fix product validation.
```

```text
Refactor this service to support retries.
```

```text
Add integration tests for the current implementation.
```

Flow:

```text
request
   ↓
Jev
   ↓
context_depth = PROJECT
   ↓
Context Manager
   ↓
ProjectContext
   ↓
Jev classification
```

---

# 5. Почему используется context_depth, а не needs_project_context

В MVP фактически нужны два значения:

```text
NONE
PROJECT
```

Но API и domain model должны называться:

```go
ContextDepth
```

а не:

```go
NeedsProjectContext bool
```

Чтобы позднее можно было без breaking changes добавить:

```text
NONE
BASIC
RELEVANT
DEEP
```

Например:

```go
type ContextDepth string

const (
    ContextDepthNone    ContextDepth = "none"
    ContextDepthProject ContextDepth = "project"
)
```

---

# 6. Context Manager

Context Manager запускается только если:

```text
context_depth == PROJECT
```

Его ответственность:

> Собрать минимально необходимый контекст проекта, который позволит Jev корректнее определить сложность и необходимые stages.

Context Manager не принимает решение о сложности задачи.

---

# 7. Project Context

Для MVP необходимо собирать следующие данные.

## 7.1 Project instructions

При наличии:

```text
AGENTS.md
CLAUDE.md
README.md
CONTRIBUTING.md
```

Также допускаются:

```text
docs/*.md
```

Но нельзя автоматически отправлять Jev все Markdown-файлы проекта.

Context Manager должен выбирать только релевантные документы либо извлекать из них project rules.

Пример:

```text
Project rules:

- repository changes require integration tests
- generated files must not be modified
- all DB changes require migrations
```

---

# 8. Project metadata

Собрать базовую информацию:

```text
language
framework
project/module name
dependency files
test infrastructure
repository structure
git status
```

Пример:

```json
{
  "language": "go",
  "dependency_file": "go.mod",
  "tests_present": true,
  "git_repository": true
}
```

---

# 9. Relevant files

Context Manager должен попытаться определить файлы, связанные с запросом.

Например:

```text
User:

Add caching to ProductRepository
```

Context Manager может найти:

```text
internal/product/repository.go
internal/product/storage/postgres.go
internal/product/service.go
internal/product/storage/postgres_test.go
```

Необходимо собрать:

```text
relevant files
related interfaces
implementations
tests
dependencies
```

Для MVP необязательно передавать полный source code.

Предпочтительный результат:

```text
Relevant code:

ProductRepository
    interface

PostgresProductRepository
    implementation

ProductService
    depends on ProductRepository

Tests
    repository integration tests exist

Cache
    no existing cache implementation
```

---

# 10. Ограничение Project Context

Нельзя делать:

```text
cat **/*
   ↓
Jev
```

Context Manager должен формировать компактный state.

Причины:

* уменьшение token usage;
* меньший noise;
* повышение качества классификации;
* predictable behaviour;
* возможность логировать причину решения;
* снижение стоимости.

---

# 11. ProjectContext Model

Пример domain model:

```go
type ProjectContext struct {
    Language string `json:"language,omitempty"`

    ProjectType string `json:"project_type,omitempty"`

    Rules []string `json:"rules,omitempty"`

    RelevantFiles []RelevantFile `json:"relevant_files,omitempty"`

    Dependencies []string `json:"dependencies,omitempty"`

    TestInfo TestInfo `json:"test_info,omitempty"`

    Git GitContext `json:"git,omitempty"`
}
```

Relevant file:

```go
type RelevantFile struct {
    Path    string `json:"path"`
    Purpose string `json:"purpose,omitempty"`
    Summary string `json:"summary,omitempty"`
}
```

Tests:

```go
type TestInfo struct {
    Exists bool `json:"exists"`

    UnitTests        bool `json:"unit_tests"`
    IntegrationTests bool `json:"integration_tests"`
}
```

---

# 12. Jev Final Classification

После определения context и его возможного сбора выполняется основной Jev request.

Input:

```text
User request
+
optional ProjectContext
```

Jev должен определить:

1. complexity;
2. необходимость каждого supported stage.

---

# 13. Complexity

Для MVP использовать:

```text
SIMPLE
MEDIUM
COMPLEX
```

Рекомендуется использовать Jev `Choice`.

TypeSafe API поддерживает typed Choice-вопросы, где допустимые варианты ответа определяются заранее.

Пример conceptual request:

```text
State:

User request:
"Add Redis caching to ProductRepository"

Project:
Go service.
ProductRepository has Postgres implementation.
No cache currently exists.
Integration tests exist.

Question:

How complex is this task?

Options:

SIMPLE
MEDIUM
COMPLEX
```

Результат:

```text
MEDIUM
```

с вероятностями/confidence, предоставляемыми Jev.

---

# 14. Complexity Definition

Чтобы результаты были стабильнее, необходимо явно определить критерии.

## SIMPLE

Обычно:

```text
small/local change
few affected components
known implementation path
no significant architecture changes
low regression risk
```

Примеры:

```text
change validation condition
rename field
add simple unit test
small bug fix
```

---

## MEDIUM

Обычно:

```text
multiple files/components
requires understanding existing implementation
introduces dependency or behaviour
requires testing
moderate regression risk
```

Примеры:

```text
add repository caching
implement new API endpoint
change validation flow
add retry mechanism
```

---

## COMPLEX

Обычно:

```text
cross-component changes
architecture changes
uncertain implementation strategy
significant data flow changes
high regression risk
multiple systems/services
```

Примеры:

```text
introduce event-driven architecture
change authentication model
redesign persistence flow
modify behaviour across multiple services
```

---

# 15. Supported Stages

MVP поддерживает:

```text
analysis
research
planning
development
testing
review
```

---

# 16. Stage Selection

Для каждого stage Jev должен выполнить отдельный бинарный decision.

Использовать `Noul`.

TypeSafe описывает Noul как yes/no decision с вероятностным результатом; их workflow-рекомендация — разбивать автоматизацию на независимые узкие вопросы вместо одного большого generative prompt.

Пример:

```text
Does this task require analysis?
```

```text
Does this task require research?
```

```text
Does this task require planning?
```

```text
Does this task require development?
```

```text
Does this task require testing?
```

```text
Does this task require review?
```

---

# 17. Stage Output

Jev не должен возвращать:

```json
{
  "stages": [
    "analysis",
    "development",
    "testing"
  ]
}
```

Вместо этого Orchestrator должен получить probability для каждого stage.

Conceptually:

```json
{
  "analysis": 0.93,
  "research": 0.18,
  "planning": 0.81,
  "development": 0.99,
  "testing": 0.96,
  "review": 0.74
}
```

Это соответствует подходу Jev: typed decisions возвращаются с вероятностями/confidence, а software самостоятельно определяет thresholds и последующее действие.

---

# 18. Почему stages должны быть независимыми

Задача может требовать:

```text
analysis
```

но не:

```text
development
```

Например:

```text
Explain why this PostgreSQL query is slow.
```

Или:

```text
research
analysis
```

без development:

```text
Find alternatives to SHACL validation in Go.
```

Или:

```text
development
testing
```

без research.

Поэтому stage selection должен быть multi-label classification.

---

# 19. Jev Classification Result

Domain model:

```go
type TaskClassification struct {
    ContextDepth ContextDepth `json:"context_depth"`

    Complexity ComplexityDecision `json:"complexity"`

    Stages StageDecisions `json:"stages"`
}
```

Complexity:

```go
type ComplexityDecision struct {
    Value      Complexity         `json:"value"`
    Confidence float64            `json:"confidence"`
    Scores     map[string]float64 `json:"scores,omitempty"`
}
```

Stages:

```go
type StageDecisions struct {
    Analysis    ProbabilityDecision `json:"analysis"`
    Research    ProbabilityDecision `json:"research"`
    Planning    ProbabilityDecision `json:"planning"`
    Development ProbabilityDecision `json:"development"`
    Testing     ProbabilityDecision `json:"testing"`
    Review      ProbabilityDecision `json:"review"`
}
```

Generic probability:

```go
type ProbabilityDecision struct {
    Probability float64 `json:"probability"`
    Confidence  float64 `json:"confidence,omitempty"`
}
```

---

# 20. Policy Engine

Jev НЕ должен самостоятельно решать, какой probability означает включение stage.

Это ответственность Orchestrator.

Example configuration:

```yaml
workflow:
  thresholds:
    analysis: 0.60
    research: 0.70
    planning: 0.65
    development: 0.60
    testing: 0.60
    review: 0.70
```

Например:

```text
Jev:

analysis = 0.91
research = 0.22
planning = 0.78
development = 0.99
testing = 0.94
review = 0.73
```

Policy Engine:

```text
analysis      YES
research      NO
planning      YES
development   YES
testing       YES
review        YES
```

Workflow:

```text
analysis
   ↓
planning
   ↓
development
   ↓
testing
   ↓
review
```

---

# 21. Complexity-dependent Policies

MVP может поддерживать дополнительные deterministic rules.

Например:

```text
COMPLEX task
    → always analysis
    → always planning
    → always review
```

Configuration:

```yaml
workflow:
  complexity_policy:

    complex:
      force_stages:
        - analysis
        - planning
        - review
```

Другой пример:

```text
development == true
AND
complexity != SIMPLE

→ testing should normally be enabled
```

Важно:

**Jev предоставляет сигнал. Policy Engine принимает окончательное решение.**

---

# 22. Stage Ordering

Jev НЕ определяет порядок stages.

Для MVP ordering должен быть deterministic.

Например:

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

Workflow Builder выбирает только включенные stages.

Например:

```text
analysis = true
planning = true
development = true
testing = true
review = true
```

становится:

```text
analysis
   ↓
planning
   ↓
development
   ↓
testing
   ↓
review
```

---

# 23. Почему ordering не должен выполнять Jev

Например, система уже знает:

```text
development must happen before testing
testing must happen before review
planning must happen before development
```

Это deterministic knowledge.

Нет необходимости использовать AI для решения того, что можно выразить кодом.

TypeSafe также рекомендует вычислять deterministic части workflow в коде и использовать model decisions там, где требуется judgment.

---

# 24. Full MVP Flow

```text
USER REQUEST
     │
     ▼
┌─────────────────────┐
│ Request Processor   │
└──────────┬──────────┘
           │
           ▼
┌─────────────────────┐
│ Jev Decision Engine │
│                     │
│ context_depth?      │
└──────────┬──────────┘
           │
      ┌────┴─────┐
      │          │
    NONE      PROJECT
      │          │
      │          ▼
      │   ┌───────────────────┐
      │   │  Context Manager  │
      │   └─────────┬─────────┘
      │             │
      │             ▼
      │      Project Context
      │             │
      └──────┬──────┘
             │
             ▼
      ┌───────────────────┐
      │ Jev Classification│
      │                   │
      │ complexity        │
      │ stage decisions   │
      └─────────┬─────────┘
                │
                ▼
        ┌───────────────┐
        │ Policy Engine │
        └───────┬───────┘
                │
                ▼
       ┌────────────────┐
       │Workflow Builder│
       └───────┬────────┘
               │
               ▼
       ordered Stage[]
               │
               ▼
       ┌────────────────┐
       │Workflow Runner │
       └───────┬────────┘
               │
       ┌───────┼────────┬──────────┐
       ▼       ▼        ▼          ▼
    Claude   Codex    Gemini      Tools
```

---

# 25. Decision Provider Abstraction

Workflow code не должен зависеть непосредственно от TypeSafe API.

Introduce:

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

Jev implementation:

```go
type JevDecisionProvider struct {
    client JevClient
}
```

---

# 26. Jev Client

Transport abstraction:

```go
type JevClient interface {
    Decide(
        ctx context.Context,
        request JevRequest,
    ) (JevResponse, error)
}
```

Implementation communicates with TypeSafe API.

Current TypeSafe API exposes:

```text
POST /v1/systemone
```

and authenticated requests use a Bearer API key. It also exposes:

```text
GET /v1/models
```

for discovering available models.

Exact external request/response structs must remain inside:

```text
internal/decision/jev
```

They must not leak into workflow domain.

---

# 27. Recommended Go Structure

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
      collector.go
      docs.go
      files.go
      git.go
      tests.go

  classification/

    service.go
    types.go

  policy/

    engine.go
    config.go

  workflow/

    builder.go
    runner.go
    stage.go
```

---

# 28. Classification Service

Recommended orchestration:

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
            return TaskClassification{}, err
        }
    }

    result, err := s.decisions.Classify(
        ctx,
        ClassificationInput{
            Request: request,
            Project: project,
        },
    )

    if err != nil {
        return TaskClassification{}, err
    }

    return result, nil
}
```

---

# 29. Workflow Builder

```go
func (b *Builder) Build(
    classification TaskClassification,
) Workflow
```

Builder:

1. receives Jev decisions;
2. applies configured thresholds;
3. applies complexity policies;
4. orders stages;
5. returns executable workflow.

Example:

```go
workflow := []Stage{}

if policy.Enabled(StageAnalysis, classification) {
    workflow = append(workflow, StageAnalysis)
}

if policy.Enabled(StagePlanning, classification) {
    workflow = append(workflow, StagePlanning)
}

...
```

---

# 30. Jev Configuration

Example:

```yaml
decision:
  provider: jev

  jev:
    base_url: https://api.typesafe.ai

    api_key_env: JEV_API_KEY

    model: <configured-model>

    timeout: 5s

workflow:

  thresholds:

    analysis: 0.60
    research: 0.70
    planning: 0.65
    development: 0.60
    testing: 0.60
    review: 0.70
```

API key must never be stored directly in repository configuration.

---

# 31. Startup Validation

На старте приложения необходимо проверить:

```text
decision.provider == jev
```

Тогда:

1. API key существует;
2. base URL корректный;
3. configured model доступен;
4. Jev configuration валидна.

Если конфигурация некорректна:

```text
startup validation error
```

с понятным сообщением.

Например:

```text
jev decision provider: JEV_API_KEY is not configured
```

или:

```text
jev decision provider: configured model "X" is unavailable
```

---

# 32. Fallback

Jev — внешний сервис, поэтому его недоступность не должна обязательно ломать весь orchestrator.

Предусмотреть интерфейс fallback.

MVP configuration:

```yaml
decision:

  provider: jev

  fallback:
    enabled: true
    provider: llm
```

Flow:

```text
Jev
 │
 ├── success
 │      ↓
 │   decision
 │
 └── timeout/error
        ↓
   LLM Decision Provider
```

Fallback provider должен возвращать тот же domain model:

```go
TaskClassification
```

Workflow Builder не должен знать, кем выполнена classification.

---

# 33. Fallback Rules

Fallback должен срабатывать при:

```text
timeout
5xx
connection error
temporary unavailable
invalid provider response
```

Не делать бесконечные retries.

Для MVP:

```text
Jev attempt
    ↓ failure
one fallback attempt
    ↓
result/error
```

---

# 34. Observability

Каждая classification должна логировать:

```text
decision_provider
model
context_depth
project_context_used
complexity
complexity_confidence
stage_probabilities
final_stages
latency
fallback_used
```

Пример structured log:

```json
{
  "event": "task_classified",

  "decision_provider": "jev",

  "context_depth": "project",

  "complexity": "medium",

  "stages": {
    "analysis": 0.91,
    "research": 0.12,
    "planning": 0.81,
    "development": 0.99,
    "testing": 0.94,
    "review": 0.72
  },

  "workflow": [
    "analysis",
    "planning",
    "development",
    "testing",
    "review"
  ]
}
```

---

# 35. Metrics

Минимальные metrics:

```text
decision_requests_total

decision_errors_total

decision_fallback_total

decision_latency_seconds

project_context_requested_total

project_context_collection_seconds
```

Labels:

```text
provider
decision_type
result
```

Не использовать high-cardinality labels:

```text
request
filename
user_id
```

---

# 36. Decision Trace

Для debugging желательно хранить:

```text
request
    ↓
context decision
    ↓
project context summary
    ↓
Jev probabilities
    ↓
policy result
    ↓
generated workflow
```

Это позволит отвечать на вопрос:

> Почему orchestrator решил запустить review?

Например:

```text
review probability = 0.74

configured threshold = 0.70

result = enabled
```

---

# 37. Example 1 — General Question

Input:

```text
Explain how Go interfaces work.
```

Pre-check:

```text
context_depth = NONE
```

Classification:

```text
complexity = SIMPLE

analysis       0.94
research       0.08
planning       0.04
development    0.03
testing        0.01
review         0.01
```

Workflow:

```text
analysis
```

No project files are read.

---

# 38. Example 2 — Repository Change

Input:

```text
Add caching to ProductRepository.
```

Pre-check:

```text
context_depth = PROJECT
```

Context Manager:

```text
language = Go

ProductRepository interface exists

Postgres repository exists

Redis dependency does not exist

integration tests exist

affected components:
- product
- storage
- service
```

Final classification:

```text
complexity = MEDIUM

analysis       0.92
research       0.31
planning       0.83
development    0.99
testing        0.96
review         0.82
```

Policy result:

```text
analysis
planning
development
testing
review
```

---

# 39. Example 3 — Investigation Only

Input:

```text
Investigate why this PostgreSQL query is slow.
```

If repository-specific query/code is referenced:

```text
context_depth = PROJECT
```

Classification may produce:

```text
complexity = MEDIUM

analysis       0.98
research       0.72
planning       0.18
development    0.20
testing        0.11
review         0.08
```

Workflow:

```text
research
   ↓
analysis
```

There is no requirement that every workflow results in code modification.

---

# 40. Context Collection Failure

Если:

```text
context_depth = PROJECT
```

но Context Manager не может собрать project context, classification не должна автоматически падать.

Передать:

```text
project_context_available = false
```

и выполнить classification на основании request.

Log:

```text
context_collection_failed
```

Result должен отмечаться:

```text
context_limited = true
```

Это позволит workflow продолжить работу.

---

# 41. Security

Context Collector не должен автоматически отправлять:

```text
.env
credentials
private keys
tokens
secrets
password files
```

Необходимо поддерживать deny list как минимум для:

```text
.env
.env.*
*.pem
*.key
credentials*
secrets*
```

Также учитывать:

```text
.gitignore
```

при обходе repository.

---

# 42. MVP Boundaries

В первой версии Jev НЕ используется для:

```text
model selection

tool selection

retry decision

stage ordering

implementation validation

review result evaluation

workflow loop decisions
```

То есть MVP scope строго:

```text
1. context depth
2. complexity
3. required stages
```

Это позволяет сначала проверить качество Jev именно на routing/classification.

---

# 43. Future Extensions

После MVP можно добавить:

```text
model selection
```

Например:

```text
simple development → Claude Sonnet
complex development → Claude Opus
research → Gemini
```

---

Можно добавить:

```text
should_retry
```

```text
implementation_complete
```

```text
requires_human_review
```

```text
review_passed
```

```text
return_to_development
```

Но эти decisions не входят в текущую задачу.

---

# 44. Unit Tests

Обязательно протестировать:

### Context decision

```text
NONE
PROJECT
```

### Complexity mapping

```text
SIMPLE
MEDIUM
COMPLEX
```

### Stage threshold

Например:

```text
probability = 0.69
threshold = 0.70

→ disabled
```

```text
probability = 0.70
threshold = 0.70

→ enabled
```

### Stage ordering

Input:

```text
review
development
analysis
testing
```

Output:

```text
analysis
development
testing
review
```

### Jev error

```text
Jev unavailable
→ fallback provider invoked
```

### Context failure

```text
context requested
context manager fails
→ classification continues with limited context
```

---

# 45. Integration Tests

Integration test with mocked Jev endpoint:

```text
request
   ↓
pre-check response = PROJECT
   ↓
context collector
   ↓
classification response
   ↓
policy
   ↓
workflow
```

Expected:

```text
correct workflow stages
correct order
```

---

# 46. Acceptance Criteria

Implementation считается завершённой, когда выполнены следующие требования.

### AC1

Orchestrator может использовать Jev как:

```text
DecisionProvider
```

без прямой зависимости workflow package от Jev implementation.

### AC2

Для каждого user request сначала определяется:

```text
context_depth
```

### AC3

Поддерживаются:

```text
NONE
PROJECT
```

### AC4

При:

```text
PROJECT
```

запускается Context Manager.

### AC5

Context Manager учитывает:

```text
project metadata
project instructions
relevant files
tests
dependencies
```

### AC6

После context collection Jev определяет:

```text
SIMPLE
MEDIUM
COMPLEX
```

### AC7

Jev независимо оценивает необходимость:

```text
analysis
research
planning
development
testing
review
```

### AC8

Для каждого stage сохраняется probability/confidence.

### AC9

Policy Engine конвертирует probabilities в включенные stages согласно configuration thresholds.

### AC10

Jev не определяет порядок stages.

### AC11

Workflow Builder формирует deterministic ordered workflow.

### AC12

Jev failure поддерживает fallback provider.

### AC13

Jev transport models не используются вне:

```text
internal/decision/jev
```

### AC14

Добавлены structured logs и metrics.

### AC15

Project secrets не отправляются в Jev.

### AC16

Есть unit и integration tests для нового flow.

---

# 47. Definition of Done

* Jev client реализован.
* Bearer authentication реализована.
* Provider abstraction реализована.
* Jev provider реализован.
* `ContextDepth` decision реализован.
* Context Manager реализован.
* `ProjectContext` реализован.
* Complexity classification реализована.
* Stage decisions реализованы.
* Probability thresholds configurable.
* Policy Engine реализован.
* Workflow Builder интегрирован с classification.
* Deterministic ordering реализован.
* Fallback provider поддерживается.
* Startup validation добавлена.
* Logging добавлен.
* Metrics добавлены.
* Unit tests проходят.
* Integration tests проходят.
* README/configuration documentation обновлена.

---

# 48. Итоговая архитектура MVP

```text
                             USER
                              │
                              ▼
                    ┌─────────────────┐
                    │  ORCHESTRATOR   │
                    └────────┬────────┘
                             │
                             ▼
                      ┌────────────┐
                      │    JEV     │
                      │ PRE-CHECK  │
                      └──────┬─────┘
                             │
                       context_depth
                             │
                 ┌───────────┴───────────┐
                 │                       │
               NONE                   PROJECT
                 │                       │
                 │                       ▼
                 │              ┌────────────────┐
                 │              │Context Manager │
                 │              └───────┬────────┘
                 │                      │
                 │               project context
                 │                      │
                 └──────────┬───────────┘
                            ▼
                       ┌───────────┐
                       │    JEV    │
                       │ CLASSIFY  │
                       └─────┬─────┘
                             │
              ┌──────────────┴─────────────┐
              │                            │
         complexity                  stage probabilities
              │                            │
              └──────────────┬─────────────┘
                             ▼
                      ┌─────────────┐
                      │Policy Engine│
                      └──────┬──────┘
                             ▼
                    ┌────────────────┐
                    │Workflow Builder│
                    └───────┬────────┘
                            ▼
               ┌────────────────────────┐
               │     Workflow Runner    │
               └────────────┬───────────┘
                            │
          ┌─────────────────┼─────────────────┐
          ▼                 ▼                 ▼
       Claude             Codex             Gemini
          │                 │                 │
          └─────────────────┼─────────────────┘
                            ▼
                          Tools
```

Главная граница ответственности MVP:

> **Jev determines how much project context is needed, evaluates task complexity and estimates which workflow stages are required. The Orchestrator collects the requested context, applies deterministic policies, constructs the workflow and executes it.**

Это позволяет использовать Jev там, где его архитектура наиболее полезна — для быстрых typed probabilistic decisions — не превращая его в универсальный agent и не отдавая ему контроль над workflow.
