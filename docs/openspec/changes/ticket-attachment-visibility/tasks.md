## 1. Модель данных: `Task.Attachments`

- [x] 1.1 `tracker.AttachmentRef{ID, Name}` и поле `Task.Attachments` в `internal/tracker/tracker.go`
- [x] 1.2 `jira.toTask` разбирает `fields["attachment"]`, тест `TestGetMapsAttachments`
- [x] 1.3 `mock.AddAttachment` хранит имя вложения (сайдкар `<id>.yaml`), `Get()` его читает, тест `TestAddAttachmentPreservesNameInGet`

## 2. Материализация вложений в рабочую папку агента

- [x] 2.1 `runner.SplitAttachmentName` в `internal/runner/agentio.go`, использован при создании `split.json` (`pipeline.go`)
- [x] 2.2 `runner.InputAttachment`, `Input.Attachments`, `writeAttachments` в `internal/runner/input.go` — санитация имени (`filepath.Base` + явная проверка `.`/`..`), разрешение коллизий счётчиком-префиксом, очистка устаревших вложений прошлого прогона
- [x] 2.3 Тесты `input_test.go`: запись вложений, санитация пути, коллизия имён, очистка устаревших

## 3. Подключение в конвейер

- [x] 3.1 `Office.humanAttachments` (фильтр служебных вложений) и `Office.fetchAttachments` в `internal/pipeline/pipeline.go`
- [x] 3.2 `taskBody` упоминает вложения строкой; `Office.work()` скачивает и передаёт их в `runner.Input`
- [x] 3.3 Тест на уровне `pipeline`, что обычный прогон материализует человеческие вложения задачи в рабочую папку, а служебные (`split.json`) — нет

## 4. Наследование вложений детьми разбиения

- [x] 4.1 `Office.ensureChildAttachments` в `internal/pipeline/splits.go`, подключено в `completeSplit` между `ensureChildren` и `linkChildren`
- [x] 4.2 Тест: подтверждённый split копирует человеческие вложения родителя на каждого созданного ребёнка
- [x] 4.3 Тест: докатка вложений на уже существующего (созданного прошлым прерванным прогоном) ребёнка, без дублирования уже присутствующих
- [x] 4.4 Тест: `split.json` не наследуется детьми

## 5. Документация

- [x] 5.1 Раздел в `docs/notes/analyst-task-splitting.md` о новой находке и её закрытии, со ссылкой на этот Comet-change
- [x] 5.2 Обновить память (`project_analyst_task_splitting_wave2_change1.md` или новую) при завершении

## 6. Финальная проверка

- [x] 6.1 `go build ./...`, `go vet ./...`, `go test ./...` — всё зелёное
- [x] 6.2 Коммит(ы) в ветку `comet/ticket-attachment-visibility`
