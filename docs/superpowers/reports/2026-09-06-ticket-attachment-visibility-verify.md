# Verification Report: ticket-attachment-visibility

Режим: full (17 задач, 2 delta-spec capability, 21 изменённый файл — все три порога превышены).

## Summary

| Dimension    | Status |
|--------------|--------|
| Completeness | 17/17 задач `tasks.md`, 4/4 требования обеих delta-спек |
| Correctness  | 4/4 требования сопоставлены с кодом и тестами, все сценарии зелёные |
| Coherence    | Решения `design.md`/Design Doc соблюдены, расхождений не найдено |

## Completeness

**Задачи (`tasks.md`)**: все 6 групп, 17 пунктов — `[x]`. Проверено чтением файла целиком, не только грепом по чекбоксам.

**Покрытие спек**:

| Требование | Спека | Реализация | Тест |
|---|---|---|---|
| Рабочая папка несёт человеческие вложения настоящими файлами | `ticket-attachment-visibility` | `internal/pipeline/pipeline.go:fetchAttachments`+`Office.work()`; `internal/runner/input.go:writeAttachments` | `TestTickMaterializesHumanAttachmentsButNotSplitJSON` (pipeline_test.go), `TestPrepareInputWritesAttachments` (input_test.go) |
| Служебные вложения раннера никогда не видны агенту | `ticket-attachment-visibility` | `internal/runner/agentio.go:SplitAttachmentName`; `internal/pipeline/pipeline.go:humanAttachments` | `TestTickMaterializesHumanAttachmentsButNotSplitJSON` (исключение из файлов и из `task.md`) |
| Имя файла вложения безопасно и не коллизирует | `ticket-attachment-visibility` | `internal/runner/input.go:ResolveAttachmentNames` | `TestPrepareInputSanitizesAttachmentName`, `TestPrepareInputDisambiguatesDuplicateAttachmentNames`, `TestResolveAttachmentNamesHandlesNameCollidingWithGeneratedName` |
| Подтверждённый split копирует вложения родителя на детей, идемпотентно | `pipeline-split-autocreate` | `internal/pipeline/splits.go:ensureChildAttachments`, подключено в `completeSplit` | `TestCompleteSplitsCopiesParentAttachmentsToChildren`, `TestCompleteSplitsBackfillsAttachmentsOnAlreadyCreatedChild` (докатка + отсутствие дублирования), `TestCompleteSplitsDoesNotInheritSplitJSON` |

Все перечисленные тесты запущены заново непосредственно перед этим отчётом (`go test ./internal/... -run "Attachment|SplitJSON" -v`) — зелёные. Полный `go build ./... && go vet ./... && go test ./...` — зелёный (зафиксировано `comet state record-check` на фазе Build, перепрогнан здесь же для этого отчёта).

## Correctness

Три сценария `ticket-attachment-visibility` и три сценария `pipeline-split-autocreate` (см. `specs/*/spec.md`) сопоставлены с кодом построчно — таблица выше. Расхождений между текстом сценария и поведением кода не найдено.

**Отдельно проверены фиксы ревью build-фазы** (коммит `777a46d`) — заявлены как сделанные, подтверждено чтением кода:
- `writeAttachments` больше не хранит собственную логику разрешения коллизий — вызывает `ResolveAttachmentNames`, которая проверяет уже ЗАНЯТЫЕ итоговые имена (`taken map[string]bool`), а не счётчик повторов исходного — critical-находка (тихая потеря вложения при коллизии с уже сгенерированным именем) закрыта, воспроизведена и подтверждена тестом `TestResolveAttachmentNamesHandlesNameCollidingWithGeneratedName`.
- `taskBody` теперь тоже зовёт `runner.ResolveAttachmentNames` на том же списке имён — упоминание в `task.md` совпадает с реально записанными на диск именами (important-находка закрыта).
- `ensureChildAttachments` скачивает байты каждого вложения родителя один раз (`map[string][]byte`) до цикла по детям, а не по разу на ребёнка (minor-находка закрыта).
- Четвёртая (minor) находка — постоянно неудачная заливка вложения блокирует `linkChildren`/`closeSplitParent` навсегда — принята как задокументированный риск (`design.md`, Risks/Trade-offs): то же свойство уже было у `linkChildren`/`closeSplitParent` и раньше этой правки, не новый регресс.

## Coherence

Ключевые решения `docs/openspec/changes/ticket-attachment-visibility/design.md` и глубокого Design Doc (`docs/superpowers/specs/2026-09-06-ticket-attachment-visibility-design.md`) соблюдены:
- `Task.Attachments` заполняется из уже идущего `Get()`, без нового запроса — да (`jira.go:toTask`, `mock.go:readAttachments`).
- Единый фильтр `humanAttachments` в трёх местах (упоминание, материализация своей задачи, наследование детьми) — да, все три вызова используют одну и ту же функцию.
- Строгая идемпотентность наследования (перечитывание на каждом проходе `CompleteSplits`, не только при создании) — да, `ensureChildAttachments` вызывается безусловно для всех `keys`, не только для только что созданных.
- Санитация пути через `filepath.Base` + явная проверка `.`/`..`/пустоты/разделителя — да, теперь в `ResolveAttachmentNames`, используемой обоими потребителями.

Design Doc локализуем (`docs/superpowers/specs/2026-09-06-ticket-attachment-visibility-design.md` существует, путь записан в `.comet.yaml design_doc`). Противоречий между delta-спеками и Design Doc не найдено.

## Issues

Нет CRITICAL, WARNING или SUGGESTION находок, оставшихся открытыми на момент этого отчёта — все находки build-фазового ревью закрыты (три) или приняты как задокументированный риск (одна, minor).

## Final Assessment

All checks passed. Ready for archive.
