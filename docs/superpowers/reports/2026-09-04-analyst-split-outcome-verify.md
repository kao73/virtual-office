# Отчёт верификации: analyst-split-outcome

Comet Classic tweak, полный режим верификации (делта-спека затрагивает одну
капабилити — `role-native-workflow`, один MODIFIED + два ADDED требования;
`comet state scale` не запускался отдельно — `verify_mode: full` выставлен
вручную сразу при открытии change, объём вышел далеко за порог lightweight
уже на этапе proposal.md, до первой строчки кода).

## Сводка

| Измерение | Статус |
|---|---|
| Полнота | 17/17 задач `tasks.md`; 3/3 требования спеки покрыты |
| Корректность | 3/3 требования реализованы; 2/3 сценариев подтверждены живыми прогонами, 1/3 (decline-ветка) — только кодом и юнит-тестом, без живой проверки — явный, принятый пробел |
| Согласованность | Реализация совпадает с решениями `design.md`, включая находки, обнаруженные уже по ходу build и не входившие в исходный `proposal.md` |

## Полнота

**Задачи.** Все 17 пунктов `tasks.md` отмечены `[x]`, десятью коммитами от
`0fe00a8` («add split outcome to result.json contract») до `24e11e6`
(«record live resume verification on EXP-2»).

**Покрытие требований спекой.** `specs/role-native-workflow/spec.md` этого
change — один `MODIFIED Requirements` и два `ADDED Requirements`, разобраны
по отдельности ниже.

## Корректность

### MODIFIED: Task-size decision precedes Comet Native entry

Сценарий «Oversized постановка is split before Comet Native starts» изменён:
`outcome: needs_human` → `outcome: split` с `split.children[]`.

- Реализация: `roles/analyst/role.md:58-65` — «Останавливайся здесь и
  завершай прогон исходом `split`» (было `needs_human` до этого change).
- **Живой прогон, а не только код**: `EXP-2` (`kao73/expense-tracker`,
  JIRA `10.73.10.235`), прогон `032f6dbe` — постановка «Трекер личных
  расходов» целиком, результат `outcome: split`, `split.children` — 5
  элементов с зависимостями (Категории → Транзакции → Бюджеты → Дашборд,
  плюс независимый Импорт CSV), `comet native new` не вызывался (этот прогон
  относится ещё к предыдущему change, `analyst-early-size-check`, — но он же
  и есть источник живого предложения, на котором проверяется резюме-сценарий
  ниже, так что его достоверность важна и для этого change).
- **Golden-кейс**: `evals/analyst/escalation-oversized-split` обновлён
  (`expect.yaml`: `outcome: split`) и прогнан живьём —
  `./bin/eval-roles --role analyst --clone`, `PASS`
  (`/tmp/eval-analyst-split-outcome.log`).
- Сценарий «Appropriately-scoped постановка proceeds normally» не менялся
  этим change и не тронут: `escalation-no-false-split` по-прежнему `PASS` в
  том же прогоне.

### ADDED: Split proposal carries a validated dependency graph

Реализация: `internal/runner/agentio.go:132-141` (`Split`/`SplitChild`),
`:211` (questions обязателен для `needs_human` ИЛИ `split`), `:230`
(`split.children` обязателен при `outcome: split`), `:303`
(`validateSplitChildren` — уникальность `id`, ссылки `depends_on` только на
существующие `id`), `:345` (`findSplitCycle` — обход в глубину с цветами,
поиск циклов).

- **Scenario «Empty children list is rejected»** — `TestReadResultRejects`
  case «split с пустым children» (`internal/runner/agentio_test.go`),
  свежепрогнано в этом сообщении вместе со всем пакетом.
- **Scenario «Duplicate child id is rejected»** — case «дубль id в
  split.children».
- **Scenario «Dangling or cyclic dependency is rejected»** — два отдельных
  case: «висячая ссылка в depends_on» и «цикл зависимостей в
  split.children» (двухузловой цикл `a→b→a`, реальный обход `dfs`, не
  вырожденный случай).

