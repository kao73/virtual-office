## Why

Ни одна роль (`analyst`/`implementer`/`reviewer`) сегодня не видит вложений
своего тикета: `tracker.Task` не имеет такого поля вообще, а
`jira.toTask` молча выбрасывает `fields["attachment"]` из ответа JIRA.
Обнаружено при разборе живого прогона `EXP-15→EXP-16..20`
(`docs/notes/analyst-task-splitting.md`) вместе со смежной дырой: даже
после того как дети разбиения стали наследовать дословный текст описания
родителя (`internal/pipeline/splits.go`, `childDescription`, коммит
`a3a504c`), человеческое вложение к родителю (схема, спецификация,
диаграмма) детям не передаётся никак — то же самое исчезновение контекста,
только для другого носителя.

## What Changes

- `tracker.Task` получает поле `Attachments []AttachmentRef` (id + имя).
  `jira.toTask` перестаёт выбрасывать `fields["attachment"]`; `mock`
  учится хранить имя вложения (сегодня `AddAttachment` его отбрасывает) —
  без этого `Get()` не может ответить, как вложение называется.
- Раннер материализует человеческие (не служебные) вложения тикета
  настоящими файлами в рабочую папку агента (`.agent/attachments/`), а не
  текстом внутри `task.md` — среди вложений бывают картинки и PDF.
  `task.md` получает одну строку-упоминание, что приложено и где искать.
- Служебное вложение `split.json` (переписка раннера с самим собой)
  исключается из того, что видит агент, — общий фильтр
  (`Office.humanAttachments`), а не отдельная проверка в каждом месте.
- `internal/pipeline/splits.go`: подтверждённый split теперь докатывает
  человеческие вложения родителя на каждого созданного ребёнка —
  идемпотентно, на **каждом** проходе `CompleteSplits`, а не только при
  первом создании: прогон, прерванный между «ребёнок создан» и «вложения
  скопированы», чинится следующим тиком сам.

## Capabilities

### New Capabilities

- `ticket-attachment-visibility`: тикет-вложения становятся видны роли,
  работающей с задачей, — как настоящие файлы в рабочей папке агента, а не
  только записью в трекере.

### Modified Capabilities

- `pipeline-split-autocreate`: подтверждённый split, помимо тикетов-детей
  и связей `depends_on`, теперь докатывает и человеческие вложения
  родителя на каждого ребёнка — идемпотентно, тем же проходом
  `CompleteSplits`.

## Impact

`internal/tracker/tracker.go` (модель `Task`, тип `AttachmentRef`),
`internal/tracker/jira/jira.go` (`toTask`), `internal/tracker/mock/mock.go`
(хранение имени вложения), `internal/runner/agentio.go`
(`SplitAttachmentName`), `internal/runner/input.go` (`InputAttachment`,
`PrepareInput` → `.agent/attachments/`), `internal/pipeline/pipeline.go`
(`humanAttachments`, `fetchAttachments`, `taskBody`), `internal/pipeline/
splits.go` (`ensureChildAttachments`). Обратной несовместимости нет: новое
поле `Task.Attachments` расширяет модель, не меняя существующих полей.
