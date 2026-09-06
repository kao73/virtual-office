---
comet_change: ticket-attachment-visibility
role: technical-design
canonical_spec: openspec
archived-with: 2026-09-06-ticket-attachment-visibility
status: final
---

# Видимость вложений тикета — глубокий технический дизайн

Высокоуровневое решение и альтернативы — в `docs/openspec/changes/
ticket-attachment-visibility/design.md` (Context/Goals-NonGoals/Decisions/
Risks-Trade-offs/Migration Plan). Этот документ углубляет его до уровня
конкретных сигнатур, порядка вызовов и тестовой стратегии; он не
переписывает уже принятые решения заново.

## Поток данных

```
jira.Get()/mock.Get()
  → tracker.Task{..., Attachments: []tracker.AttachmentRef{ID, Name}}
  → Office.humanAttachments(task)              // фильтр split.json и подобных
      ├─→ taskBody(task)                       // строка-упоминание в task.md
      └─→ Office.fetchAttachments(task)         // скачивание байт
            → runner.Input.Attachments
              → runner.PrepareInput → .agent/attachments/<санитизированное имя>
```

Для split-детей — отдельный, не связанный с обычным прогоном путь:

```
Office.completeSplit(parent, attachmentID)
  → ensureChildren(parent, children) → map[childID]childKey
  → ensureChildAttachments(parent, keys)        // НОВОЕ, между ensureChildren и linkChildren
      для каждого childKey:
        child := Tracker.Get(childKey)          // текущее состояние — не кеш
        has := {имена уже присутствующих вложений child}
        для каждого parentAttachment из humanAttachments(parent):
          если parentAttachment.Name не в has:
            data := Tracker.GetAttachment(parent.Key, parentAttachment.ID)
            Tracker.AddAttachment(childKey, BySystem(), parentAttachment.Name, data)
  → linkChildren(children, keys)
```

Порядок `ensureChildAttachments` **до** `linkChildren` не принципиален
(разные тикеты, разные вызовы) — сохранён порядок, в котором задачи
перечислены в `tasks.md`, для читаемости диффа.

## Конкретные сигнатуры

Уже реализовано в коде этой ветки (не проект, а состояние на момент
письма этого документа):

- `tracker.AttachmentRef{ID, Name string}`, `tracker.Task.Attachments
  []AttachmentRef` — `internal/tracker/tracker.go`.
- `jira.toTask` — блок `if attachments, ok :=
  fields["attachment"].([]any)` — `internal/tracker/jira/jira.go`.
- `mock.attachmentHead{Name string}`, `mock.attachmentFiles(dir)`,
  `mock.readAttachments(dir)` — `internal/tracker/mock/mock.go`.
- `runner.SplitAttachmentName = "split.json"` — `internal/runner/
  agentio.go`.
- `runner.InputAttachment{Name, Data}`, `Input.Attachments`,
  `writeAttachments(agentDir, attachments)` — `internal/runner/input.go`.
- `pipeline.humanAttachments(task) []tracker.AttachmentRef`,
  `Office.fetchAttachments(task) ([]runner.InputAttachment, error)` —
  `internal/pipeline/pipeline.go`.
- `Office.ensureChildAttachments(task, keys map[string]string) error` —
  `internal/pipeline/splits.go`, вызывается из `completeSplit`.

## Граничные случаи и их обработка

| Случай | Обработка |
|---|---|
| Имя вложения `..`, `.`, пусто, голый разделитель путей | заменяется на `"attachment"` (`writeAttachments`) |
| Имя вложения содержит `../../etc/passwd` | `filepath.Base` обрезает до `passwd` |
| Два вложения одного прогона с одинаковым именем | второму и далее — числовой префикс (`2-schema.png`) |
| Родитель без человеческих вложений | `ensureChildAttachments` возвращает `nil` сразу, ни одного `Get()` не тратится |
| `split.json` среди вложений родителя | не входит в `humanAttachments`, не наследуется, не материализуется |
| Прогон прерван между «ребёнок создан» и «вложения скопированы» | следующий проход `CompleteSplits` находит ребёнка по метке (как и раньше), но **дополнительно** перечитывает его вложения и докатывает недостающее — не полагается на то, что «ребёнок найден» = «ему уже ничего не нужно» |
| Вложение уже есть у ребёнка (повторный проход) | сверка по имени (`has[parentAttachment.Name]`) пропускает повторную заливку |
| Два вложения родителя с одинаковым именем | докатится только одно (см. design.md, Risks) — не различить по имени, принято как упрощение |

## Тестовая стратегия (по слоям)

1. **mock**: `TestAddAttachmentPreservesNameInGet` — имя переживает
   `AddAttachment` → `Get()`.
2. **jira**: `TestGetMapsAttachments` — `fields["attachment"]` не
   выбрасывается, id/filename сохранены.
3. **runner/input**: `TestPrepareInputWritesAttachments`,
   `TestPrepareInputSanitizesAttachmentName`,
   `TestPrepareInputDisambiguatesDuplicateAttachmentNames`,
   `TestPrepareInputClearsStaleAttachments` — уже написаны и зелёные.
4. **pipeline** (задача 3.3, ещё не написана): обычный прогон
   материализует человеческое вложение задачи и НЕ материализует
   `split.json`.
5. **splits** (задачи 4.2–4.4, ещё не написаны):
   - подтверждённый split копирует вложения родителя на всех детей;
   - ребёнок, созданный прошлым прерванным прогоном без вложений,
     получает их следующим проходом, без дублирования уже присутствующих
     у другого ребёнка;
   - `split.json` не оказывается среди вложений ни одного ребёнка.

## Что дальше

Задачи `tasks.md`, группы 3–6: тест 3.3, тесты 4.2–4.4, документация
(раздел в `docs/notes/analyst-task-splitting.md`, память), финальный
`go build`/`vet`/`test` и коммит. Design Doc фиксирует технический
подход — исполнение продолжается в фазе Build того же изменения.
