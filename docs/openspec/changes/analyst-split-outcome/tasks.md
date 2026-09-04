## 1. Контракт (`internal/runner`)

- [x] 1.1 Добавить `OutcomeSplit` в `agentio.go` (константа + `Outcome.known()`)
- [x] 1.2 Добавить структуру `Split`/`SplitChild` (`id`/`title`/`description`/`depends_on`) и поле `Result.Split`
- [x] 1.3 Расширить валидацию `Questions`: непуст при `outcome ∈ {needs_human, split}`, а не только `needs_human`
- [x] 1.4 Валидация `split.children[]`: непустой список, уникальные `id`, `depends_on` ссылается только на `id` из этого же списка, без циклов
- [x] 1.5 Go-юнит-тесты на 1.3–1.4: пустой список, дубль `id`, висячая ссылка, цикл, валидный случай (плюс пустые title/description — найдено по ходу, естественное расширение той же проверки)
- [x] 1.6 Обновить `docs/contracts/agent-io.md`: таблица полей, пример `split`, обновлённый список из пяти исходов (плюс два места, не названных в задаче: таблица кодов возврата и список отвергаемых нарушений — оба хардкодили старые четыре значения)
- [x] 1.7 Обновить `ResultSpec()` в `agentio.go` — системный промпт агента про схему `result.json` (сегодня перечисляет только четыре исхода и поле `split` там не появится вовсе, если не добавить)

## 2. Граф (`workflow.yaml`)

- [x] 2.0 Добавить `runner.OutcomeSplit` в пакетный список `outcomes` (`internal/tracker/config.go`) — без этого валидатор графа `split` не узнает вовсе
- [x] 2.1 Добавить маршрут `split: {to: Blocked, human: true}` для `analyst`
- [x] 2.2 Добавить тот же маршрут для `implementer` и `reviewer` (только ради `Workflow.Validate()` — роли `split` не эмитят)
- [x] 2.3 Тот же маршрут — в синтетические workflow.yaml юнит-тестов (`internal/tracker/config_test.go`, `internal/pipeline/pipeline_test.go`): у обоих есть свои встроенные YAML-графы для тестов, и `Workflow.Validate()` требует route у **каждой** роли **каждого** такого графа, не только у настоящего `workflow.yaml`
- [x] 2.4 `go build ./...`, `go vet ./...` и `go test ./...` (весь модуль, не только `internal/tracker`) зелёные

## 3. Харнесс golden-кейсов (`cmd/eval-roles`)

- [ ] 3.1 `knownOutcome` (`loadcase.go`) допускает `split`
- [ ] 3.2 `outcomeChecker.Run` (`checkers.go`) проверяет `questions_not_empty` и для `split`, тем же условием, что и `needs_human`
- [ ] 3.3 `go build ./...` для пакета `cmd/eval-roles`

## 4. Роль (`roles/analyst/role.md`)

- [ ] 4.1 «Исходы»: «задача крупная» → `outcome: split` (было `needs_human`)
- [ ] 4.2 Новый параграф о резюме после ответа человека на split-вопрос: подтверждение → `split` снова, без `comet native new` и без повторного Shape; отказ → обычный Shape на всю постановку; любой другой ответ → как новая информация, по общим правилам роли

## 5. Golden-кейс (`evals/analyst/escalation-oversized-split`)

- [ ] 5.1 `expect.yaml`: `outcome: split` (было `needs_human`), `questions_not_empty: true`, `next_owner: human` без изменений
- [ ] 5.2 `./bin/eval-roles --role analyst --clone` — все кейсы `PASS`, включая три существующих (`capability-basic-plan`, `capability-resume-no-reinvoke`, `escalation-ambiguous-decision`) и `escalation-no-false-split`

## 6. Эмпирическая проверка резюме-поведения (живой прогон, реальные деньги)

Харнесс `eval-roles` не умеет подсадить фикстуре предыдущий ответ человека
(design.md, «Non-Goals» и «Risks»), поэтому это единственная проверка
резюме-поведения — не automated golden-кейс, разовое подтверждение перед
верификацией.

- [ ] 6.1 Ответить на живой тикет `EXP-2` (`Q1: yes`, уже в `Blocked` с предложением разбивки от прогона до этого change) через REST на `10.73.10.235`
- [ ] 6.2 `./bin/runner tick --role analyst --tracker jira` (с кредами `JIRA_USER`/`JIRA_PASSWORD`/`JIRA_REVIEWER_USER`/`JIRA_REVIEWER_PASSWORD`/`GITHUB_TOKEN`/`CLAUDE_CODE_OAUTH_TOKEN` в окружении), в фоне
- [ ] 6.3 Разобрать результат: `outcome: split` повторно, `comet native new` не вызывался (по `run.log`/диффу), тикет остался/вернулся в `Blocked`
- [ ] 6.4 Записать находку (включая оговорку: исходное предложение на `EXP-2` посчитано под старым `outcome:needs_human`, до этого change — резюме-инструкция проверяется по смыслу контекста, не по буквальному тегу) в отчёт верификации
