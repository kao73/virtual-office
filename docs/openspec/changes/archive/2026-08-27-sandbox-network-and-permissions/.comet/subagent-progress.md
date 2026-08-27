Change: sandbox-network-and-permissions
Plan: docs/superpowers/plans/2026-08-27-sandbox-network-and-permissions.md
review_mode: standard | build_mode: subagent-driven-development

Completed: Tasks 1-15 — все фазы A-F, кроме 16. tasks.md 21/22 (только 6.3 осталось).
Задача 15 — центральная живая проверка — пройдена: defaults.network/tools.deny
реально работают на живом прогоне EXP-2 (см. docs/notes/stage-5-live-backlog.md).

Текущая задача плана: Задача 16 — "Живой прогон на sbx для проекта без своей специфики"
Соответствие tasks.md: 6.3
Модель: sonnet (живой ранбук, суждение по логам)
BASE: 3d9eb84 (репозиторий не меняется этой задачей, кроме будущего коммита находок)

Разведка координатора перед диспетчем:
- VO не существует как проект в этом инстансе Jira — непригоден.
- OFFICE использует tracker: mock (файловый, локальный, без Jira-кредов) —
  выбран для этой задачи. Каталог mock-хранилища ($OFFICE_HOME/mock) пока
  не существует — готовой задачи Ready для OFFICE нет, придётся создать
  синтетическую (формат task.yaml: project/summary/description/status/
  labels/owner/attempts/human_flag — tracker/mock/mock.go:418-425).
  Валидные статусы (workflow.yaml): Backlog, Analysis, Ready, InProgress,
  Review, Approved, Done, Blocked. implementer читает из Ready.
- Репозиторий OFFICE — тот же бэйр-репозиторий office-polygons/client.git,
  что и у VO (не GitHub, локальный) — ниже стоимость/риск, чем у Задачи 15.
- projects.yaml: OFFICE не имеет собственных network/tools — только
  default_branch/branch_prefix, значит должен унаследовать РОВНО repo-wide
  defaults, ничего специфичного для EXP.
