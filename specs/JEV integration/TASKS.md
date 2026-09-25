# TASKS.md — Jev Integration: план реализации

Источники: [`SPEC.md`](SPEC.md) (нормативный), [`MVP.md`](MVP.md) (развёрнутое описание),
документация Jev — <https://docs.typesafe.ai/llms.txt> (API, модели, ограничения).
Где SPEC и MVP расходятся, побеждает SPEC. Единственное расхождение: MVP.md §28 прерывает
classification при ошибке Context Manager, а SPEC §14 и MVP.md §40 требуют продолжить
работу с `context_limited = true`. Принимаем второе.

Статус: ред. 4. Работа разбита на две итерации. Решения по вопросам собраны в разделе 7.
Оставшиеся вопросы не блокируют старт.

**Отступления от SPEC, принятые осознанно:**
- **Fallback (SPEC §17, AC12).** При сбое decision provider автоматического перехода на
  `LLMDecisionProvider` нет: пользователь сам выбирает complexity и stages (решение В9).
  LLM при этом можно выбрать основным decision provider в config (`decision.provider`,
  T22). Это статический выбор, а не fallback.
- **Перевод запроса.** Если запрос не на английском, перед отправкой в decision provider
  его переводит LLM (решения В7, В13). В SPEC этого шага нет.
- **Complexity.** В одном запросе задаются два вопроса: `choice` и `score`. Решение
  принимается по основному, оба пишутся в лог для сравнения (решение В1).

---

## 1. Целевой flow

```
request
  │
  ▼
Language check (детерминированно)
  ├── английский ──────────────────────────────┐
  └── русский → перевод (LLM CLI) → request_en │   сбой перевода → оригинал + warning
                                               ▼
Decision pre-check: Jev (1 HTTP-запрос) или LLM (1 вызов CLI), 1 вопрос
  «нужен ли контекст проекта, чтобы оценить задачу?» → P(none), P(project)
  │  orchestrator выбирает depth по порогу
  ├── NONE ────────────────────────────┐
  └── PROJECT → Context Manager        │
                 → ProjectContext      │
                         ▼             ▼
Decision classification: Jev (1 HTTP-запрос, 8 вопросов параллельно) или LLM (1 вызов CLI)
  complexity (choice) + complexity_score (score) → распределения по simple/medium/complex
  6 stages: yes/no на каждый                      → вероятность каждого stage
  │
  ├── сбой провайдера на любом шаге ──► пользователь выбирает complexity и stages (терминал);
  │                                     в --mode auto или без TTY — ошибка
  ▼
Policy Engine (детерминированно, конфигурируемо)
  1. complexity ← распределение основного вопроса + пороги (теневой вопрос → только в лог)
  2. stages     ← вероятность ≥ threshold
  3. + forced stages для выбранной complexity
  │
  ▼
Workflow Builder: research → analysis → planning → development → testing → review
  │
  ▼
существующий engine (state machine, лимиты, тесты по exit code, review по git diff)
```

Decision provider задаётся в config (`decision.provider`, T22): `jev` или CLI-провайдер
(`claude`, `codex`, `gemini`). Flow от этого не меняется: те же два раунда, те же вопросы,
те же доменные типы, policy и trace. Меняется только, кто отвечает на вопросы.

Decision provider не принимает решений: он возвращает распределения, а выбор делает
orchestrator — для depth, complexity и stages. Переведённый запрос нужен только для
decision provider и для поиска relevant files. Stages получают оригинальный текст.

---

## 2. Jev API — что известно из документации