### ADDED: A human's reply to a split proposal is not re-investigated as Shape work

Реализация: `roles/analyst/role.md:42-56` (три ветки — подтверждение,
отказ, «что-то ещё»).

- **Scenario «Confirmed split ends the resumed run without invoking Comet
  Native»** — подтверждено живым прогоном, специально для этого change:
  ответ `Q1: yes` от учётки `owner` (человеческая, не входит в
  `AgentAccounts()` — в отличие от первой попытки этим же прогоном ответить
  от `admin`, самоисправлено до запуска роли, см. `tasks.md` 6.1), прогон
  `6c0efa30` (`/tmp/runner-tick-exp2-resume.log`) — исход снова `split`, тот
  же список из 5 детей, `EXP-2: split → Blocked`. Прочитан `run.log`
  прогона: единственная команда, упоминающая `comet`/`native`, —
  read-only разведка (`git log/status`, `ls .comet`, `ls docs/comet`);
  самого вызова `comet native new` в логе нет. Рабочая папка после
  прогона чистая (`git status --short` пуст).
- **Scenario «Declined split proceeds with ordinary Shape»** — **не
  проверен живым прогоном**. Харнесс `eval-roles` не умеет подсадить
  фикстуре чужой предыдущий ответ человека (`design.md`, «Non-Goals» и
  «Risks» — известное ограничение, названное ещё на этапе дизайна, не
  находка верификации). Реализация есть (`role.md:47-50`: «отклоняет...→
  решение человека перекрывает эвристику... продолжай с шага 1»), логика
  симметрична подтверждённой ветке и использует тот же код-путь (обычный
  шаг 1 протокола, ничего специфичного не добавлено), но эмпирического
  подтверждения именно этой ветки в этом заходе нет. Указываю как открытое
  ограничение, не как невыполненное требование: сама спека и её сценарий
  реализованы текстом `role.md`, только не проверены живьём.

## Согласованность

Реализация соответствует решениям `design.md`:

- **`split` маршрутизируется как `needs_human`, не как `done`** —
  `workflow.yaml:63,94,120` (все три роли, `{to: Blocked, human: true}`),
  ровно то решение, что зафиксировано в design.md «Decisions» после находки
  про опасный маршрут `done` по умолчанию в `Ready`.
- **`implementer`/`reviewer` получили маршрут `split` только ради
  `Workflow.Validate()`** — их `role.md` не упоминает `split` вовсе
  (проверено `grep`, чисто).
- **Резюме использует `split`, а не `done`/`next_owner: none`** —
  design.md явно отверг `by_next_owner.none` (не проходит валидацию, `none`
  не является ролью графа); реализация (`role.md:44-46`) использует `split`.
- Golden-кейс и харнесс обновлены тем же способом, что описан в proposal.md
  «Impact»: `knownOutcome` и `outcomeChecker.Run` расширены, а не продублирован
  новый check kind — прохождение `outcome`-проверки уже свидетельствует о
  валидности `split.children`, раз контракт не пропустит невалидный вывод.

**Находки, вышедшие за исходный `proposal.md`, все закрыты и
задокументированы по ходу build** (не расхождение план/факт, а честно
обновлённый план — `proposal.md` дописан теми же коммитами, что вносили
код): `internal/tracker/config.go` (пакетный список `outcomes`),
`internal/runner/agentio.go` `ResultSpec()` (системный промпт агента),
инлайн-фикстуры `workflow.yaml` в `internal/tracker/config_test.go` и
`internal/pipeline/pipeline_test.go`. Все проверены свежим `go test ./...`
в этом сообщении.

## Итог

Критических проблем нет. Один явный, осознанно принятый пробел (decline-
сценарий не проверен живьём — ограничение харнесса, не дефект реализации).
Готово к архивации.
