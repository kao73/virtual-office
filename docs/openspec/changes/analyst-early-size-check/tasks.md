## 1. Правка role.md

- [x] 1.1 Вставить в `roles/analyst/role.md` новый подраздел в конце преамбулы
      раздела «Comet Native: фаза Shape», перед нумерованным шагом 1: дешёвая
      оценка размера постановки до входа в протокол (без вызова
      `comet native new`, если решено резать) и явный запрет заводить
      `children.yaml` (Supervisor Change) — тем же голосом, что уже держит
      запрет загружать скилл `comet` напрямую в этом файле.

## 2. Golden-кейс `escalation-oversized-split` (позитивный)

- [x] 2.1 `evals/analyst/escalation-oversized-split/fixture/` — минимальная
      плейсхолдер-фикстура по образцу
      `evals/analyst/escalation-ambiguous-decision/fixture/` (`go.mod` +
      `README.md`, ничего не намекает на конкретное решение).
- [x] 2.2 `evals/analyst/escalation-oversized-split/task.md` — постановка,
      описывающая несколько независимых сущностей (по образцу
      Category+Transaction+Budget из `docs/notes/stage-5-live-backlog.md`).
- [x] 2.3 `evals/analyst/escalation-oversized-split/expect.yaml` —
      `role: analyst`; проверки: `outcome` (`expect: needs_human`,
      `questions_not_empty: true`, `next_owner: human`); `diff_scope`
      (`allow: []` — `comet native new` не должен быть вызван, значит
      `.comet/config.yaml`/`.comet/current-change.json` не должны появиться в
      диффе); `fixture_tests`, явно проверяющий отсутствие любого
      `children.yaml` в закоммиченном дереве.

## 3. Golden-кейс `escalation-no-false-split` (негативный)

- [x] 3.1 `evals/analyst/escalation-no-false-split/fixture/` — минимальная
      плейсхолдер-фикстура, как в группе 2.
- [x] 3.2 `evals/analyst/escalation-no-false-split/task.md` — постановка того
      же вида, что реальный `Category`-тикет из `stage-5-live-backlog.md`:
      одна сущность, несколько мелких, но безопасных решений (модель,
      миграция, CRUD create/list/get/update).
- [x] 3.3 `evals/analyst/escalation-no-false-split/expect.yaml` —
      `role: analyst`; проверки: `outcome` (`expect: done`,
      `next_owner: implementer`); `diff_scope` (`allow` включает
      `docs/comet/changes/**`, `.comet/config.yaml`,
      `.comet/current-change.json` — как в `capability-basic-plan`);
      `fixture_tests`, подтверждающий, что `brief.md` и `spec.md` реально
      созданы (обычный Shape прошёл целиком, ранняя проверка его не
      подрезала).

## 4. Проверка

- [x] 4.1 `./bin/eval-roles --role analyst --case escalation-oversized-split`
      — кейс проходит.
- [x] 4.2 `./bin/eval-roles --role analyst --case escalation-no-false-split --clone`
      — кейс проходит.

      **Решено (2026-09-04), после паузы и отдельного расследования.** Три
      прогона без `--clone` подряд дали три разных исхода: `errored`
      (result.json не появился), `done` (сработало, но `comet native
      new`/`next --confirmed` в логе аналитика падали кодом 73 «Native lock
      coordinator ownership changed» при фактически успешном продвижении
      состояния), `blocked` (тот же конфликт координатора блокировок, но на
      этот раз реально не дал завести изменение). Причина — нестабильность
      Comet Native lock coordinator конкретно под обычным bind-mount
      sbx-песочницы, задокументированная в самом репозитории
      (`internal/runner/input.go` `ClearStaleCometLocks`,
      `internal/backends/sbx/clone.go`, `cmd/eval-roles/fixture.go`) как
      причина прошлого инцидента EXP-2 — штатный обход уже существует, это
      флаг `--clone` (агент работает на клоне внутри песочницы, а не на
      bind-mount). Апстрим `@rpamis/comet` (включая текущий HEAD, не только
      наш `beta.20`) не даёт настроить или отключить сам координатор —
      ни флага, ни `env`.

      Первая попытка с `--clone` тоже не дала чистого результата — но по
      другой причине: `--clone` синхронизирует работу агента обратно на хост
      отдельным шагом со своим 5-минутным пределом
      (`cloneSyncTimeout`, `internal/backends/sbx/clone.go`) поверх времени
      самого агента (до 30 минут по `context.md`), а мой Bash-вызов был с
      таймаутом 10 минут на весь прогон целиком — сам процесс убило раньше,
      чем успел закрыться штатный шаг синхронизации. `.agent/run.log`
      подтвердил: агент реально дописал `result.json` и закоммитил Shape
      внутри песочницы. Повтор в фоне (`run_in_background`, без потолка в 10
      минут) прошёл чисто с первого раза.

      Фикстура/`task.md`/`expect.yaml` кейса не менялись — они были
      спроектированы верно с самого начала, найденное относится
      исключительно к вызову харнесса. См. память
      `reference_comet_native_lock_coordinator_flakiness.md`.
- [ ] 4.3 `./bin/eval-roles --role analyst` — весь набор кейсов роли
      (включая `escalation-ambiguous-decision`, `capability-basic-plan`,
      `capability-resume-no-reinvoke`) проходит без регрессий от правки
      `role.md`.

      Прогнать с `--clone` в фоне, тем же приёмом, что и 4.2 — по той же
      причине (`capability-basic-plan` тоже вызывает `comet native new`).