Источники: [API reference](https://docs.typesafe.ai/api.md), [Models](https://docs.typesafe.ai/models.md),
[Jev 1.13 jaggedness](https://docs.typesafe.ai/model-jaggedness/jev-1.13.md).

- **Endpoint.** `POST https://api.typesafe.ai/v1/systemone`, заголовок `Authorization: Bearer <key>`.
  Тело — `{state, model, questions: map<id, Question>}`, где `state` — строка, объект или
  массив. Все вопросы оцениваются **параллельно и независимо** против одного state, поэтому
  classification — один запрос. State тарифицируется один раз, так что лишний вопрос
  (теневой `score`) почти ничего не стоит.
- **Типы вопросов.** Общие поля — `type`, `instructions`, `criteria`.
  - `choice`: criteria — map «вариант → описание» (до 255 вариантов). Ответ —
    `{choice, probabilities, confidence}`.
  - `score`: criteria — упорядоченный массив уровней (2–10). Ответ — `{score, legend,
    probabilities: {"0": p, …}, confidence}`.
  - `noul`: criteria `{true, false}` необязательны. Ответ — `{noul: 0..1}`, **без confidence**.
- **Ответ.** `{model: "jev-1.13.0", answers: {id: Answer}, usage}`. В `model` приходит
  конкретная версия, её нужно логировать.
- **Модели.** `GET /v1/models` → `{models: [{name, description, release_date}]}`. Список
  содержит только aliases. Версионные ID API принимает, даже если их нет в списке.
- **Ошибки.** `401` — ключ; `422` — невалидное тело (наш баг); `429` — rate limit;
  `529` — сервис перегружен. SDK повторяет `408`/`429`/`5xx` с backoff и учитывает `Retry-After`.
- **Лимиты.** 1200 запросов в минуту. 64k токенов на запрос, из них **32k на state вместе
  с самым длинным вопросом**. Латентность 70–500 мс.
- **Качество.**
  - Нерелевантный контент в state снижает точность, поэтому ProjectContext должен быть компактным.
  - Модель понимает вопрос буквально: у каждого варианта нужны точные критерии.
  - Основной язык — английский. Вопросы пишем на английском, запрос переводим (В7).
  - Alias `jev-latest` может смениться без предупреждения. Пороги настраиваются под версию,
    поэтому в config стоит версия `jev-1.13.0`.
  - Документация прямо предупреждает, что `choice` и `score` на один и тот же вопрос
    не обязаны давать согласованные числа, и пороги от одного к другому не переносятся. Поэтому
    теневой вопрос считается по тем же правилам, но только логируется: решение принимается
    по одному источнику.

### Запрос pre-check

```json
{
  "model": "jev-1.13.0",
  "state": { "request": "<запрос на английском>" },
  "questions": {
    "context_depth": {
      "type": "choice",
      "instructions": "To estimate the complexity and required workflow stages of `request`, is it necessary to inspect the current code repository?",
      "criteria": {
        "none": "The request can be classified from its text alone: general knowledge, concepts, comparisons; it does not refer to this repository's code, files or behaviour.",
        "project": "The request refers to code, components, files, bugs, tests or behaviour of the current repository, so its complexity depends on that code."
      }
    }
  }
}
```

### Запрос classification

```json
{
  "model": "jev-1.13.0",
  "state": {
    "request": "<запрос на английском>",
    "project_context_available": true,
    "project": { "language": "go", "rules": ["…"], "relevant_files": ["…"], "tests": {"…": "…"} }
  },
  "questions": {
    "complexity": {
      "type": "choice",
      "instructions": "How complex is it to fulfil `request` in this project?",
      "criteria": {
        "simple":  "Small local change, few components, known implementation path, no architecture change, low regression risk.",
        "medium":  "Several files/components, requires understanding existing code, introduces a dependency or behaviour, moderate regression risk.",
        "complex": "Cross-component or architectural change, uncertain strategy, significant data-flow change, high regression risk."
      }
    },
    "complexity_score": {
      "type": "score",
      "instructions": "Rate how complex it is to fulfil `request` in this project.",
      "criteria": [
        "Simple: small local change, few components, known implementation path, no architecture change, low regression risk.",
        "Medium: several files/components, requires understanding existing code, introduces a dependency or behaviour, moderate regression risk.",
        "Complex: cross-component or architectural change, uncertain strategy, significant data-flow change, high regression risk."
      ]
    },
    "stage_research":    { "type": "noul", "instructions": "Does fulfilling `request` require researching external knowledge (approaches, libraries, alternatives) before working?", "criteria": { "true": "…", "false": "…" } },
    "stage_analysis":    { "type": "noul", "instructions": "Does fulfilling `request` require analysing existing code or the problem before acting?" },
    "stage_planning":    { "type": "noul", "instructions": "…" },
    "stage_development": { "type": "noul", "instructions": "Does fulfilling `request` require changing code in the repository?" },
    "stage_testing":     { "type": "noul", "instructions": "…" },
    "stage_review":      { "type": "noul", "instructions": "…" }
  }
}
```

Критерии для `choice` и `score` одинаковые, взяты из MVP.md §14. Иначе сравнение
бессмысленно. Уровни `score` (индексы `"0"`, `"1"`, `"2"`) маппятся на simple/medium/complex
по порядку. Тексты вопросов — константы в коде. Любая их правка — повод перекалибровать пороги.

---

## 3. Что меняется в текущем коде

| сейчас | после |
|---|---|
| `internal/classifier`: 1 вызов Claude CLI → `{type, complexity, requires_*}` | перевод (при необходимости) + `DecisionProvider` (Jev или LLM, по `decision.provider`): pre-check и classification |
| `TaskType` + таблица в `workflow.Build` (корневой MVP.md §9) | вероятности → Policy Engine → фиксированный порядок stages |
| `Complexity` `low/medium/high` | `simple/medium/complex`, выбирается policy по распределению |
| actions `analyze, plan, implement, test, review, answer` | stages `research, analysis, planning, development, testing, review` → actions (В4) |
| `Next()`: analyze/plan → **всегда** implement | в workflow может не быть development, переходы обобщаются (В5) |
| сбой classifier → ошибка run | сбой Jev → ручной выбор пользователем (В9) |
| только локальные CLI (MVP.md §30: без HTTP API) | внешний HTTP-сервис Jev → §30 нужно поправить |

Не меняется: engine как state machine, append-only `run.Executions`, лимиты
`max_steps`/`max_fix_iterations`, тесты — только по exit code, review — только по `git diff`,
статический routing provider/model по action, fail-fast до первого AI-вызова.

Архитектурные решения:
1. Доменные типы живут в `internal/domain`: их использует `internal/workflow`, а он
   может импортировать только `domain`.
2. Context Manager — пакет `internal/projectctx`. Имя `internal/context` конфликтует
   со stdlib `context`.
3. `internal/policy` импортирует только `domain` и получает `domain.DecisionPolicy`
   (по аналогии с `LimitsView`). Из config его собирает `app`.
4. Transport-типы Jev в `internal/decision/jev` неэкспортируемые. Изоляцию (AC13)
   проверяет компилятор.
5. Ручной выбор при сбое Jev — отдельная ветка Classification Service, а не
   `DecisionProvider`. Пользователь не выдаёт вероятности, и выдумывать их (1.0/0.0) —
   значит портить данные для сравнения. В trace такой выбор записывается как
   `source: user`.
6. Перевод работает через существующий `provider.Client`. Специфика провайдеров
   остаётся в `buildArgs`.
7. Decision provider выбирается один раз, в `app`, по `decision.provider` (T22).
   Classification Service, policy и trace зависят только от интерфейса
   `DecisionProvider`. LLM-провайдер работает через `provider.Client`, как переводчик.
   Тексты вопросов общие для обоих провайдеров, чтобы их ответы были сравнимы.

---

## 4. Итерации

**Итерация 1 — вертикальный срез.** Цепочка перевод → decision provider → Policy →
Workflow → исполнение работает end-to-end: с Jev (реальным или mock) и с LLM в роли decision
provider, в integration-тестах — с mock Jev и fake CLI. При сбое провайдера пользователь
выбирает вручную. Choice и score логируются для сравнения.

**Итерация 2 — полное соответствие SPEC и итоги эксперимента.** Проверка модели при старте,
relevant files по символам, structured logs и metrics, редактирование stages в
интерактивном режиме, выбор одного вопроса для complexity, документация.

| # | задача | итер. | размер | зависит от | вопрос |
|---|---|---|---|---|---|
| T01 | Доменные типы | 1 | S | — | В4 |
| T02 | Config v2 и offline-валидация | 1 | M | T01 | — |
| T03 | Policy Engine: depth, complexity (основная + теневая), stages | 1 | M | T01 | В6 |
| T04 | Workflow Builder на stages | 1 | S | T01, T03 | — |
| T05 | Обобщённые переходы engine | 1 | M | T04 | В5 |
| T06 | Jev HTTP client | 1 | M | — | — |
| T07 | `DecisionProvider` и Jev provider (choice + score) | 1 | M | T01, T06 | В3 |
| T08 | Context Manager (базовый) | 1 | M | T01 | В8 |
| T09 | Classification Service | 1 | S | T03, T07, T08, T15, T16 | — |
| T10 | Action `research`, удаление `answer` | 1 | M | T04 | В4 |
| T11 | Decision trace и вывод в консоль | 1 | S–M | T09 | — |
| T12 | Wiring в `app`, удаление старого classifier | 1 | M | T02–T11, T15, T16, T22 | — |
| T13 | Integration tests с mock Jev | 1 | M | T12 | — |
| T15 | Определение языка и перевод запроса | 1 | M | T02 | — |
| T16 | Ручной выбор при сбое Jev | 1 | S–M | T03, T04 | — |
| T22 | Выбор decision provider в config: Jev или LLM | 1 | M | T02, T07 | — |
| T14 | Startup-проверка модели (`GET /v1/models`) | 2 | S | T06, T12 | — |
| T17 | Relevant files по символам (Go) | 2 | M | T08 | — |
| T18 | Structured logs и metrics, отчёт choice vs score | 2 | M | T11 | В10 |
| T19 | Интерактивное редактирование stages | 2 | M | T04, T16 | — |
| T20 | Документация | 2 | S–M | всё | — |
| T21 | Итог эксперимента: оставить choice или score | 2 | S | T18 + данные | — |

Номера задач сохранены с прошлой редакции. В итерацию 1 перенесены T15 и T16 (раньше это
были LLM provider и fallback). Добавлена T21. Ред. 4: добавлена T22 — LLM как основной
decision provider, выбранный в config (не fallback).

```
T01 ─┬─ T02 ── T15 ─────────────────────┐
     ├─ T03 ─┬─ T04 ─┬─ T05              │
     │       │       ├─ T10              │
     │       │       └─ T16 ─────────────┤
     │       └───────────────────────────┤
     ├─ T08 ─────────────────────────────┼─ T09 ── T11 ── T12 ── T13
     └─ T07 ◄── T06 ─────────────────────┘                 │
        T07 + T02 ── T22 ──────────────────────────────────┘
итерация 2: T14, T17, T18 → T21, T19, T20
```

Порядок работы в итерации 1:
1. Без сети, только unit-тесты: T01, T02, T03, T04, T05, T06.
2. T07, T08, T15, T16.
3. T09, T10, T11, T22.
4. Сборка: T12, T13.

Правила для каждой задачи:
- `go build ./... && go vet ./... && go test ./...` проходят, `gofmt -l .` пуст;
- `internal/workflow` и `internal/policy` импортируют только `domain`;
- transport-типы Jev не выходят за пределы `internal/decision/jev`;
- API key не попадает ни в config, ни в логи, ни в артефакты, ни в тексты ошибок.

---

## 5. Задачи — итерация 1

### T01 — Доменные типы

**Файлы:** `internal/domain/{stage,decision,projectcontext}.go` и тесты к ним.

1. `Stage`: 6 констант, `StageOrder` (research, analysis, planning, development, testing,
   review), `ParseStage`, `Stage.Action()` (маппинг — В4). Порядок и маппинг задаются
   таблицами-значениями, а не `switch`.
2. `ContextDepth`: `none`, `project`. Это строковый тип, не bool (MVP.md §5).
3. `Complexity`: `simple`, `medium`, `complex`, плюс `ComplexityOrder`.
4. Сырые решения провайдера:
   - `ContextDepthDecision{Probabilities map[ContextDepth]float64, Confidence}`;
   - `ComplexityDecision{Probabilities map[Complexity]float64, Confidence, Score *float64}`
     — `Score` заполнен только у score-вопроса;
   - `ProbabilityDecision{Probability}` (у Noul нет confidence);
   - `StageDecisions` — 6 полей, `Get(Stage)`, `All()` в порядке `StageOrder`.
5. `ComplexitySource`: `choice`, `score`.
6. `TaskClassification`:
   ```
   {ContextDepth, ContextLimited,
    Complexity map[ComplexitySource]ComplexityDecision,
    Stages StageDecisions, Provider, Model, Latency}
   ```
   Внутри только данные от провайдера, выбор делает T03.
7. `ClassificationInput{Request, Project *ProjectContext}`. `Request` — уже переведённый текст.
8. `ProjectContext`, `RelevantFile`, `TestInfo`, `GitContext` — с json-тегами из MVP.md §11.
   Плюс `Truncated bool`.
9. `Validate()`:
   - вероятности в [0,1] и не NaN;
   - в распределениях complexity и depth ровно все варианты, сумма ≈ 1 (±0.01);
   - `Score` в диапазоне [0, 2].

**Acceptance:** table-тесты маппинга и порядка; `Validate` отвергает значения 1.01, −0.01,
NaN, лишний и отсутствующий вариант.

**Сделано.** Отличия от плана:
- `ClassificationInput` дополнительно несёт `ContextDepth` и `ContextLimited`. Это факты
  orchestrator'а, провайдер копирует их в результат без изменений, поэтому
  `TaskClassification.Validate()` может требовать валидный depth.
- `Latency` вынесен из `TaskClassification` в тайминги Classification Service (T09) и
  trace (T11): время измеряет сервис, не провайдер.
- Старый `Complexity` (low/medium/high) переименован в `LegacyComplexity` и удаляется в
  T12. Каждая задача при этом остаётся зелёной.
- Константа `ActionResearch` добавлена уже здесь, её executor появится в T10.
- В `ProjectContext` добавлено поле `Module`, в `RelevantFile.Purpose` — фиксированный
  словарь ролей.

### T02 — Config v2 и offline-валидация

**Файлы:** `internal/config/{config,validator,dependencies}.go` и тесты.

```yaml
version: 2

decision:
  jev:
    base_url: https://api.typesafe.ai
    api_key_env: JEV_API_KEY
    model: jev-1.13.0          # версия, не alias
    timeout: 5s                # на один HTTP-запрос
    max_retries: 1             # только 408/429/5xx/529, учитывает Retry-After, в пределах timeout
    complexity:
      primary: choice          # choice | score — по нему решает policy
      shadow: true             # второй вопрос в том же запросе, только в лог (В1)
  on_failure: ask              # ask | fail — сбой Jev: ручной выбор (В9) или ошибка

translation:                   # В7
  provider: claude
  model: haiku
  timeout: 60s

policy:
  context_depth:
    project_threshold: 0.5     # P(project) >= порога → PROJECT
  complexity:                  # В2: проверка от самой сложной
    complex: 0.40              # P(complex) >= 0.40              → complex
    medium: 0.50               # P(medium) + P(complex) >= 0.50  → medium, иначе simple
  stages:
    thresholds: {research: 0.70, analysis: 0.60, planning: 0.65, development: 0.60, testing: 0.60, review: 0.70}
    force:
      complex: [analysis, planning, review]

context:
  max_files: 20
  max_bytes: 24576             # ≈ 6k токенов; у Jev лимит 32k на state
  extra_deny: []

actions:
  research: { provider: claude, model: sonnet }
  # analyze / plan / implement / review — как сейчас; answer удаляется (В4)
```

Пороги вынесены в `policy:`, а не в `workflow:`, как в SPEC. Секции `workflow` в config нет,
а `policy` соответствует компоненту SPEC. Если нужно имя из SPEC, секцию переименуем.

`Validate` собирает все проблемы сразу:
- **Версия.** `version == 2`. Для v1 выдаётся понятная ошибка с перечнем изменений.
- **`decision.jev`.** `base_url` — корректный `http(s)://host`. `model` не пуст.
  `timeout > 0`. `max_retries` в диапазоне 0–3. `complexity.primary` — из enum.
- **API key.** `api_key_env` не пуст **и переменная задана**, иначе
  `jev decision provider: JEV_API_KEY is not configured`. Ключ `api_key` в YAML — ошибка.
- **`on_failure`.** Только `ask` или `fail`.
- **`translation`.** `provider` поддерживается, `model` не пуст, `timeout > 0`.
- **Пороги.** Все 6 stage thresholds заданы (отсутствие ≠ `0`) и лежат в [0,1],
  неизвестные ключи — ошибка. То же для `project_threshold` и порогов complexity.
- **`force`.** Только известные complexity и stages.
- **`ValidateDependencies`.** Добавляются `translation.provider` и `actions.research`,
  убираются `answer` и `classifier`. Executable переводчика проверяется всегда, даже
  для английского запроса: fail-fast не должен зависеть от текста задачи.

`config.DecisionPolicy()` возвращает `domain.DecisionPolicy` для T03.

**Acceptance:** на каждое правило — тест; fail-fast по-прежнему срабатывает до
создания любого провайдера.

**Сделано.** Отличия от плана:
- **Version остаётся 1, старые поля не убраны.** `cfg.Classifier` и `cfg.Actions.Answer`
  до сих пор используются реальным flow (`app.go`, `internal/classifier`,
  `internal/action/answer.go`). Если сейчас потребовать `version: 2` и убрать эти поля,
  сломается всё, что ещё работает по старому flow — а его заменяют только T03–T11. Поэтому
  версия и удаление старых полей переносятся в T12 целиком, одним отрезанием: там же
  старый flow заменяется новым, так что config, требующий новых секций, не окажется
  раньше, чем появится код, который их использует.
- **Валидация нового surface — в отдельных функциях**, не в `Validate`/`ValidateDependencies`.
  Новый файл `internal/config/decision.go` содержит `ValidateDecisionConfig` (структурные
  проверки) и `ValidateDecisionConfigDependencies` (переменная окружения ключа + executable
  переводчика/research) — их вызов в `app.go` добавит T12. До этого момента ни один
  существующий config (включая `orchestrator.example.yaml` и все integration-тесты)
  не обязан заполнять `decision`/`translation`/`policy`/`context`.
- **`actions.research` не участвует в `actionProviderModels()`/`actionTimeouts()`**
  (используются старым `Validate`/`ValidateDependencies`) — только в новых функциях. Иначе
  каждый существующий config должен был бы завести `actions.research`, хотя это действие
  пока ничего не выполняет (executor появится в T10).
- **`domain.DecisionPolicy`** добавлен в `internal/domain/policy.go` — в T01 его не было,
  но T02 явно требует `config.DecisionPolicy()`, возвращающий этот тип. Структура зеркалит
  `domain.LimitsView`: чистые данные, `internal/policy` (T03) не будет знать про config.
- **`*float64` для `project_threshold`, `complexity.complex`, `complexity.medium`** — чтобы
  отличить «не задано» от явного `0`. Для порогов stages это и так есть естественно (через
  наличие ключа в map), для этих трёх скалярных полей понадобился отдельный приём.
- **Обнаружение литерального `api_key` в YAML.** `yaml.Unmarshal` по умолчанию не считает
  неизвестные поля ошибкой, поэтому `api_key: …` в конфиге просто тихо игнорировался бы.
  `JevConfig.UnmarshalYAML` теперь отдельно ловит это поле (в неэкспортируемый флаг,
  значение никуда не попадает) и `ValidateDecisionConfig` на нём падает. Глобально включать
  строгий режим декодирования (`KnownFields(true)`) не стал — это отдельное, более широкое
  изменение поведения для всего config, не входящее в T02.
- **`ValidateDependencies` отрефакторен**: общий цикл проверки executable вынесен в
  `checkExecutablesPresent`, чтобы `ValidateDecisionConfigDependencies` мог использовать ту
  же логику и те же формулировки ошибок. Поведение `ValidateDependencies` не изменилось —
  весь существующий набор тестов зелёный без правок.

### T03 — Policy Engine

**Файлы:** `internal/policy/engine.go` и тесты; `domain.DecisionPolicy`, `domain.Decision`.

Чистые функции:
1. `SelectContextDepth(d, p) ContextDepth`: PROJECT, если `P(project) >= project_threshold`.
2. `SelectComplexity(d ComplexityDecision, p) Complexity` — правило В2: сначала
   `P(complex) ≥ t.complex`, затем `P(medium)+P(complex) ≥ t.medium`, иначе simple.
   Применяется **к обоим** распределениям. Основное даёт решение, теневое — значение
   `shadow` для лога. Флаг `agree = primary == shadow`.
3. `SelectStages(c, complexity, p) StageSelection`:
   - `probability >= threshold` включает stage (граница включительно);
   - затем добавляется `force[complexity]`;
   - если ничего не включено — В6.
4. `ApplyForced(complexity, userStages, p)` — те же forced-правила поверх ручного выбора (T16).
5. `domain.Decision` — полный итог для trace:
   - `source`: `jev` или `user`;
   - depth: выбор, вероятность, порог;
   - complexity: primary и shadow — распределение, confidence, score, выбор, `agree`;
   - stages: `{probability, threshold, forced, enabled, reason}`.

   Отсюда строка вида `review: p=0.74 ≥ 0.70 → enabled`.

**Acceptance:**
- порог 0.70: при p=0.69 stage выключен, при p=0.70 включён;
- forced stage с p=0.05 включён с reason `forced: complexity=complex`;
- примеры из MVP.md §37–39 дают ожидаемые workflow;
- `{simple:.35, medium:.30, complex:.35}` даёт complex (консервативно);
- при расхождении choice и score — `agree=false`, решение по primary;
- повторный вызов даёт тот же результат.

**Сделано.** Отличия от плана:
- **Формы `domain.Decision` и его частей план не специфицировал** — только называл их.
  Добавлены в `internal/domain/policy.go`: `DecisionSource` (`jev`/`user`),
  `ContextDepthOutcome`, `ComplexitySourceOutcome`, `ComplexityOutcome` (с `Sources`
  по каждому заданному вопросу и `Agree`), `StageOutcome`, `StageSelection`
  (+ `EnabledStages()` — им пользуется T04). У `StageOutcome.Probability/Threshold` тип
  `*float64`: `nil` у stages, выбранных вручную через `ApplyForced` (T16/T19) — там нет
  вероятности от провайдера, которую можно было бы показать.
- **`Evaluate` возвращает `(domain.Decision, error)`**, хотя в псевдокоде плана у всех
  функций по одному возвращаемому значению. Ошибка — только на один случай: `primary`
  (`decision.jev.complexity.primary` из config) не оказался среди вопросов, которые
  реально были заданы. Это не пограничный случай вероятностей, а баг связки config/T07,
  и по правилам проекта такое должно падать явно, а не тихо возвращать нулевую сложность.
- **Пример `{simple:.35, medium:.30, complex:.35} → complex`** по правилу В2 с порогами
  по умолчанию (`complex: 0.40`) даёт **medium** (0.30+0.35=0.65 ≥ 0.50), не complex — я
  сам неточно сформулировал этот пример при написании плана. Тест использует более низкий
  порог `complex: 0.35`, чтобы честно показать заложенную идею: complex, набравший
  вероятность не ниже своего (более осторожного) порога, выигрывает сам по себе, даже
  деля первое место с simple, — оператор может опустить порог `complex` ниже порога
  `medium` умышленно, а не только полагаться на сумму. Правило В2 не менялось.
- **`ApplyForced` не применяет откат В6** («ничего не выбрано → analysis»): по T16, UI не
  даёт отправить пустой выбор, так что пустой результат здесь означал бы баг вызывающего
  кода, а не реальный случай «ничего не подошло».

### T04 — Workflow Builder на stages

**Файлы:** `internal/workflow/builder.go`, `internal/domain/workflow.go`.

`Build(sel StageSelection) (Workflow, error)` фильтрует `StageOrder` по enabled и маппит
stage в action. У `Step` появляется поле `Stage`. Старый `Build(Classification)` и
`TaskType` удаляются.

**Acceptance:** вход `review, development, analysis, testing` → выход
`analysis, development, testing, review`; одна и та же selection → один и тот же workflow.

**Сделано.** Отличия от плана:
- **Старый `Build(Classification)` не удалён, а переименован в `BuildLegacy`.** Он всё ещё
  используется реальным flow (`app.go`, `interaction/editplan.go` и их тесты) — как и в
  T02/T03, удаление откладывается до T12, где старый flow заменяется новым целиком. Три
  вызова обновлены механически, поведение не изменилось (полный набор старых тестов
  зелёный без правок логики, только переименование). `TaskType` пока тоже не удалён — он
  используется только `domain.Classification`/`BuildLegacy`, но сам по себе безвреден,
  трогать его отдельно от остального старого flow смысла нет.
- **`Build` обходит `domain.StageOrder()` сам**, а не доверяет порядку `sel.Stages` (хотя
  на практике `policy.SelectStages`/`ApplyForced` уже строят его по порядку) — так
  acceptance-пример из плана («вход review, development, analysis, testing») проверяется
  буквально: порядок входа не имеет значения в принципе, а не только у настоящих вызовов
  через `policy`.
- **Добавлены два защитных случая ошибки**, которых не было в плане явно: пустая/полностью
  выключенная selection и stage без маппинга в action (`domain.StageSelection` — публичная
  структура, её можно было бы собрать вручную некорректно).
- **`Step.Stage` — с `json:"stage,omitempty"`**, чтобы JSON старых run (собранных через
  `BuildLegacy`) не менялся побайтово.

### T05 — Обобщённые переходы engine

**Файлы:** `internal/workflow/{transitions,engine}.go` и тесты.

- Forward-переход — следующий step workflow. Хардкода analyze/plan → implement больше нет.
- Fail в test/review → implement, только если implement есть в workflow. Иначе — В5.
- `isBackwardEntryToImplement`, лимиты и append-only история не меняются.

**Acceptance:**
- существующие последовательности (типа bugfix, feature, refactor) проходят;
- `research → analysis → done`;
- workflow из одного testing с падающими тестами;
- workflow из одного review с fail;
- fail review в полном workflow каскадом проходит implement → test → review.

**Сделано.** Отличия от плана:
- **`Next` теперь возвращает `(State, string)`**, не только `State`. Второе значение —
  причина, осмысленная только когда результат `StateFailed`; её нужно было куда-то
  положить, чтобы `«tests failed»` из В5 попало в `run.FailureReason`, а не потерялось.
  Отдельного `transitions_test.go` раньше не было (переходы проверялись только через
  `Engine.Run` в `engine_test.go`), так что смена сигнатуры не задела внешних тестов —
  только единственный вызов в `engine.go`.
- **`forward()` отказывает явно**, если `current` вообще не найден в `wf.Steps` (а не
  тихо считает это концом workflow). Старый `default: return StateFailed` в switch по
  `current` был защитой на случай неизвестного состояния; при переходе на «следующий
  шаг по позиции» эта защита могла незаметно потеряться — сохранил её отдельной веткой.
- **Добавлена константа `StateResearch`** — раньше не было нужна ни одному существующему
  вызову, но без неё нечем было явно обозначить стадию в тесте на
  `research → analysis → done`.
- **Явный `case StateAnswer: return StateDone` убран** — обобщённый переход по позиции
  даёт тот же результат: `answer` как последний (и единственный) шаг в workflow
  `BuildLegacy` при успехе уходит в `Done` точно так же. `Dispatcher.executeAnswer`
  никогда не возвращает `OutcomeFailure` (только `err != nil`, который в `Next` вообще
  не попадает), так что теоретическое отличие поведения на failure-ветке ничем не
  проверяемо и не наблюдаемо на практике — старый flow (`app.go`,
  `interaction/editplan.go`) не тронут и весь его набор тестов зелёный без правок.

### T06 — Jev HTTP client

**Файлы:** `internal/decision/jev/client.go` и тест; `internal/apperr` (`DecisionError`).

1. `net/http` и Bearer. Ключ передаётся в конструктор: `app` читает его из env.
2. Неэкспортируемые `systemOneRequest/Response`, `question`, `answer` по разделу 2.
   `question.Criteria` имеет тип `any`: map для choice, массив для score, объект для noul.
3. `decide(ctx, req)` → `POST /v1/systemone`; `listModels(ctx)` → `GET /v1/models` (T14).
4. `apperr.DecisionError{Provider:"jev", Kind}`:
   - timeout и `408` → `Timeout`;
   - dial, DNS, reset → `Connection`;
   - `429` → `RateLimited`;
   - `529` и `5xx` → `Unavailable`;
   - `401` и `403` → `Auth`;
   - `422` и прочие `4xx` → `BadRequest`, тело ответа ≤ 2 KB;
   - не-JSON → `InvalidResponse`.

   `Kind.Transient()` используется для retry. `exitcode`: `DecisionError` → 3,
   `Auth` → 2.
5. Retry только для `Transient`, не больше `max_retries` раз, с учётом `Retry-After` и в
   пределах `timeout`. Отмена ctx (Ctrl-C) даёт `FailureCancelled` без retry.

**Acceptance:**
- `httptest.Server` на каждый статус;
- проверка Bearer;
- ключ не встречается ни в одной ошибке;
- на `529` — ровно один повтор, на `401` — ни одного.

**Сделано.** Отличия от плана:
- **Mock-клиент на время без доступа к API.** Регистрация в Jev сейчас закрыта, ключа нет.
  Поэтому `Client` — интерфейс с неэкспортируемыми методами `decide`/`listModels`
  (реализовать его можно только внутри `jev`, изоляция transport-типов сохраняется), и у
  него две реализации:
  - `NewHTTPClient(Options, apiKey)` — настоящий клиент по плану, покрыт тестами на
    `httptest.Server`;
  - `NewMockClient()` — офлайн-заглушка. Запрос проходит через тот же JSON, что ушёл бы
    по сети, и проверяется, как это делает Jev: неизвестный `type`, неверная форма
    `criteria`, 1 или 11 уровней score дают `BadRequest`/422. Иначе ответ
    детерминированно строится из ключевых слов `state.request`. Mock знает id вопросов
    из раздела 2 (`context_depth`, `complexity`, `complexity_score`, `stage_*`), на
    остальные отвечает нейтрально (равномерное распределение, `noul = 0.5`). В
    `response.model` приходит `mock:<model>`, чтобы офлайн-trace нельзя было спутать
    с настоящим. Числа правдоподобные, но не калиброванные: mock нужен, чтобы
    pipeline работал end-to-end, а не для настройки порогов.
- **`decision.jev.mock: true`** в config выбирает mock. При нём
  `ValidateDecisionConfigDependencies` не требует переменной с ключом. Остальная
  валидация `decision.jev` не меняется. Какой клиент создать, по флагу решает `app` (T12).
  Mock — замена транспорта Jev для разработки и тестов. Настоящее решение без Jev даёт
  `decision.provider: claude` (T22).
- **`apperr.DecisionFailureKind` — отдельный тип**, а не расширение `FailureKind`
  CLI-провайдеров. У `FailureKind` описания привязаны к CLI («exceeded
  execution.timeout»), а `Transient()` для spawn/exit не имеет смысла. Отмена —
  `DecisionCancelled` (в плане `FailureCancelled`). Она оборачивает `context.Canceled`,
  поэтому `exitcode` даёт 5.
- **Трактовка «в пределах timeout».** `timeout` ограничивает одну попытку, у каждого
  повтора он свой. Пауза перед повтором — `Retry-After`, если сервер его прислал
  (секунды или HTTP-date). Иначе 200 мс с удвоением, но не больше `timeout`.
  `Retry-After` длиннее `timeout` не ждём и сразу возвращаем ошибку. Истёкший
  родительский context (`execution.timeout`) не повторяется, как и отмена.
- **Ключ вычищается из ошибок**: и из тела ответа, и из текста причины (на случай,
  если прокси или сервер эхом вернёт заголовки). Тело в ошибке — до 2 KB для любого
  не-2xx статуса, не только 4xx: для 5xx оно тоже помогает понять причину.
- `1xx`/`3xx` без ответа считаются `InvalidResponse`. Ошибка сборки запроса (битый
  `base_url`, `config` его уже проверил) — `BadRequest`.

### T07 — `DecisionProvider` и Jev provider

**Файлы:** `internal/decision/{provider,questions}.go`, `internal/decision/jev/{provider,mapper}.go`.

1. Интерфейс по SPEC §10: `ContextDepth(ctx, request)`, `Classify(ctx, input)`, `Name()`.
2. `internal/decision/questions.go` — тексты вопросов и критериев из раздела 2 как
   константы. Они лежат вне `jev`, потому что их же использует LLM-провайдер (T22).
   Transport-форму (`question`) из них собирает `jev`. Критерии
   complexity хранятся **одним** списком, из него строятся и map для `choice`, и массив
   для `score`, чтобы тексты не разошлись.
3. Запрос classification содержит `complexity` (choice), а при `shadow: true` — ещё и
   `complexity_score` (score). Какой из них основной, провайдер не знает. Он возвращает
   оба распределения, выбор делает T03.
4. State: без проекта — `project_context_available: false`, при усечении —
   `context_truncated: true` (MVP.md §40).
5. Mapper:
   - на месте все ожидаемые id, у каждого правильный `type`;
   - `probabilities` содержат ровно наши варианты;
   - у score индексы уровней `"0".."2"` маппятся на complexity через `legend` и порядок;
   - `noul` лежит в [0,1];
   - любое нарушение → `InvalidResponse`;
   - поле `choice` из ответа **игнорируется**;
   - `response.model` записывается в `TaskClassification.Model`.

**Acceptance:** golden-файлы запросов (что уходит в Jev, с `shadow` и без); fixture-ответы
маппятся в доменные типы; каждый вид невалидного ответа даёт `InvalidResponse`, в том
числе score с неожиданной `legend`.

**Сделано.** Отличия от плана:
- **Интерфейс называется `decision.Provider`**, а не `DecisionProvider`: иначе получится
  `decision.DecisionProvider`. Методы — как в плане.
- **Вопросы — типизированные значения, а не JSON-константы.** `internal/decision/questions.go`
  отдаёт `ChoiceQuestion`, `ScoreQuestion` и `YesNoQuestion` (каждый раз свежую копию). В
  transport-форму их переводит `jev`, LLM (T22) соберёт из них prompt. Тексты
  planning/testing/review и критерии true/false для всех stages в разделе 2 были
  многоточиями — дописаны. Их нужно вычитать: они влияют на калибровку порогов.
- **Провайдер получает список вопросов complexity, а не флаг `shadow`**:
  `NewProvider(client, model, sources)`. Так работает и `primary: score` без shadow (в
  запросе только `complexity_score`). Какой вопрос основной, провайдер по-прежнему не
  знает. Список собирает `app` из `primary` и `shadow`.
- **Порядок вариантов choice сохраняется.** Go сериализует map по алфавиту (complex,
  medium, simple). Criteria кодируются в порядке вопросов, потому что модель читает текст
  последовательно.
- **Mapper строже плана.** Ошибкой считаются ещё: лишний ответ (id, о котором не
  спрашивали), пустой `response.model`, отсутствующие `confidence` или `score`. После
  разбора результат проверяется `TaskClassification.Validate()` (сумма распределения,
  диапазоны).
- **Проверка `legend` — допущение.** Документация не говорит, что именно лежит в
  `legend`. Mapper требует точного совпадения с текстами наших уровней (mock так и
  отвечает). Проверить на настоящем API, когда появится доступ. Если Jev кладёт туда
  что-то другое, правится одна функция `complexityFromScore`.
- **`context_truncated: true` в state дублирует `project.truncated`**: поле
  `ProjectContext.Truncated` сериализуется как есть. Лишнее поле безвредно, отдельно
  прятать его не стал.
- Golden-файлы обновляются командой `go test ./internal/decision/jev -update`. Есть
  end-to-end тест провайдера с mock-клиентом из T06.

### T08 — Context Manager (базовый)

**Файлы:** `internal/projectctx/{manager,secrets,docs,metadata,git,tests,relevant}.go`.

1. `Manager.Build(ctx, request, requestEN) (*domain.ProjectContext, error)`. Токены для
   поиска берутся из обоих текстов: идентификаторы (`ProductRepository`) при переводе не
   меняются, а английские слова лучше совпадают с путями.
2. Список файлов — `git ls-files --cached --others --exclude-standard` через `repository`,
   поэтому `.gitignore` учитывается. `filepath.Walk` не используется.
3. Deny list до любого чтения: `.env`, `.env.*`, `*.pem`, `*.key`, `credentials*`,
   `secrets*` (по basename, без учёта регистра) плюс `context.extra_deny`. Не читаются
   бинарные файлы (NUL в первых 8 KB) и файлы больше 256 KB.
4. Rules — bullet-строки из `AGENTS.md`, `CLAUDE.md` и `CONTRIBUTING.md`. Из `README.md` и
   `docs/*.md` — только секции, где есть токены запроса (MVP.md §7.1).
5. Metadata: язык и тип проекта по `go.mod`, `package.json`, `pyproject.toml`,
   `Cargo.toml`, `pom.xml`; module; прямые зависимости.
6. TestInfo: есть ли тесты; unit или integration (по каталогам и build tags).
7. Git: branch, dirty, изменённые пути (тоже через deny list).
8. Relevant files (по путям): совпадение токенов с путями плюс связанные тесты. Summary —
   doc-комментарий пакета или первые строки. Символы — в T17.
9. Бюджет `max_files`/`max_bytes`: сначала отбрасываются наименее релевантные файлы,
   потом rules. Ставится `Truncated`.

**Acceptance:** fixture-репозиторий в `t.TempDir()` с `.env`, `x.key`, `secrets.yaml`,
файлом из `.gitignore` и бинарником — ни путь, ни содержимое ни одного из них не
попадает в контекст. Бюджет соблюдается. Русский запрос «добавь кэш в ProductRepository»
находит файлы с `ProductRepository`.

**Сделано.** Отличия от плана:
- **Файлы пакета.** `internal/projectctx/{manager,secrets,tokens,docs,metadata,tests,git,relevant}.go`.
  В `repository` добавлены read-only методы `ListFiles`, `Branch`, `ChangedFiles`.
  Manager зависит от интерфейса `Repo` (эти три метода), а acceptance-тест работает на
  настоящем `git` во временном репозитории.
- **Deny list строже плана.** Паттерны сверяются с **каждым сегментом пути**, а не только
  с basename: `secrets/db.txt` тоже закрыт. Паттерн со слешем из `extra_deny` сверяется с
  путём целиком и с его префиксами. Всегда пропускаются `.git/`, `.orchestrator/` (там
  прошлые запросы и ответы моделей) и симлинки: `Lstat`, только обычные файлы, чтобы
  ссылка не вывела за пределы репозитория.
- **Relevant files ищутся не только по путям.** CamelCase-, snake_case- и dotted-
  идентификаторы из запроса ищутся дословно в исходниках (до 2000 файлов и 16 MB). Только
  так русский запрос без перевода находит `ProductRepository`, когда файл называется
  `postgres.go`. Слова запроса сравниваются с частями путей по нестрогой основе
  (cache ≈ caching). Слова капсом (`HTTP`, `API`) идентификаторами не считаются, иначе
  они совпадают с половиной кода. `vendor/`, `node_modules/`, `testdata/`, `build/` и
  подобное в ранжирование не попадают.
- **`Purpose` пока только `test`.** По путям нельзя честно отличить интерфейс от
  реализации, поэтому остальные роли (`interface`, `implementation`, `dependent`)
  заполнит T17. Константы ролей добавлены в `domain`.
- **Выдержки из README и docs/\*.md — отдельное поле `ProjectContext.Docs`**
  (`{path, section, text}`), не `Rules`. Это описание проекта, а не его правила.
  Берётся первый абзац секции, до 5 секций, самые совпадающие первыми.
- **Порядок урезания по бюджету:** сначала docs, потом relevant files с наименее
  релевантного конца, потом rules с конца, потом dependencies и changed files. Метаданные
  и TestInfo не урезаются. `max_bytes` считается по JSON, который уйдёт в провайдера.
- **Изменённые пути** — `git status --untracked-files=normal`: неотслеживаемая
  директория приходит одной строкой (`graphify-out/`), а не сотнями файлов. Не больше 50
  путей, тоже через deny list.
- `ProjectType` — тип экосистемы по манифесту (`go-module`, `npm-package`,
  `python-project`, `cargo-crate`, `maven-project`). `package.json` вместе с
  `tsconfig.json` даёт typescript. Без манифеста язык определяется по самому частому
  расширению. TOML и pom разбираются построчно и регулярками, без новых зависимостей:
  необычная раскладка даст меньше зависимостей, но не ошибку.

### T09 — Classification Service

**Файлы:** `internal/classification/service.go`.

1. `translate.Prepare(request)` (T15) → `request`, `requestEN`.
2. `ContextDepth(requestEN)` → `policy.SelectContextDepth`.
3. PROJECT → `Manager.Build`. При ошибке: `project = nil`, `ContextLimited = true`,
   событие `context_collection_failed`, выполнение продолжается.
4. `Classify` → `policy.SelectComplexity` (primary + shadow) → `policy.SelectStages`.
5. `DecisionError` на шаге 2 или 4: при `on_failure: ask` — ручной выбор (T16) с причиной,
   иначе ошибка. Отмена (Ctrl-C) ручной выбор не предлагает.
6. Возвращает `domain.Decision`, ProjectContext и тайминги каждого шага.

**Acceptance:**
- NONE → Manager не вызывается;
- PROJECT → Manager вызывается, контекст попадает в input;
- сбой Manager → работа продолжается с `ContextLimited`;
- сбой Jev на pre-check и на classification → вызывается `Chooser` с причиной;
- `on_failure: fail` → ошибка.

**Сделано.** Отличия от плана:
- **API:** `Service.Classify(ctx, Request{RunID, Text, UserCtx}) (Result, error)`. Зависимости
  — узкие интерфейсы (`Translator`, `decision.Provider`, `ContextBuilder`, `Chooser`),
  `on_failure` приходит как `AskOnFailure bool`: сервис не импортирует `config`.
- **`UserCtx`** — context для ожидания ручного выбора: Ctrl-C его отменяет, а дедлайна
  `execution.timeout` у него нет (требование T12). Его создаёт `app`.
- **`Result`** содержит всё для trace T11: результат перевода, `domain.Decision`, сырые
  ответы провайдера (`Depth`, `Classification`), отправленный `Project`,
  `ContextLimited` и `ContextError` (событие `context_collection_failed` для T18),
  `DecisionError` и `Timings` по шагам (перевод, pre-check, контекст, classification,
  ручной выбор).
- **Ручной выбор не предлагается** ни при отмене (Ctrl-C), ни при истёкшем
  `execution.timeout`: запускать stages уже некогда.
- **`Decision` при ручном выборе:** `source: user`, complexity — только `Selected`,
  stages — через `policy.ApplyForced`. Depth заполнен, если pre-check успел ответить.
- **Context Manager получает `requestEN` только при удавшемся переводе.** Иначе пустую
  строку, чтобы не искать по одному тексту дважды.
- `Source` для Jev ставит `policy.Evaluate` (`jev`). Для LLM-провайдера его выставит T22
  по `Provider.Name()`.

### T10 — Action `research`, удаление `answer`

**Файлы:** `internal/action/{research,prompts,dispatcher,context}.go`, `domain/artifact.go`.

- Новый read-only action `research`: `ACTION: research`, свой artifact, routing
  `actions.research`.
- Результат research попадает в контекст analyze и plan.
- `answer` удаляется. Если development в workflow нет, итоговый ответ пользователю
  формирует prompt последнего read-only шага (В4).
- Stages получают **оригинальный** текст запроса.

**Acceptance:** unit-тесты prompt'ов; сценарий research → analysis не оставляет изменений
в git.

**Сделано.** Отличия от плана:
- **`answer` не удалён — переносится в T12**, как и в T02–T05. Его ещё использует
  старый flow (`BuildLegacy`, `app`, `actions.answer` в config). Удалять его до того, как
  T12 заменит flow целиком, значит сломать то, что сейчас работает.
- **Research:** `ResearchArtifact{summary, findings, options, recommendation,
  references}`, routing и timeout — `actions.research`.
- **Контекст дальше по цепочке:** research передаётся в analyze и plan (по плану), а
  analysis — ещё и в plan. Раньше analyze и plan не встречались в одном workflow; теперь
  stages research → analysis → planning идут подряд, и план без результатов анализа
  потерял бы их.
- **Итоговый ответ (В4).** Если в workflow нет implement, последний read-only шаг
  (research, analyze или plan) просит в своём JSON поле `answer` — полный ответ
  пользователю на языке запроса. Поле `answer` появилось во всех трёх артефактах
  (`omitempty`). Показ ответа в консоли — T11/T12.
- **Read-only prompts.** Все три read-only prompt'а явно запрещают менять файлы. Гарантия
  на уровне orchestrator'а проверяется тестом: research → analysis на настоящем
  git-репозитории оставляет его чистым. Помешать CLI самому изменить файлы orchestrator
  не может — так было и раньше с analyze.

### T11 — Decision trace и вывод в консоль

**Файлы:** `internal/persistence/filesystem.go`, `internal/domain/run.go`,
`internal/logging/progress.go`.

- `decision.json` вместо `classification.json`. Поля:
  - `request`, `request_en`, `translation` — `{detected, translated, provider, model,
    failed, latency}`;
  - `source` — `jev`, `llm` или `user`, плюс `decision_error`, если был сбой;
  - `provider` и `model` — `jev` и версия из ответа Jev либо CLI-провайдер и его модель (T22);
  - depth: вероятности, выбор, порог; `context_limited`;
  - complexity: `primary` и `shadow` — распределение, confidence, score, выбор;
    `agree`;
  - stages: `{probability, threshold, forced, enabled, reason}`;
  - workflow, латентности.
- `project_context.json` — ровно то, что ушло в Jev.
- В консоль:
  - `context: project (p=0.91)`;
  - `complexity: medium (choice p=…, score=1.2 → medium ✓)`, при расхождении — пометка;
  - workflow;
  - с `--verbose` — таблица stages;
  - строка перевода, если он был.

**Acceptance:** golden-тест формата. Из `decision.json` восстанавливается, почему включён
каждый stage и как разошлись choice и score.

**Сделано.** Отличия от плана:
- **Тип trace — `domain.DecisionTrace`** в новом `internal/domain/trace.go`, а не в
  `run.go`. Его собирает `classification.Result.Trace(workflow)`, пишет
  `persistence.Store.SaveDecision`, выводит `logging.RenderDecision`. Так `persistence`
  и `logging` не зависят от `classification`. `domain.Run` не менялся: замена
  `Run.Classification` на решение нового flow — часть T12.
- **Поля.** Как в плане, плюс `provider` (имя decision provider — понадобится с T22),
  `context_error`, `translation.cyrillic_share`. `jev_error` называется `decision_error`:
  провайдер не обязательно Jev. Latency хранятся в миллисекундах (`*_ms`), а не как
  `time.Duration` в наносекундах. У `context_depth` есть и распределение провайдера,
  и выбор policy. Если pre-check упал, этого поля нет.
- **`project_context.json`** пишется только когда контекст реально ушёл в провайдера:
  само наличие файла означает, что контекст использовался (сценарий 2 в T13).
- **Консоль.** Формат зафиксирован golden-файлами `internal/logging/testdata/decision_*.golden`
  (обновление: `go test ./internal/logging -update`). Расхождение choice и score
  помечается `✗ disagrees`, совпадение — `✓`. Сбой перевода выводится как `Warning:`.
  При ручном выборе вместо модели показывается первая строка ошибки провайдера.
- `classification.json` старого flow остаётся до T12. Итоговый ответ read-only шага (T10)
  в консоль пока не выводится — это делает T12 в конце run.

### T12 — Wiring в `app`

**Файлы:** `internal/app/app.go`. Удаляются `internal/classifier`, `TaskType`, старый
`Classification`. Удаляется и текущий `interaction/editplan.go`: до T19 режимы
`--mode plan|manual` показывают decision и просят подтверждения без редактирования.

- **Fail-fast.** `Load → Validate → ValidateDependencies` (в итерации 2 — ещё T14).
  Только после этого создаются decision provider (Jev или LLM, T22), переводчик и
  провайдеры.
- **Таймаут.** `execution.timeout` покрывает перевод, pre-check, сбор контекста и
  classification. Время ожидания ручного выбора пользователя в него **не входит**:
  отдельный context без дедлайна, как для gate в `manual`.
- **Ключ.** API key читается из env в `app` и уходит только в конструктор Jev client.
  При `decision.provider` ≠ `jev` ключ не читается и не требуется.

**Сделано.** Отличия от плана:
- **Fail-fast — внутри `Validate` и `ValidateDependencies`,** а не отдельными шагами.
  Проверки decision/translation/policy/context из T02/T22 теперь часть `Validate`, а
  ключ Jev и executables переводчика и LLM decision provider — часть
  `ValidateDependencies`. Отдельные `ValidateDecisionConfig*` удалены. Цепочка в `app`
  та же: `Load → Validate → ValidateDependencies`, до создания любого провайдера.
- **`version: 2` обязателен.** Для `version: 1` ошибка перечисляет, что поменялось, и
  отсылает к `orchestrator.example.yaml`. Пример переведён на v2, и тест проверяет, что
  он проходит `Validate`.
- **`domain.Run.Decision`** заменил `Classification`/`OriginalClassification`. Полный
  trace — в `decision.json`.
- **Таймаут.** `execution.timeout` отсчитывается от начала решения. Если был ручной
  выбор, workflow получает новый дедлайн: время пользователя возвращается в бюджет
  (`engineContext`). Сам выбор ждёт на signal-context без дедлайна.
- **Интерактивность для ручного выбора:** не `--mode auto` и stdin — терминал. Если
  stdin — настоящий файл, но не терминал (pipe, `/dev/null`, CI), выбор сразу падает с
  ошибкой, а не читает EOF. Любой другой `io.Reader` считается скриптованным вводом
  (так работают integration-тесты).
- **Подтверждение без редактирования** (до T19): `[a]pprove / [c]ancel`. В `manual`
  отказ от шага даёт `[r]etry / [a]bort`.
- **`decision.jev.mock: true`** печатает в stderr предупреждение, что решения идут от
  офлайн-эвристики, а не от Jev.
- **Итоговый ответ (В4)** выводится после успешного run без development: `Answer:` и
  поле `answer` последнего read-only шага.
- **Удалены** `internal/classifier`, `domain.Classification`/`TaskType`/`LegacyComplexity`,
  `workflow.BuildLegacy`, action `answer` (+ `actions.answer`, `AnswerArtifact`),
  `interaction/editplan.go`, `apperr.ClassificationError`, `classification.json` и
  `logs/classify.*`. `ClassifyReporter` переименован в `CallReporter` — им пользуются
  перевод и LLM decision provider.
- **T13 п. 10 сделан здесь:** без него T12 не была бы зелёной. Существующие сценарии
  переведены на новый flow. Решение по умолчанию даёт фейковый Jev на `httptest.Server`
  (`test/integration/jev_test.go`, отвечает по id вопросов). Сценарии 8/12/13 (невалидный
  ответ, сбой и таймаут решения) — на LLM decision provider через fake CLI.
- `CLAUDE.md`, `README.md` и корневой `MVP.md` пока описывают старый flow. Их обновление
  — T20.

### T13 — Integration tests с mock Jev

Mock Jev — `httptest.Server`, отвечает по id вопросов. Провайдеры (в том числе
переводчик) — через fake CLI harness. Сценарии:
1. PROJECT: в теле запроса к Jev нет `.env` и его содержимого; workflow и порядок actions
   совпадают с ожидаемыми.
2. NONE: Context Manager не вызывается, `project_context.json` не создаётся.
3. complex при низких вероятностях stages: forced stages включены.
4. Сбой Context Manager: `context_limited: true`, run продолжается.
5. Русский запрос: переводчик вызван, в Jev уходит английский текст, а stage prompts
   получают оригинал. Сбой перевода: в Jev уходит оригинал, `translation.failed: true`.
6. Jev 529 два раза + `on_failure: ask`: scripted stdin выбирает complexity и stages →
   run продолжается, `source: user`, forced stages добавлены.
7. Сбой Jev в `--mode auto` и Jev 401 при `on_failure: fail`: exit code по T06,
   CLI-провайдеры stages не вызываются.
8. Нет `JEV_API_KEY` → ошибка до любого HTTP-запроса и вызова CLI.
9. choice и score расходятся → `agree: false`, решение по primary.
10. Существующие сценарии 1–13 переведены на новый flow.
11. `decision.provider: claude`: pre-check и classification идут через fake CLI, HTTP к Jev
    нет, `JEV_API_KEY` не задан. Workflow и `decision.json` той же формы, что в сценарии 1,
    `source: llm`.

### T15 — Определение языка и перевод запроса

**Файлы:** `internal/translate/{detect,translator,prompt}.go` и тесты.

1. **Детекция** — детерминированная, без AI: считаются буквы; если доля кириллицы
   ≥ 30 % от всех букв, нужен перевод. Код, пути и идентификаторы в запросе не мешают:
   они латинские, но доля считается по всем буквам. Порог — константа.
2. **Перевод** — один вызов `provider.Client` (`translation.provider/model`) с
   `translation.timeout`, внутри run-wide timeout.
   - Prompt начинается с `ACTION: translate` (маркер для fake-исполняемых в тестах).
   - Требование: перевести на английский, идентификаторы, пути, код, строки в кавычках и
     числа оставить как есть, вывести только перевод.
   - Текст пользователя в prompt изолирован как недоверенные данные.
3. **Проверка результата** — только санитарная:
   - текст не пустой;
   - длина от 0.3× до 3× оригинала;
   - доля кириллицы < 10 %;
   - все CamelCase- и snake_case-идентификаторы и пути из оригинала есть в переводе.
4. **Сбой** (ошибка CLI, таймаут или проверка не прошла): в Jev уходит оригинал, в trace
   пишется `translation.failed: true` с причиной, в консоли — предупреждение. Run не
   падает: Jev обработает русский текст, только хуже.
5. Логи вызова — `logs/translate.{stdout,stderr}.log`, по аналогии с classify. Там же
   observer и heartbeat.

**Acceptance:**
- детекция на наборе: русский, английский, смешанный с кодом, только идентификаторы;
- в fake-переводе с потерянным `ProductRepository` проверка не проходит → оригинал;
- английский запрос не вызывает CLI.

**Сделано.** Отличия от плана:
- **Код не входит в подсчёт доли кириллицы.** По правилу «доля от всех букв» пути и
  идентификаторы размывают русскую фразу: «исправь panic в internal/config/loader.go и
  config_test.go» набирает 19 % и не переводится. Поэтому идентификаторы, пути и
  `backticks` вырезаются до подсчёта, и доля считается по буквам прозы. Запрос из одних
  идентификаторов («ProductRepository.Get») букв прозы не содержит и не переводится. Порог
  30 % — константа `CyrillicThreshold`.
- **API:** `Translator.Prepare(ctx, runID, request) (Result, error)`. В `Result` есть
  `Text` (что уходит в decision provider), `Original` (что получают stages) и поля для
  trace: `detected {language, cyrillic_share}`, `translated`, `failed`, `reason`,
  `provider`, `model`, `latency`. Ошибка возвращается только при завершении самого run
  (Ctrl-C, `execution.timeout`). Любой сбой перевода даёт `failed: true` с причиной и
  оригинал. Предупреждение в консоль выводит wiring (T11/T12) по `Reason`.
- **Закрывающий тег в тексте обезврежен.** `</request>` в запросе превращается в
  `</ request>`, иначе пользовательский текст мог бы закрыть блок данных раньше и
  дописать инструкции модели.
- **Вывод модели чистится**: code fences и эхо тегов `<request>` снимаются до проверки.
- Проверка сохранности — CamelCase/snake_case-идентификаторы и пути (`a/b.go`,
  `README.md`). Слова капсом (`HTTP`, `API`) не проверяются: переводчик вправе
  перестроить фразу вокруг них.
- **Логи.** В `persistence` появился общий `AppendCallLog(runID, name, stream, content)`
  и `CallLogPath` для вызовов вне шагов workflow (`logs/translate.*`, дальше
  `logs/decide-*.*` в T22). `AppendClassifierLog` теперь вызывает его, до удаления в T12.

### T16 — Ручной выбор при сбое Jev

**Файлы:** `internal/interaction/choose.go`, использует `Prompter`.

1. `Chooser.Choose(ctx, reason, policy) (Complexity, []Stage, error)`.
2. Сначала выводится причина: `Jev unavailable: 529 overloaded (after 1 retry)`. Если
   pre-check прошёл, показывается и собранный контекст.
3. Выбор complexity: `[1] simple  [2] medium  [3] complex`.
4. Выбор stages — список в фиксированном порядке, ввод номеров через запятую.
   - Forced stages для выбранной complexity уже отмечены и выключаются только с явным
     предупреждением. Так forced из config сохраняет силу.
   - Пустой выбор не принимается.
5. Результат идёт через `policy.ApplyForced` и `workflow.Build`, в trace пишется
   `source: user`.
6. Без TTY или в `--mode auto` выбор невозможен: `DecisionError` (exit 3) с сообщением,
   как повторить в интерактивном режиме.
7. Ctrl-C или EOF → отмена run (exit 5).

**Acceptance:** scripted stdin — корректный выбор, невалидный ввод с повтором запроса,
пустой выбор, EOF; режим auto отдаёт ошибку без попытки чтения stdin.

**Сделано.** Отличия от плана:
- **Сигнатура:** `Chooser.Choose(ctx, cause, project, policy) (ManualChoice, error)`.
  `cause` — ошибка провайдера (выводится первой строкой), `project` — собранный контекст
  или `nil`, если pre-check не прошёл. `ManualChoice{Complexity, Stages, Excluded}`.
  Интерактивность (TTY и не `--mode auto`) передаётся в `NewChooser`: определять её —
  дело `app`.
- **`policy.ApplyForced` получил параметр `excluded`.** План разрешает выключить forced
  stage после предупреждения, но `ApplyForced` из T03 добавлял forced обратно
  безусловно. Теперь forced stage из `excluded` остаётся выключенным с `Forced: true` и
  reason `forced: complexity=…, excluded by user`, так что в trace видно, что policy
  переопределили. Правила forced по-прежнему живут только в policy.
- **Ввод stages:** номера или имена через запятую или пробел, `-N` — исключить. Forced
  stages отмечены `*` и входят в выбор без ввода, поэтому пустой ответ при наличии forced
  допустим. Пустым считается итоговый набор: ни выбранных, ни оставшихся forced.
  Исключение forced: предупреждение и вопрос `[y/N]`, по умолчанию «нет».
- **Без TTY / в `--mode auto`** возвращается исходная ошибка провайдера, обёрнутая
  инструкцией, а не новый `DecisionError`. Так exit code остаётся по T06: 3, а для `Auth`
  — 2 (сценарий 7 в T13). stdin не читается.
- **EOF и Ctrl-C** оборачивают `context.Canceled`, exit 5. Ctrl-C проверяется до и после
  каждого вопроса: блокирующее чтение stdin прервать нельзя, как и у gate в `manual`.

### T22 — Выбор decision provider в config: Jev или LLM

**Файлы:** `internal/config/decision.go` и тесты; `internal/decision/llm/{provider,prompt,parse}.go`
и тесты; `internal/decision/questions.go` (из T07).

Decision provider задаётся в config так же, как `classifier` сейчас: провайдер и модель.
Варианты — `jev` или CLI-провайдер (`claude`, `codex`, `gemini`). **Flow не меняется:**
перевод → pre-check → Context Manager → classification → Policy Engine → Workflow Builder →
engine. Меняется только, кто отвечает на вопросы. Classification Service, policy, ручной
выбор при сбое и trace работают с `DecisionProvider` и не знают, какой он.

```yaml
decision:
  provider: jev                # jev | claude | codex | gemini
  model: jev-1.13.0            # jev — версия модели Jev; иначе — модель CLI
  timeout: 5s                  # jev — на один HTTP-запрос; иначе — на один вызов CLI
  complexity:
    primary: choice
    shadow: true
  on_failure: ask
  jev:                         # только для provider: jev
    base_url: https://api.typesafe.ai
    api_key_env: JEV_API_KEY
    max_retries: 1
    mock: false
```

То же с LLM:

```yaml
decision:
  provider: claude
  model: sonnet
  timeout: 60s
  complexity: { primary: choice, shadow: true }
  on_failure: ask
```

1. **Config.** `model`, `timeout` и `complexity` переезжают из `decision.jev` на уровень
   `decision`: они общие для всех провайдеров. В `decision.jev` остаются только транспортные
   поля Jev.
   - `provider` — `jev` или провайдер из того же списка, что у actions.
   - `provider: jev` — блок `jev` валидируется как в T02, ключ в env обязателен (кроме `mock`).
   - LLM — блок `jev` не обязателен. Если он задан, то не валидируется и не используется:
     переключение провайдера — правка одной строки. Executable провайдера проверяется в
     `ValidateDecisionConfigDependencies` через `checkExecutablesPresent`, до любого
     AI-вызова. `JEV_API_KEY` не нужен.
   - `timeout > 0` для всех. У LLM он, как у translation, внутри `execution.timeout`.
2. **Вопросы.** Тексты и критерии из `internal/decision/questions.go` (T07) общие: Jev
   собирает из них transport-запрос, LLM — prompt. Разные формулировки сделали бы ответы
   несравнимыми.
3. **`llm.Provider`** реализует `DecisionProvider` через `provider.Client`, как переводчик
   (T15). Специфика CLI — только в `buildArgs`. Два вызова CLI вместо двух HTTP-запросов:
   - `ContextDepth` — prompt с маркером `ACTION: decide-context`. Ответ:
     `{"context_depth": {"none": p, "project": p}}`.
   - `Classify` — `ACTION: decide-classify`. В prompt идёт state в том же виде, что и в
     Jev: `request`, `project_context_available`, `context_truncated`, `project`. Ответ:
     `{"complexity": {simple, medium, complex}, "complexity_score": {"0", "1", "2"},
     "stages": {research … review}}`. `complexity_score` запрашивается только при
     `shadow: true`.
   - Запрос пользователя и ProjectContext изолированы в prompt как недоверенные данные.
   - Prompt требует вероятности, а не итоговый выбор (SPEC §8): решает policy.
   - Логи — `logs/decide-{context,classify}.{stdout,stderr}.log`, как у translate.
4. **Разбор ответа** — по правилам mapper'а Jev (T07):
   - JSON извлекается так же, как в `action/jsonextract.go`;
   - ключи — ровно наши варианты и stages, значения в [0,1];
   - сумма распределения 1 ± 0.05, затем нормализация до 1 (LLM округляет);
   - любое нарушение → `InvalidResponse`.

   Своего confidence у LLM нет: `Confidence` = максимальная вероятность распределения,
   `Score` score-вопроса = Σ i·p(i). В `TaskClassification.Provider/Model` пишутся
   CLI-провайдер и модель из config.
5. **Ошибки** → `apperr.DecisionError{Provider: "<cli>", Kind}`:
   - таймаут CLI → `Timeout`;
   - spawn или ненулевой exit → `Unavailable`;
   - неразбираемый или невалидный ответ → `InvalidResponse`;
   - Ctrl-C → `DecisionCancelled`.

   Исходный `ProviderError` сохраняется в `Err`. Retry нет. Поскольку это тот же
   `DecisionError`, `on_failure: ask|fail` и ручной выбор (T16) работают без изменений.
6. **Trace и консоль (T11).** `source: llm` (`domain.DecisionSource` получает третье
   значение), `provider`/`model` — из `TaskClassification`. Консоль вместо версии Jev
   показывает `decision: claude/sonnet`.
7. **Wiring (T12).** `app` по `decision.provider` создаёт `jev.NewProvider(NewHTTPClient |
   NewMockClient)` или `llm.NewProvider(client, model, timeout)`. Больше `decision.provider`
   нигде не читается.

**Не входит:** автоматический переход Jev → LLM при сбое и любой выбор провайдера по
ситуации. Routing статический (MVP.md §31.1), сбой провайдера ведёт к ручному выбору (В9).

**Acceptance:**
- config:
  - `provider: claude` без блока `jev` и без `JEV_API_KEY` проходит;
  - `provider: jev` без ключа — ошибка;
  - неизвестный провайдер — ошибка;
  - отсутствующий executable — ошибка до любого AI-вызова;
- fake CLI:
  - prompt содержит те же тексты вопросов, что уходят в Jev;
  - ответ маппится в те же доменные типы;
  - каждый вид невалидного ответа даёт `InvalidResponse`;
  - при `shadow: false` `complexity_score` нет ни в prompt, ни в результате;
- сбой CLI: при `on_failure: ask` — ручной выбор, при `fail` — exit 3;
- один и тот же запрос с mock Jev и с fake LLM даёт `decision.json` одной формы. Отличаются
  только `source`, `provider`, `model` и числа.

**Сделано.** Отличия от плана:
- **`Source()` в интерфейсе `decision.Provider`.** Jev возвращает `jev`, LLM — `llm`, и
  сервис записывает это в решение. Без метода пришлось бы сравнивать имя провайдера со
  строкой `"jev"`.
- **State вынесен в `internal/decision/state.go`** (`PreCheckState`,
  `NewClassificationState`). Jev и LLM получают его из одного места. Golden-файлы
  запросов Jev (T07) при этом не изменились.
- **Config.** `JevComplexityConfig` переименован в `ComplexityQuestionsConfig`, у него
  появился метод `Sources()` (основной вопрос плюс теневой), по которому `app` соберёт
  список для провайдеров. Литеральный `decision.jev.api_key` отклоняется при любом
  провайдере: секрет в файле опасен независимо от того, кто его читает.
- **Prompt LLM.** Вопросы и критерии — из `internal/decision/questions.go`, state — тот
  же JSON, что уходит в Jev, внутри `<state>…</state>`. `encoding/json` экранирует
  `<`/`>`, поэтому запрос не может закрыть блок. Из JSON-ответа извлекается первый
  объект (fences и проза вокруг допускаются). Неизвестные поля и ответ на незаданный
  `complexity_score` — ошибка.
- **Ошибки CLI.** Таймаут даёт `Timeout`, отмена — `DecisionCancelled`, любой другой сбой
  — `Unavailable`. Исходный `ProviderError` лежит внутри, с `Action: decide-*` и путём
  к логу. В `InvalidResponse` сохраняется вывод модели (до 2 KB).
- **Сравнимость проверена тестом:** один запрос через mock Jev и через fake LLM даёт
  `decision.json` с одинаковым набором полей.

---

## 6. Задачи — итерация 2

### T14 — Startup-проверка модели
- Только при `decision.provider: jev` и без `mock`.
- Выполняется после `ValidateDependencies`, в `validate` и в `run`: `GET /v1/models`.
- Проверка пройдена, если модель есть в списке **или** совпадает с
  `^jev-\d+\.\d+\.\d+$`: версионные ID API принимает, но не перечисляет.
- Иначе ошибка: `jev decision provider: configured model "X" is unavailable`.
- Сетевая ошибка при проверке: с `on_failure: ask` — предупреждение (при классификации
  всё равно будет ручной выбор), с `fail` — ошибка.

### T17 — Relevant files по символам
- Go: `go/parser`, только декларации.
- Токены запроса (оригинал и перевод) сопоставляются с экспортируемыми типами и функциями.
- `Purpose`: interface / implementation / test / dependent. Summary — doc и сигнатуры.
- Реализации интерфейса ищутся эвристикой по совпадающему набору методов.
- Fixture — `ProductRepository` из MVP.md §9.

### T18 — Structured logs и metrics, отчёт choice vs score
- Событие `task_classified` со всеми полями SPEC §19, плюс `model`, `source`, `translated`,
  блок complexity `{choice, score, agree}`.
- События `context_collection_failed`, `decision_manual_choice`, `translation_failed`.
- Metrics из SPEC §19. Labels: `provider`, `decision_type`, `result` — без request и путей.
  `decision_fallback_total` теперь считает ручные выборы.
- Для В1 — команда `orchestrator decisions report`. Она читает `metrics.jsonl` / `decision.json`
  всех runs и выводит:
  - долю совпадений choice и score;
  - матрицу расхождений 3×3;
  - средние confidence обоих вопросов;
  - список runs с расхождением.
- Формат хранения — В10.

### T19 — Интерактивное редактирование stages
- В `--mode plan|manual` видны вероятности и пороги.
- Stages можно включать и выключать тем же компонентом, что в T16. Порядок фиксирован.
- Правки попадают в trace как `overridden_by_user`.

### T20 — Документация
- Корневой `MVP.md`: новые §8–11, правка §30 (HTTP Jev), раздел про Jev, перевод и
  ручной выбор.
- `README.md`: config, `decision.provider` (Jev или LLM), `JEV_API_KEY`, какие данные уходят на `api.typesafe.ai` (запрос —
  переведённый — и ProjectContext); Jev не обучается на данных клиентов, ZDR есть только
  в enterprise.
- `orchestrator.example.yaml` (v2), `CLAUDE.md`.
- Тест: example-config проходит `Validate`.

### T21 — Итог эксперимента choice vs score
- По отчёту T18 выбрать один вопрос.
- Удалить второй из `questions.go`, mapper и config (`complexity.primary`/`shadow`).
- Перекалибровать пороги complexity под выбранный вопрос.
- Условие старта — достаточная выборка реальных задач, например ≥ 50 runs. Порог
  определим, когда появятся данные.

---

## 7. Вопросы

### Решено

| # | вопрос | решение |
|---|---|---|
| В1 | Complexity: `choice` или `score`? | Оба в одном запросе. Решение по `primary` (по умолчанию `choice`), второй — теневой, только в лог. Сравнение — T18, выбор одного — T21 |
| В2 | Как выбирать complexity | Кумулятивные пороги от самой сложной: `P(complex) ≥ 0.40` → complex; иначе `P(medium)+P(complex) ≥ 0.50` → medium; иначе simple |
| В7 | Язык | Вопросы к Jev — на английском. Запрос на русском переводит LLM перед Jev (T15). Stages получают оригинал |
| В9 | Сбой Jev | Пользователь сам выбирает complexity и stages (T16). Forced stages остаются в силе. Без TTY или в `--mode auto` — ошибка |
| В13 | Переводить ли запрос, если decision provider — LLM? | Да, одинаково для всех провайдеров: flow один, ответы сравнимы |
| В14 | Пороги policy для LLM: общие с Jev или свои? | Общие. Пересматриваются по данным T18 |

### Открыто (есть решение по умолчанию)

| # | вопрос | по умолчанию | до задачи |
|---|---|---|---|
| В3 | Pre-check: `choice none/project` + порог, или `noul` «нужен ли репозиторий?» | `choice` + `project_threshold: 0.5` | T07 |
| В4 | `research` — новый action? `answer` удаляется? | Да и да. Research — внешнее знание (подходы, библиотеки), analysis — разбор кода или проблемы. На вопрос отвечает последний read-only шаг | T01, T10 |
| В5 | Fail в test/review, когда в workflow нет development | Fail тестов → run `failed` («tests failed»). Fail review → run `completed`, вердикт выводится | T05 |
| В6 | Ни один stage не прошёл порог и нет forced | workflow = `[analysis]` | T03 |
| В8 | ProjectContext — только для Jev или ещё и для prompt'ов stages? | Только Jev и trace | T08 |
| В10 | Формат metrics для CLI | `metrics.json` в run dir плюс строка в `.orchestrator/metrics.jsonl`; logs — `slog` JSON в `decision.log` | T18 |
| В11 | Модель для перевода | `claude` / `haiku` — быстрая и дешёвая, переводу большего не нужно | T15 |
| В12 | Сбой перевода: отправить оригинал или предложить ручной выбор? | Отправить оригинал с предупреждением: Jev справится хуже, но справится | T15 |
