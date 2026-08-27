---
change: sandbox-network-and-permissions
verify_mode: full
verified_at: 2026-08-27
---

# Отчёт верификации: sandbox-network-and-permissions

## Summary

| Dimension    | Status |
|---|---|
| Completeness | 22/22 задач `tasks.md`, все 4 requirement'а delta spec покрыты реализацией и тестами |
| Correctness  | 4/4 requirement'а подтверждены юнит-тестами; 2 центральных (repo-wide union, tools.deny-граница) дополнительно подтверждены двумя живыми прогонами на реальном `sbx` |
| Coherence    | Реализация следует всем зафиксированным в `design.md` решениям; единственное отклонение от буквального текста `tasks.md` (4.2, adapter.go не меняется в части логики слияния) явно задокументировано и обосновано в самом `tasks.md`, `tracker/rules.go`, контракте |

## Completeness

- `tasks.md`: 22/22 отмечены `[x]` (подтверждено `openspec instructions apply --json`: `progress.total=22, complete=22, remaining=0`).
- План (`docs/superpowers/plans/2026-08-27-sandbox-network-and-permissions.md`, 16 задач) — все чекбоксы всех задач и всех шагов внутри задач отмечены.
- Delta spec (`specs/role-sandbox-permissions/spec.md`) — 4 ADDED-requirement'а, каждый с реализацией:
  - «Слоистое разрешение правил» → `tracker.MergeProjectRules` + `unionStrings` (`tracker/rules.go`, `tracker/config.go`).
  - «Repo-wide умолчания применяются ко всем проектам» → `extractDefaultsOffice`/`extractDefaultsMachine` + union в `LoadProjects`.
  - «Проектный слой не утекает в другие проекты» → тот же union-механизм, изоляция по ключу проекта.
  - «tools.deny — граница, которую нельзя ослабить слиянием» → union-only семантика `unionStrings`, нет ветки удаления.
  - «Машинный слой дополняет проектный» → `extractDefaultsMachine` симметрично office-половине.

## Correctness

Requirement → Scenario → реализация → тест/живое подтверждение:

1. «Роль без собственных правил наследует итог предыдущих слоёв» — `TestLoadProjectsWithoutDefaultsAtAllIsUnaffected`, живьём: OFFICE-16 (задача 16 плана, `docs/notes/followup-network-and-permissions.md`) — сеть прогона равна ровно `defaults.network` (`pypi.org`+`files.pythonhosted.org`+домен адаптера), без утечки специфики `EXP`.
2. «Ролевой слой добавляет к унаследованному» — `TestMergeProjectRulesUnionsWithoutLoss`.
3. «Новый проект без собственных сетевых правил получает базовый список» — `TestLoadProjectsProjectInheritsOnlyDefaults`; живьём: EXP-2 (задача 15 плана) — `sbx policy log` показал чистый ALLOW на всех трёх хостах Docker Hub из `defaults.network`, ни одного BLOCK.
4. «Специфика одного проекта не видна другому» — `TestLoadProjectsProjectSpecificsAreIsolated`.
5. «Ролевой allow не снимает repo-wide deny» — `TestShippedDefaultsCarrySevenCommonDenyRules`, `TestMergeProjectRulesDedupsOverlap`; живьём: EXP-2 — составная Bash-команда с `git branch -a` реально заблокирована `defaults.tools.deny`'s `Bash(git *branch*)`.
6. «Машина добавляет внутренний хост поверх проектных правил» — `TestLoadProjectsAllowsDefaultsInEitherOrBothFiles` (обе половины/одна половина/ни одной).

Два самых нагруженных по риску requirement'а (repo-wide union и tools.deny-граница) подтверждены не только юнит-тестами, но и двумя независимыми живыми прогонами на реальном `sbx` — это выше стандартной планки для этого проекта.

## Coherence

- `design.md`'s Migration Plan (6 шагов) реализован в точности в этом порядке (Tasks 1-2 → Task 3 → Task 4-9 → Task 10-12 → Task 13), каждый шаг соответствует своей группе задач `tasks.md`.
- Ключевые решения design.md выполнены дословно: union-only семантика (не override) — «Альтернатива — полноценный override — отклонена»; `defaults` не проект — `extractDefaultsOffice`/`extractDefaultsMachine`; точка резолюции — «между `LoadProjects`/`LoadRole` и адаптером» → `pipeline.tickRole` (после `claim()`) и `run-agent`'s `-project` флаг, адаптер не меняется в части логики.
- Единственное явное отклонение от буквального текста `tasks.md` 4.2 («Передать объединённый результат в adapters/claude/adapter.go: networkAllow перестаёт быть единственным источником...») — адаптер НЕ меняется в части логики слияния; вместо этого слияние происходит выше по стеку, и адаптер получает уже смёрженную роль. Это прямо предусмотрено самим `design.md` («Место резолюции слоёв — между LoadProjects/LoadRole и адаптером») и явно записано как отклонение в `tasks.md`, `tracker/rules.go`, контракте — не случайная поломка, а зафиксированное архитектурное решение.
- Финальный обзор всей ветки (агент на модели, отвечающей за максимальную дотошность) не нашёл ни одной Critical проблемы; 5 Important и 8 Minor были обработаны единственным допустимым fix wave (коммит `48d43cd`) и подтверждены точечным повторным ревью (коммит после — все находки ADDRESSED, новых блокирующих проблем нет).

## Известные, осознанно отложенные находки (не блокируют архивацию)

- **I1** (валидация `network`/`tools` на смёрженных уровнях 1-3 — `runner.validHost` защищает только уровень роли) — зафиксирована как явный follow-up в `docs/notes/followup-network-and-permissions.md` (коммит `9e2f28e`), требует отдельной задачи с полным TDD+review циклом, не рушится молча в текущем виде (риск — авторская опечатка в `projects.yaml`/`projects.local.yaml`, редактируемых доверенными операторами, не агентом).
- Несколько Minor-наблюдений scoped re-review (неточная атрибуция механизма скоупинга Write в одном комментарии, отсутствие квалификатора «для Bash» в одной фразе README, временной снимок конфига в одном сценарии delta spec) — чисто формулировочные, не влияют на поведение.

## Итог

**Все проверки пройдены. Готово к архивации.**
