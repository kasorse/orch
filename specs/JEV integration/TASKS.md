# TASKS.md — Jev Integration: план реализации

Источники: [`SPEC.md`](SPEC.md) (нормативный), [`MVP.md`](MVP.md) (развёрнутое описание),
документация Jev — <https://docs.typesafe.ai/llms.txt> (API, модели, ограничения).
Где SPEC и MVP расходятся, побеждает SPEC. Единственное расхождение: MVP.md §28 прерывает
classification при ошибке Context Manager, а SPEC §14 и MVP.md §40 требуют продолжить
работу с `context_limited = true`. Принимаем второе.

Статус: ред. 3. Работа разбита на две итерации. Решения по вопросам собраны в разделе 7.
Оставшиеся вопросы не блокируют старт.

**Отступления от SPEC, принятые осознанно:**
- **Fallback (SPEC §17, AC12).** Вместо `LLMDecisionProvider` при сбое Jev пользователь сам
  выбирает complexity и stages (решение В9).
- **Перевод запроса.** Если запрос не на английском, перед отправкой в Jev его переводит
  LLM (решение В7). В SPEC этого шага нет.
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
Jev pre-check (1 HTTP-запрос, 1 вопрос)
  «нужен ли контекст проекта, чтобы оценить задачу?» → P(none), P(project)
  │  orchestrator выбирает depth по порогу
  ├── NONE ────────────────────────────┐
  └── PROJECT → Context Manager        │
                 → ProjectContext      │
                         ▼             ▼
Jev classification (1 HTTP-запрос, 8 вопросов параллельно)
  complexity (choice) + complexity_score (score) → распределения по simple/medium/complex
  6 stages: yes/no на каждый                      → вероятность каждого stage
  │
  ├── сбой Jev на любом шаге ──► пользователь выбирает complexity и stages (терминал);
  │                              в --mode auto или без TTY — ошибка
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

Jev не принимает решений: он возвращает распределения, а выбор делает orchestrator —
для depth, complexity и stages. Переведённый запрос нужен только для Jev и для поиска
relevant files. Stages получают оригинальный текст.

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
| `internal/classifier`: 1 вызов Claude CLI → `{type, complexity, requires_*}` | перевод (при необходимости) + `DecisionProvider` (Jev): pre-check и classification |
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

---

## 4. Итерации

**Итерация 1 — вертикальный срез.** Цепочка перевод → Jev → Policy → Workflow →
исполнение работает end-to-end на реальном Jev и в integration-тестах с mock Jev. При сбое
Jev пользователь выбирает вручную. Choice и score логируются для сравнения.

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
| T12 | Wiring в `app`, удаление старого classifier | 1 | M | T02–T11, T15, T16 | — |
| T13 | Integration tests с mock Jev | 1 | M | T12 | — |
| T15 | Определение языка и перевод запроса | 1 | M | T02 | — |
| T16 | Ручной выбор при сбое Jev | 1 | S–M | T03, T04 | — |
| T14 | Startup-проверка модели (`GET /v1/models`) | 2 | S | T06, T12 | — |
| T17 | Relevant files по символам (Go) | 2 | M | T08 | — |
| T18 | Structured logs и metrics, отчёт choice vs score | 2 | M | T11 | В10 |
| T19 | Интерактивное редактирование stages | 2 | M | T04, T16 | — |
| T20 | Документация | 2 | S–M | всё | — |
| T21 | Итог эксперимента: оставить choice или score | 2 | S | T18 + данные | — |

Номера задач сохранены с прошлой редакции. В итерацию 1 перенесены T15 и T16 (раньше это
были LLM provider и fallback). Добавлена T21.

```
T01 ─┬─ T02 ── T15 ─────────────────────┐
     ├─ T03 ─┬─ T04 ─┬─ T05              │
     │       │       ├─ T10              │
     │       │       └─ T16 ─────────────┤
     │       └───────────────────────────┤
     ├─ T08 ─────────────────────────────┼─ T09 ── T11 ── T12 ── T13
     └─ T07 ◄── T06 ─────────────────────┘
итерация 2: T14, T17, T18 → T21, T19, T20
```

Порядок работы в итерации 1:
1. Без сети, только unit-тесты: T01, T02, T03, T04, T05, T06.
2. T07, T08, T15, T16.
3. T09, T10, T11.
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
- **Mock-клиент вместо реального доступа.** Регистрация в Jev сейчас закрыта, ключа нет.
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

**Файлы:** `internal/decision/provider.go`, `internal/decision/jev/{provider,questions,mapper}.go`.

1. Интерфейс по SPEC §10: `ContextDepth(ctx, request)`, `Classify(ctx, input)`, `Name()`.
2. `questions.go` — тексты вопросов и критериев из раздела 2 как константы. Критерии
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

### T11 — Decision trace и вывод в консоль

**Файлы:** `internal/persistence/filesystem.go`, `internal/domain/run.go`,
`internal/logging/progress.go`.

- `decision.json` вместо `classification.json`. Поля:
  - `request`, `request_en`, `translation` — `{detected, translated, provider, model,
    failed, latency}`;
  - `source` — `jev` или `user`, плюс `jev_error`, если был сбой;
  - `model` — версия из ответа Jev;
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

### T12 — Wiring в `app`

**Файлы:** `internal/app/app.go`. Удаляются `internal/classifier`, `TaskType`, старый
`Classification`. Удаляется и текущий `interaction/editplan.go`: до T19 режимы
`--mode plan|manual` показывают decision и просят подтверждения без редактирования.

- **Fail-fast.** `Load → Validate → ValidateDependencies` (в итерации 2 — ещё T14).
  Только после этого создаются Jev client, переводчик и провайдеры.
- **Таймаут.** `execution.timeout` покрывает перевод, pre-check, сбор контекста и
  classification. Время ожидания ручного выбора пользователя в него **не входит**:
  отдельный context без дедлайна, как для gate в `manual`.
- **Ключ.** API key читается из env в `app` и уходит только в конструктор Jev client.

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

---

## 6. Задачи — итерация 2

### T14 — Startup-проверка модели
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
- `README.md`: config, `JEV_API_KEY`, какие данные уходят на `api.typesafe.ai` (запрос —
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
