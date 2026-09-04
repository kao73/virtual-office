# Отчёт верификации: analyst-early-size-check

Comet Classic tweak, полный режим верификации (delta-спека затрагивает одну
капабилити — `role-native-workflow`, порог full достигнут ещё и по числу
задач и файлов: `comet state scale` → 10 задач / 17 изменённых файлов / 1
капабилити, все три выше порога).

## Сводка

| Измерение | Статус |
|---|---|
| Полнота | 10/10 задач `tasks.md`; 2/2 требования спеки покрыты |
| Корректность | 2/2 требования реализованы, 3/3 сценария подтверждены живыми golden-прогонами |
| Согласованность | Реализация совпадает с решениями `design.md`; расхождений с delta-спекой нет |

## Полнота

**Задачи.** Все 10 пунктов `tasks.md` отмечены `[x]`, каждый коммитом
(`b64071f`… по `8134045`), последний коммит перед этим отчётом — `8134045`
(«4.3 done — full analyst suite passes with --clone, no regressions»).

**Покрытие требований спекой.** `specs/role-native-workflow/spec.md`
объявляет два `ADDED Requirements` — оба реализованы, ниже разобраны отдельно.

## Корректность

### Requirement: Task-size decision precedes Comet Native entry

Реализовано в `roles/analyst/role.md:38-45` (свежепрочитано только что) —
новый абзац сразу после преамбулы Comet Native и **перед** нумерованным
шагом 1, который вызывает `comet native new`. Текст прямо предписывает
прочитать `.agent/task.md`, не вызывая `comet native new`, и уйти в
`needs_human`, если постановка описывает несколько независимых сущностей.

- **Scenario «Oversized постановка is split before Comet Native starts»** —
  подтверждено живым прогоном `evals/analyst/escalation-oversized-split`
  (`PASS`, только что, `./bin/eval-roles --role analyst --clone`):
  `outcome: needs_human`, `diff_scope: allow: []` (значит `comet native new`
  действительно не вызывался — иначе `.comet/config.yaml` появился бы в
  диффе и провалил бы пустой allow-лист).
- **Scenario «Appropriately-scoped постановка proceeds normally»** —
  подтверждено `evals/analyst/escalation-no-false-split` (`PASS`):
  `outcome: done`, `next_owner: implementer`, `brief.md`/`spec.md` реально
  созданы — обычный Shape прошёл целиком, ранняя проверка не подрезала.

Заодно скорректирована стоявшая рядом формулировка исхода «задача крупная»
в разделе «Исходы» (`role.md:163-167`) — убрана ссылка на «разбиение в
спеке» (на момент решения спеки ещё нет), добавлена перекрёстная ссылка на
новый абзац. Это не отдельное требование спеки, а необходимое следствие
первого — старая формулировка стала бы противоречить новому поведению.

### Requirement: Comet Native Supervisor Change is never used

Реализовано в `roles/analyst/role.md:47-53` — явный, безусловный запрет
заводить `children.yaml`, с объяснением (совпадает по объёму ответственности
с `implementer`, минует учёт офиса).

- **Scenario «A Supervisor-Change-shaped постановка does not produce
  children.yaml»** — подтверждено тем же `escalation-oversized-split`:
  отдельный `fixture_tests`-чек грепает весь закоммиченный `git ls-tree`
  на `children.yaml$` и требует его отсутствия — прошёл.

### Регрессия по остальным кейсам роли

`./bin/eval-roles --role analyst --clone` (полный набор, 5 кейсов) — все
`PASS`: `capability-basic-plan`, `capability-resume-no-reinvoke`,
`escalation-ambiguous-decision` (существовавшие до этого tweak, не
затронуты правкой) плюс два новых. `go build ./...` — `exit 0`.

## Согласованность

Расхождений между `design.md` и фактической реализацией нет:

- место правки в `role.md` — «в конец преамбулы... перед шагом 1» — так и
  сделано, тем же голосом, что и существующий запрет `comet` напрямую;
- golden-кейсы используют исключительно существующие типы проверок харнеса
  (`outcome`/`diff_scope`/`fixture_tests`) — нового кода в `cmd/eval-roles`
  не добавлено;
- названия кейсов начинаются с `escalation-`, как и было решено.

`proposal.md`'s Non-Goals соблюдены: исход `split`, `workflow.yaml`,
`docs/contracts/agent-io.md`, `role.md` `implementer`/`reviewer` — не
тронуты (подтверждено `git diff --stat master...HEAD`: только `role.md` и
две новые директории `evals/analyst/escalation-*`).

## Находка вне объёма (не блокирует эту верификацию)

В процессе задачи 4.2 обнаружена и решена нестабильность Comet Native lock
coordinator под sbx bind-mount (не связана с содержимым этого изменения) —
задокументирована в `tasks.md` 4.2 и в памяти
`reference_comet_native_lock_coordinator_flakiness`. Открытый вопрос на
будущее: стоит ли `--clone` сделать умолчанием для golden-кейсов/живых
прогонов, реально вызывающих `comet native new` — не решается в рамках
этого tweak.

## Итог

Критических и важных проблем нет. Готово к архивации.
