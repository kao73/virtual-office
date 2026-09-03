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

- [ ] 3.1 `evals/analyst/escalation-no-false-split/fixture/` — минимальная
      плейсхолдер-фикстура, как в группе 2.
- [ ] 3.2 `evals/analyst/escalation-no-false-split/task.md` — постановка того
      же вида, что реальный `Category`-тикет из `stage-5-live-backlog.md`:
      одна сущность, несколько мелких, но безопасных решений (модель,
      миграция, CRUD create/list/get/update).
- [ ] 3.3 `evals/analyst/escalation-no-false-split/expect.yaml` —
      `role: analyst`; проверки: `outcome` (`expect: done`,
      `next_owner: implementer`); `diff_scope` (`allow` включает
      `docs/comet/changes/**`, `.comet/config.yaml`,
      `.comet/current-change.json` — как в `capability-basic-plan`);
      `fixture_tests`, подтверждающий, что `brief.md` и `spec.md` реально
      созданы (обычный Shape прошёл целиком, ранняя проверка его не
      подрезала).

## 4. Проверка

- [ ] 4.1 `./bin/eval-roles --role analyst --case escalation-oversized-split`
      — кейс проходит.
- [ ] 4.2 `./bin/eval-roles --role analyst --case escalation-no-false-split`
      — кейс проходит.
- [ ] 4.3 `./bin/eval-roles --role analyst` — весь набор кейсов роли
      (включая `escalation-ambiguous-decision`, `capability-basic-plan`,
      `capability-resume-no-reinvoke`) проходит без регрессий от правки
      `role.md`.
