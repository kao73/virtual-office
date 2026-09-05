---
change: split-autocreate-tickets
design-doc: docs/superpowers/specs/2026-09-05-split-autocreate-tickets-design.md
base-ref: e908309ebad1f855c5506004aa903cf1cb5140a7
---

# split-autocreate-tickets Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Второе подряд подтверждение `outcome: split` от `analyst` на одном
тикете запускает детерминированное, идемпотентное авто-создание и связывание
тикетов-детей в трекере и переводит родителя в `Done` — без прогона агента на
сам шаг создания, для `mock` и `jira` одинаково.

**Architecture:** Новая узкая поверхность `Tracker` (`CreateTask`,
`FindByMarker`, `AddAttachment`/`GetAttachment`, `LinkDependsOn`) с паритетной
реализацией в `mock` и `jira`. Исход `split` (волна 1) дополняется: раннер
кладёт вложение с сырыми `split.children[]` и тегирует комментарий
`attachment:<id>`. Новый системный проход `CompleteSplits`
(`internal/pipeline`), по образцу уже существующего `Reap`: считает
предыдущие `outcome:split`-маркеры `analyst`'а на тикете (≥2 — подтверждено),
скачивает последнее вложение, досоздаёт недостающих детей (опрос трекера —
источник идемпотентности), связывает по `depends_on`, переводит родителя в
терминальный статус. Вызывается из `Loop` рядом с `Reap` и отдельной CLI-командой
`runner complete-splits` — единый код-путь для первой попытки и для повтора
после сбоя (синхронного вызова из `finish()` нет, ошибка любого шага уходит
системной записью и не прерывает обход остальных задач). `roles/analyst/role.md`
лишается двух нижних веток резюме-цикла, ставших мёртвым кодом.

**Tech Stack:** Go 1.26 (`go.mod`), стандартная библиотека
(`net/http`, `encoding/json`, `mime/multipart`), `gopkg.in/yaml.v3`. Тесты —
`go test`, файловый трекер `mock` как основной стенд для поведенческих
тестов `internal/pipeline`, `httptest.Server`-подделка (`fakeJira`) для
`internal/tracker/jira`. Golden-кейсы роли — `./bin/eval-roles`.

**Spec:**
- Design Doc (основной источник конкретики): `docs/superpowers/specs/2026-09-05-split-autocreate-tickets-design.md`
- Open-фазный дизайн (архитектурные решения и их обоснование):
  `docs/openspec/changes/split-autocreate-tickets/design.md`
- Proposal: `docs/openspec/changes/split-autocreate-tickets/proposal.md`
- Границы объёма (8 групп, 39 пунктов): `docs/openspec/changes/split-autocreate-tickets/tasks.md`
- Дельта-спеки: `docs/openspec/changes/split-autocreate-tickets/specs/pipeline-split-autocreate/spec.md`,
  `docs/openspec/changes/split-autocreate-tickets/specs/role-native-workflow/spec.md`
- Живая спека, синхронизируемая при архивации: `docs/openspec/specs/role-native-workflow/spec.md`

## Global Constraints

- Язык документов (комментарии, doc-комментарии, сообщения об ошибках,
  тексты в тикетах) — русский; идентификаторы, ключи YAML/JSON, имена
  функций и типов — английские (`CLAUDE.md`).
- Никакого LLM в коде `internal/pipeline`, `internal/tracker` — все решения
  по `workflow.yaml` и `result.json`, промпт ни на что не влияет (`docs/DESIGN.md` §2.1).
- Методы `Tracker`, меняющие задачу, принимают `Actor` и обязаны проверять
  право через `tracker.CheckOwner`; методы, ничего не меняющие, актора не
  требуют (существующее правило контракта, `internal/tracker/tracker.go`).
- Контракт `result.json` (`internal/runner/agentio.go`) не меняется: исход
  `split` и его валидация остаются как есть.
- `mock` — инструмент отладки конвейера, не имитация конкурентного трекера
  под нагрузкой: коллизии допустимо ронять ошибкой, а не разрешать молча
  (доккомментарий пакета `internal/tracker/mock`).
- JIRA REST — только v2 (`internal/tracker/jira` уже фиксирует это: v3 с ADF
  понимает только Cloud, целевая версия — Server 8.13).
- Каждый файл `X.go` в `internal/pipeline` держит тесты в соседнем `X_test.go`
  (`prpass.go`/`prpass_test.go`, `archive.go`/`archive_test.go`) — тот же
  приём для новых файлов этого плана.
- `go build ./...`, `go vet ./...`, `gofmt -l .` (пусто), `go test ./...` —
  обязаны быть зелёными на каждом коммите, где это применимо (родной
  workflow репозитория, не только финальная проверка).

---

## Найденные и исправленные неточности Design Doc

Design Doc — основной источник конкретики для этого плана, но при сверке с
реальным кодом нашлись два места, где его текст не совпадает с тем, что
реально в репозитории. План ниже уже учитывает исправления, а не повторяет
неточности:

1. **`Marker.Attachment` поля не существует.** Design Doc утверждает: «id
   вложения из тега последнего такого маркера (`attachment:<id>`, поле уже
   существует в марке́ре с волны 1)» — это неверно: в
   `internal/tracker/marker.go` у `Marker` сегодня есть только `RunID`,
   `Role`, `Outcome`, `Event`, `Next`, `ConfigSHA`. `ParseMarker` вдобавок
   строгий: неизвестный ключ в строке маркера делает всю строку невалидной
   (`case default: return Marker{}, false`). Без явного добавления поля
   `Attachment` и случая `"attachment"` в `ParseMarker` **любой** маркер с
   тегом `attachment:...` перестал бы разбираться вовсе. Задача 9 ниже
   заводит поле, парсинг и рендер с нуля.
2. **`splitConfirmed` не может быть неэкспортированной функцией пакета
   `tracker`, если её вызывает `internal/pipeline`.** Псевдокод Design Doc
   вызывает `splitConfirmed(task.Comments)` изнутри `CompleteSplits`
   (метод `*Office` в пакете `internal/pipeline`) — но описывает функцию как
   «новая функция в `internal/tracker/marker.go`». Неэкспортированное имя
   из другого пакета не видно; задача 9 заводит её как экспортированную
   `tracker.SplitConfirmed(comments []Comment, role string) (bool, string)`,
   и задача 11 вызывает её как `tracker.SplitConfirmed(task.Comments, "analyst")`.
   Параметр `role` добавлен для консистентности с остальными функциями
   `marker.go` (`LeaseExpiries`, `PushFailures`, `ReturnRounds` — ни одна не
   хардкодит имя роли), хотя единственный вызывающий код передаёт
   `"analyst"` буквально.

Дополнительно, Design Doc указывает считать `outcome:split`-маркеры «по
переписке», не уточняя механику. Прямая реализация «серией с конца» (как
`eventStreak`/`ReturnRounds`) здесь **не работает**: системная запись
`event:human-reply`, которую `unblock()` пишет между двумя split-маркерами,
подписана тем же `Role`, что и вопрос («роль, говорившая последней»), — и
серия, обрывающаяся на первой не-`split`-записи той же роли, никогда не
досчитала бы до двух. Задача 9 поэтому считает **все** `outcome:split`-маркеры
роли в истории целиком, не суффиксом с конца — с явным доккомментарием,
почему это осознанный выбор, а не недосмотр.

## File Structure

- `internal/tracker/tracker.go` — **modify**: тип `TaskInput`, 5 новых
  методов интерфейса `Tracker`, новое поле `Task.DependsOn`.
- `internal/tracker/mock/mock.go` — **modify**: `CreateTask`,
  `FindByMarker`, `AddAttachment`, `GetAttachment`, `LinkDependsOn`; новый
  подкаталог `attachments/`; рефакторинг `AddComment` (общий приём
  эксклюзивной нумерации, переиспользуемый вложениями).
- `internal/tracker/mock/mock_test.go` — **modify**: тесты на все пять методов.
- `internal/tracker/jira/jira.go` — **modify**: `Config.IssueType`,
  `Config.DependsOnLink`, те же 5 методов через REST v2; низкоуровневые
  `upload`/`download` для вложений (не JSON, `t.call` не годится).
- `internal/tracker/jira/jira_test.go` — **modify**: расширение `fakeJira`
  (создание, вложения, `issueLink`) + тесты.
- `tracker.example.yaml` — **modify**: примеры `issue_type`, `depends_on_link`.
- `internal/tracker/marker.go` — **modify**: поле `Marker.Attachment`,
  разбор/рендер, функция `SplitConfirmed`, события `EventSplitCreated`,
  `EventSplitCreateFailed`.
- `internal/tracker/marker_test.go` — **modify**: тесты на `Attachment` и
  `SplitConfirmed`.
- `internal/pipeline/pipeline.go` — **modify**: `finish()` кладёт вложение
  на исход `split` и тегирует маркер; `Loop` зовёт `CompleteSplits`.
- `internal/pipeline/pipeline_test.go` — **modify**: тест на вложение при
  `outcome: split`.
- `internal/pipeline/splits.go` — **create**: `CompleteSplits`,
  `completeSplit` и его подшаги — по образцу `prpass.go`.
- `internal/pipeline/splits_test.go` — **create**: поведенческие тесты
  `CompleteSplits` — по образцу `prpass_test.go`.
- `cmd/runner/office.go` — **modify**: `completeSplitsCommand`.
- `cmd/runner/main.go` — **modify**: подкоманда `complete-splits`, строка usage.
- `roles/analyst/role.md` — **modify**: убрать две нижние ветки резюме-цикла,
  поправить первую ветку и раздел «Исходы».
- `docs/notes/analyst-task-splitting.md` — **modify** (задача 18): запись
  находок живой проверки.

`internal/tracker/report.go` **не меняется**: `ReportBody`/`SplitBlock` уже
универсальны — они читают `Marker.String()`, и тег `attachment:<id>` появится
в комментарии сам собой, как только вызывающий код (`finish()`) заполнит
поле `Marker.Attachment` до вызова `ReportBody`. Формулировка в
`proposal.md`'s Impact («`internal/tracker/report.go` — вложение при исходе
split») закрывается изменениями в `marker.go`+`pipeline.go`, не в `report.go`.

---

## Task 1: Контракт `Tracker` — `TaskInput` и пять новых методов

Интерфейс `Tracker` (`internal/tracker/tracker.go`) — общий для `mock` и
`jira`; обе реализации проверяются на этапе компиляции строкой
`var _ tracker.Tracker = (*Tracker)(nil)`. Момент добавления методов в
интерфейс — единственный момент, когда обе реализации обязаны иметь **хотя
бы** заглушки, иначе пакеты `mock` и `jira` перестанут собираться. Эта
задача добавляет интерфейс и временные заглушки в обеих реализациях; задачи
2–7 заменяют заглушки настоящим кодом одну за другой.

**Files:**
- Modify: `internal/tracker/tracker.go`
- Modify: `internal/tracker/mock/mock.go`
- Modify: `internal/tracker/jira/jira.go`

**Interfaces:**
- Produces: `tracker.TaskInput{Summary, Description string; Labels []string}`;
  `Tracker.CreateTask(project string, input TaskInput) (TaskRef, error)`;
  `Tracker.FindByMarker(project, marker string) ([]TaskRef, error)`;
  `Tracker.AddAttachment(key string, by Actor, name string, data []byte) (id string, err error)`;
  `Tracker.GetAttachment(key, id string) ([]byte, error)`;
  `Tracker.LinkDependsOn(key, dependsOnKey string, by Actor) error`.

- [x] **Step 1: Написать тест на компиляцию (падающий)**

  Тест ничего не запускает — он констатирует состояние «интерфейса ещё нет».
  Запусти:

  ```bash
  go build ./... 2>&1 | tee /tmp/before.txt
  ```

  Ожидаемо: сборка проходит (интерфейс ещё не тронут). Это точка отсчёта,
  не провал — следующий шаг специально ломает сборку `mock`/`jira`,
  добавив методы в интерфейс раньше реализаций.

- [x] **Step 2: Добавить `TaskInput` и методы в интерфейс**

  В `internal/tracker/tracker.go`, сразу после `func (t TaskRef) LeaseAlive`
  и перед комментарием `// Actor —`:

  ```go
  // TaskInput — данные для создания новой задачи. Отдельный тип, а не Task
  // целиком: у только что создаваемой задачи нет ни ключа, ни аренды, ни
  // статуса — их назначает сам трекер.
  type TaskInput struct {
      Summary     string
      Description string
      Labels      []string
  }
  ```

  В `Task` (после поля `Labels []string`):

  ```go
      Labels      []string
      // DependsOn — ключи задач, от которых зависит эта (LinkDependsOn).
      // Пишется этой волной, не читается никаким кодом Change 1 — гейт
      // очерёдности по этому полю добавит Change 2.
      DependsOn   []string
  ```

  В интерфейс `Tracker`, после `SetAttempts`:

  ```go
      // CreateTask заводит новую задачу. Без Actor: создавать нечего "владеть" —
      // как у Add в mock (не из контракта) и List/ListReady в самом контракте.
      CreateTask(project string, input TaskInput) (TaskRef, error)

      // FindByMarker — задачи проекта с данной меткой. Источник идемпотентности
      // пакетного создания: спрашивает трекер, не хранимую запись о нём.
      FindByMarker(project, marker string) ([]TaskRef, error)

      // AddAttachment сохраняет сырые данные вложением к существующей, уже
      // захваченной задаче — Actor и CheckOwner нужны, как у Comment.
      AddAttachment(key string, by Actor, name string, data []byte) (id string, err error)

      // GetAttachment читает вложение обратно. Без Actor — как Get, чтение
      // не требует владения.
      GetAttachment(key, id string) ([]byte, error)

      // LinkDependsOn связывает только что созданную задачу (key) с её
      // зависимостью (dependsOnKey). by обычно BySystem() — тем же приёмом,
      // что reap и разбор ответа человека используют для мутаций вне аренды
      // какой-либо роли.
      LinkDependsOn(key, dependsOnKey string, by Actor) error
  ```

- [x] **Step 3: Убедиться, что сборка сломана именно там, где ожидалось**

  ```bash
  go build ./... 2>&1
  ```

  Ожидаемо: `internal/tracker/mock` и `internal/tracker/jira` не собираются —
  `*Tracker does not implement tracker.Tracker (missing method CreateTask)`
  (и ещё четыре метода) в обоих пакетах.

- [x] **Step 4: Временные заглушки в `mock`**

  В `internal/tracker/mock/mock.go`, после `SetAttempts`:

  ```go
  // CreateTask — заглушка, замещается настоящей реализацией в задаче 2
  // плана docs/superpowers/plans/2026-09-05-split-autocreate-tickets.md.
  func (t *Tracker) CreateTask(project string, input tracker.TaskInput) (tracker.TaskRef, error) {
      return tracker.TaskRef{}, errors.New("mock.CreateTask: пока не реализовано")
  }

  func (t *Tracker) FindByMarker(project, marker string) ([]tracker.TaskRef, error) {
      return nil, errors.New("mock.FindByMarker: пока не реализовано")
  }

  func (t *Tracker) AddAttachment(key string, by tracker.Actor, name string, data []byte) (string, error) {
      return "", errors.New("mock.AddAttachment: пока не реализовано")
  }

  func (t *Tracker) GetAttachment(key, id string) ([]byte, error) {
      return nil, errors.New("mock.GetAttachment: пока не реализовано")
  }

  func (t *Tracker) LinkDependsOn(key, dependsOnKey string, by tracker.Actor) error {
      return errors.New("mock.LinkDependsOn: пока не реализовано")
  }
  ```

- [x] **Step 5: Временные заглушки в `jira`**

  В `internal/tracker/jira/jira.go`, после `SetAttempts`:

  ```go
  // CreateTask — заглушка, замещается настоящей реализацией в задаче 5
  // плана docs/superpowers/plans/2026-09-05-split-autocreate-tickets.md.
  func (t *Tracker) CreateTask(project string, input tracker.TaskInput) (tracker.TaskRef, error) {
      return tracker.TaskRef{}, errors.New("jira.CreateTask: пока не реализовано")
  }

  func (t *Tracker) FindByMarker(project, marker string) ([]tracker.TaskRef, error) {
      return nil, errors.New("jira.FindByMarker: пока не реализовано")
  }

  func (t *Tracker) AddAttachment(key string, by tracker.Actor, name string, data []byte) (string, error) {
      return "", errors.New("jira.AddAttachment: пока не реализовано")
  }

  func (t *Tracker) GetAttachment(key, id string) ([]byte, error) {
      return nil, errors.New("jira.GetAttachment: пока не реализовано")
  }

  func (t *Tracker) LinkDependsOn(key, dependsOnKey string, by tracker.Actor) error {
      return errors.New("jira.LinkDependsOn: пока не реализовано")
  }
  ```

- [x] **Step 6: Сборка проходит снова**

  ```bash
  go build ./... && go vet ./...
  ```

  Ожидаемо: без ошибок.

- [x] **Step 7: Commit**

  ```bash
  git add internal/tracker/tracker.go internal/tracker/mock/mock.go internal/tracker/jira/jira.go
  git commit -m "feat(tracker): add TaskInput and 5-method surface to Tracker interface"
  ```

---

## Task 2: `mock` — `CreateTask` и `FindByMarker`

**Files:**
- Modify: `internal/tracker/mock/mock.go`
- Modify: `internal/tracker/mock/mock_test.go`

**Interfaces:**
- Consumes: `tracker.TaskInput`, `tracker.TaskRef`, существующие `t.Keys()`, `t.list(match func(tracker.Task) bool)`, `t.dir(key)`, `writeTask`, `t.updated(key)`.
- Produces: `(*Tracker).CreateTask`, `(*Tracker).FindByMarker`, обе — настоящая реализация вместо заглушки задачи 1.

- [x] **Step 1: Написать падающий тест**

  В `internal/tracker/mock/mock_test.go` добавить в импорты `"os"` и
  `"path/filepath"` (их сегодня в файле нет), затем:

  ```go
  func TestCreateTaskThenFindByMarker(t *testing.T) {
      tr := fixture(t) // OFF-1 уже есть; следующий ключ — OFF-2

      ref, err := tr.CreateTask("OFF", tracker.TaskInput{
          Summary: "Category CRUD", Description: "Модель, миграция, CRUD категорий.",
          Labels: []string{"split-child:OFF-1:category-crud"},
      })
      if err != nil {
          t.Fatalf("задача не создана: %v", err)
      }
      if ref.Key != "OFF-2" {
          t.Errorf("ключ %q, ожидался OFF-2", ref.Key)
      }
      if ref.Status != "Analysis" {
          t.Errorf("статус %q, ожидался Analysis", ref.Status)
      }

      found, err := tr.FindByMarker("OFF", "split-child:OFF-1:category-crud")
      if err != nil {
          t.Fatalf("поиск по метке не удался: %v", err)
      }
      if len(found) != 1 || found[0].Key != "OFF-2" {
          t.Errorf("найдено %+v, ожидалась одна OFF-2", found)
      }
  }

  func TestFindByMarkerEmptyWhenNoneMatch(t *testing.T) {
      tr := fixture(t)
      found, err := tr.FindByMarker("OFF", "split-child:OFF-1:none")
      if err != nil {
          t.Fatalf("поиск по метке не удался: %v", err)
      }
      if len(found) != 0 {
          t.Errorf("найдено %+v, ожидался пустой список", found)
      }
  }

  func TestCreateTaskCollisionFailsInsteadOfOverwriting(t *testing.T) {
      tr := fixture(t)
      if err := os.Mkdir(filepath.Join(tr.Root(), "OFF-2"), 0o755); err != nil {
          t.Fatalf("подготовка не удалась: %v", err)
      }

      if _, err := tr.CreateTask("OFF", tracker.TaskInput{Summary: "x", Description: "y"}); err == nil {
          t.Error("коллизия ключа с уже существующим каталогом не замечена")
      }
  }
  ```

- [x] **Step 2: Убедиться, что тест падает**

  ```bash
  go test ./internal/tracker/mock/... -run 'TestCreateTaskThenFindByMarker|TestFindByMarkerEmptyWhenNoneMatch|TestCreateTaskCollisionFailsInsteadOfOverwriting' -v
  ```

  Ожидаемо: FAIL — заглушки задачи 1 возвращают «пока не реализовано».

- [x] **Step 3: Реализовать**

  В `internal/tracker/mock/mock.go`, в блок констант добавить `attachmentsDir`
  (нужен и этой задаче для `Add`, чтобы задачи, заведённые до `CreateTask`,
  тоже могли принять вложение в задаче 3):

  ```go
  const (
      taskFileName   = "task.yaml"
      commentsDir    = "comments"
      attachmentsDir = "attachments"
      leasePrefix    = "lease."
      leaseFree      = leasePrefix + "free"
  )
  ```

  В `Add`, рядом с уже существующим `os.MkdirAll(filepath.Join(dir, commentsDir), 0o755)`:

  ```go
      if err := os.MkdirAll(filepath.Join(dir, commentsDir), 0o755); err != nil {
          return fmt.Errorf("каталог задачи не создан: %w", err)
      }
      if err := os.MkdirAll(filepath.Join(dir, attachmentsDir), 0o755); err != nil {
          return fmt.Errorf("каталог вложений не создан: %w", err)
      }
  ```

  Заменить заглушки `CreateTask`/`FindByMarker` из задачи 1 на:

  ```go
  // createdStatus — начальный статус тикета, заведённого CreateTask. Analysis —
  // тот же вход, что человек даёт обычной задаче, переводя её из Backlog
  // («берите в работу», workflow.yaml). TaskInput статуса не несёт (design
  // doc) — решать его обязана реализация, а не вызывающий код.
  const createdStatus = "Analysis"

  // nextKey подбирает следующий свободный ключ проекта: <project>-N, где N —
  // максимум существующих номеров этого проекта плюс один. От коллизии двух
  // параллельных CreateTask эта функция сама не защищает — защищает os.Mkdir
  // в CreateTask.
  func (t *Tracker) nextKey(project string) (string, error) {
      keys, err := t.Keys()
      if err != nil {
          return "", err
      }
      prefix := project + "-"
      max := 0
      for _, key := range keys {
          n, ok := strings.CutPrefix(key, prefix)
          if !ok {
              continue
          }
          if v, err := strconv.Atoi(n); err == nil && v > max {
              max = v
          }
      }
      return fmt.Sprintf("%s%d", prefix, max+1), nil
  }

  // CreateTask заводит новую задачу с ключом <project>-N. Директория задачи
  // создаётся os.Mkdir, не MkdirAll: коллизия двух параллельных CreateTask,
  // подобравших один и тот же номер, обязана упасть с ошибкой, а не молча
  // переписать половину задачи другого — mock отлаживает конвейер, а не
  // имитирует конкурентный трекер под нагрузкой (доккомментарий пакета).
  func (t *Tracker) CreateTask(project string, input tracker.TaskInput) (tracker.TaskRef, error) {
      key, err := t.nextKey(project)
      if err != nil {
          return tracker.TaskRef{}, err
      }
      dir := t.dir(key)
      if err := os.Mkdir(dir, 0o755); err != nil {
          return tracker.TaskRef{}, fmt.Errorf("задача %s не создана: %w", key, err)
      }
      if err := os.Mkdir(filepath.Join(dir, commentsDir), 0o755); err != nil {
          return tracker.TaskRef{}, fmt.Errorf("каталог комментариев %s не создан: %w", key, err)
      }
      if err := os.Mkdir(filepath.Join(dir, attachmentsDir), 0o755); err != nil {
          return tracker.TaskRef{}, fmt.Errorf("каталог вложений %s не создан: %w", key, err)
      }

      task := tracker.Task{
          Key: key, Project: project, Summary: input.Summary, Description: input.Description,
          Status: createdStatus, Labels: input.Labels,
      }
      if err := writeTask(dir, task); err != nil {
          return tracker.TaskRef{}, err
      }
      if err := os.WriteFile(filepath.Join(dir, leaseFree), nil, 0o644); err != nil {
          return tracker.TaskRef{}, fmt.Errorf("аренда %s не заведена: %w", key, err)
      }

      created, err := t.Get(key)
      if err != nil {
          return tracker.TaskRef{}, err
      }
      ref := created.Ref()
      ref.Updated = t.updated(key)
      return ref, nil
  }

  // FindByMarker — задачи проекта с данной меткой.
  func (t *Tracker) FindByMarker(project, marker string) ([]tracker.TaskRef, error) {
      return t.list(func(task tracker.Task) bool {
          return task.Project == project && slices.Contains(task.Labels, marker)
      })
  }
  ```

  `strconv` уже импортирован в `mock.go` (используется в `readLease`).
  `strings.CutPrefix` доступен на Go 1.20+ (модуль — 1.26).

  `readTask`/`writeTask` также нужно расширить полем `DependsOn` — это
  сделает задача 4 (там же, где заводится `LinkDependsOn`); на этом шаге
  `taskFile` менять не нужно, `CreateTask` его не трогает.

- [x] **Step 4: Тест проходит**

  ```bash
  go test ./internal/tracker/mock/... -run 'TestCreateTaskThenFindByMarker|TestFindByMarkerEmptyWhenNoneMatch|TestCreateTaskCollisionFailsInsteadOfOverwriting' -v
  ```

  Ожидаемо: PASS.

- [x] **Step 5: Полный прогон пакета — старое не сломано**

  ```bash
  go test ./internal/tracker/mock/...
  ```

- [x] **Step 6: Commit**

  ```bash
  git add internal/tracker/mock/mock.go internal/tracker/mock/mock_test.go
  git commit -m "feat(mock): implement CreateTask and FindByMarker"
  ```

---

## Task 3: `mock` — `AddAttachment`/`GetAttachment`

**Files:**
- Modify: `internal/tracker/mock/mock.go`
- Modify: `internal/tracker/mock/mock_test.go`

**Interfaces:**
- Consumes: `tracker.CheckOwner`, `tracker.Actor`, `tracker.ErrNotFound`, `tracker.ErrNotOwner`.
- Produces: `(*Tracker).AddAttachment`, `(*Tracker).GetAttachment`; общий
  приватный помощник `nextExclusive(dir string, existing int, suffix string) (*os.File, string, error)`,
  переиспользуемый существующим `AddComment`.

- [x] **Step 1: Написать падающий тест**

  В `internal/tracker/mock/mock_test.go` добавить в импорты `"bytes"`, затем:

  ```go
  func TestAddAttachmentThenGetAttachmentRoundTrips(t *testing.T) {
      tr := fixture(t)
      data := []byte(`{"children":[{"id":"a","title":"A","description":"d"}]}`)

      id, err := tr.AddAttachment("OFF-1", tracker.BySystem(), "split.json", data)
      if err != nil {
          t.Fatalf("вложение не сохранено: %v", err)
      }

      got, err := tr.GetAttachment("OFF-1", id)
      if err != nil {
          t.Fatalf("вложение не прочитано: %v", err)
      }
      if !bytes.Equal(got, data) {
          t.Errorf("вложение %q, ожидалось %q", got, data)
      }
  }

  func TestGetAttachmentUnknownIDFails(t *testing.T) {
      tr := fixture(t)
      if _, err := tr.GetAttachment("OFF-1", "9999"); !errors.Is(err, tracker.ErrNotFound) {
          t.Errorf("ошибка %v, ожидался ErrNotFound", err)
      }
  }

  func TestAddAttachmentRequiresOwnership(t *testing.T) {
      tr := fixture(t)
      if err := claim(tr, "прогон-1"); err != nil {
          t.Fatalf("захват не удался: %v", err)
      }
      if _, err := tr.AddAttachment("OFF-1", tracker.ByRun("чужой"), "x", []byte("y")); !errors.Is(err, tracker.ErrNotOwner) {
          t.Errorf("ошибка %v, ожидался ErrNotOwner", err)
      }
  }
  ```

  (`errors` уже импортирован в файле.)

- [x] **Step 2: Убедиться, что тест падает**

  ```bash
  go test ./internal/tracker/mock/... -run 'TestAddAttachmentThenGetAttachmentRoundTrips|TestGetAttachmentUnknownIDFails|TestAddAttachmentRequiresOwnership' -v
  ```

- [x] **Step 3: Рефакторинг `AddComment` + реализация**

  В `internal/tracker/mock/mock.go` заменить тело эксклюзивного цикла в
  `AddComment` на вызов общего помощника, и добавить сам помощник рядом:

  ```go
  // nextExclusive создаёт файл со следующим по счёту именем в каталоге,
  // эксклюзивно: два конкурентных писателя гарантированно получают разные
  // номера. Общий приём для комментариев (AddComment) и вложений
  // (AddAttachment) — вместо двух копий одного и того же цикла.
  func nextExclusive(dir string, existing int, suffix string) (*os.File, string, error) {
      for n := existing + 1; n < existing+16; n++ {
          name := fmt.Sprintf("%04d%s", n, suffix)
          f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
          if errors.Is(err, os.ErrExist) {
              continue
          }
          if err != nil {
              return nil, "", fmt.Errorf("файл %s не создан: %w", name, err)
          }
          return f, name, nil
      }
      return nil, "", errors.New("не нашлось свободного номера")
  }
  ```

  В `AddComment`, заменить:

  ```go
      // Номер занимаем эксклюзивным созданием: два комментатора одновременно
      // не должны получить один файл.
      for n := len(existing) + 1; n < len(existing)+16; n++ {
          path := filepath.Join(dir, fmt.Sprintf("%04d.md", n))
          f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
          if errors.Is(err, os.ErrExist) {
              continue
          }
          if err != nil {
              return fmt.Errorf("комментарий не записан: %w", err)
          }
          defer f.Close()
          if _, err := f.WriteString(content); err != nil {
              return fmt.Errorf("комментарий не записан: %w", err)
          }
          return nil
      }
      return errors.New("комментарий не записан: не нашлось свободного номера")
  ```

  на:

  ```go
      f, _, err := nextExclusive(dir, len(existing), ".md")
      if err != nil {
          return fmt.Errorf("комментарий не записан: %w", err)
      }
      defer f.Close()
      if _, err := f.WriteString(content); err != nil {
          return fmt.Errorf("комментарий не записан: %w", err)
      }
      return nil
  ```

  Затем заменить заглушки `AddAttachment`/`GetAttachment` из задачи 1 на:

  ```go
  // AddAttachment сохраняет сырые данные вложением. name сегодня не влияет
  // на путь хранения (файл называется по номеру, как и комментарии) —
  // параметр существует ради паритета с jira, которой имя нужно для
  // multipart-формы.
  func (t *Tracker) AddAttachment(key string, by tracker.Actor, name string, data []byte) (string, error) {
      task, err := t.Get(key)
      if err != nil {
          return "", err
      }
      if err := tracker.CheckOwner(task, by, t.Now()); err != nil {
          return "", err
      }

      dir := filepath.Join(t.dir(key), attachmentsDir)
      entries, err := os.ReadDir(dir)
      if err != nil {
          return "", fmt.Errorf("вложения %s не прочитаны: %w", key, err)
      }

      f, id, err := nextExclusive(dir, len(entries), "")
      if err != nil {
          return "", fmt.Errorf("вложение %s не записано: %w", key, err)
      }
      defer f.Close()
      if _, err := f.Write(data); err != nil {
          return "", fmt.Errorf("вложение %s не записано: %w", key, err)
      }
      return id, nil
  }

  // GetAttachment читает вложение обратно, байт в байт.
  func (t *Tracker) GetAttachment(key, id string) ([]byte, error) {
      data, err := os.ReadFile(filepath.Join(t.dir(key), attachmentsDir, id))
      if errors.Is(err, os.ErrNotExist) {
          return nil, fmt.Errorf("%w: вложение %s/%s", tracker.ErrNotFound, key, id)
      }
      if err != nil {
          return nil, fmt.Errorf("вложение %s/%s не прочитано: %w", key, id, err)
      }
      return data, nil
  }
  ```

- [x] **Step 4: Тесты проходят, включая существующие комментарные**

  ```bash
  go test ./internal/tracker/mock/... -v -run 'Attachment|Comment'
  ```

  Ожидаемо: PASS по всем — рефакторинг `AddComment` не должен менять его
  наблюдаемое поведение (те же имена файлов `0001.md`, `0002.md`, …).

- [x] **Step 5: Полный прогон пакета**

  ```bash
  go test ./internal/tracker/mock/...
  ```

- [x] **Step 6: Commit**

  ```bash
  git add internal/tracker/mock/mock.go internal/tracker/mock/mock_test.go
  git commit -m "feat(mock): implement AddAttachment/GetAttachment, share exclusive numbering"
  ```

---

## Task 4: `mock` — `LinkDependsOn` и поле `Task.DependsOn`

**Files:**
- Modify: `internal/tracker/mock/mock.go`
- Modify: `internal/tracker/mock/mock_test.go`

**Interfaces:**
- Consumes: `t.mutate(key, by, change func(*tracker.Task))`.
- Produces: `(*Tracker).LinkDependsOn`; `taskFile.DependsOn []string`.

- [x] **Step 1: Написать падающий тест**

  В `internal/tracker/mock/mock_test.go` добавить в импорты `"slices"`, затем:

  ```go
  func TestLinkDependsOnRecordsDependency(t *testing.T) {
      tr := fixture(t)
      if _, err := tr.CreateTask("OFF", tracker.TaskInput{
          Summary: "B", Description: "d", Labels: []string{"split-child:OFF-1:b"},
      }); err != nil {
          t.Fatalf("задача не создана: %v", err)
      }

      if err := tr.LinkDependsOn("OFF-2", "OFF-1", tracker.BySystem()); err != nil {
          t.Fatalf("связь не записана: %v", err)
      }

      task, err := tr.Get("OFF-2")
      if err != nil {
          t.Fatalf("задача не прочитана: %v", err)
      }
      if !slices.Contains(task.DependsOn, "OFF-1") {
          t.Errorf("DependsOn %v не содержит OFF-1", task.DependsOn)
      }
  }

  func TestLinkDependsOnIsIdempotent(t *testing.T) {
      tr := fixture(t)
      if err := tr.LinkDependsOn("OFF-1", "OFF-1", tracker.BySystem()); err != nil {
          t.Fatalf("связь не записана: %v", err)
      }
      if err := tr.LinkDependsOn("OFF-1", "OFF-1", tracker.BySystem()); err != nil {
          t.Fatalf("повторная связь не должна падать: %v", err)
      }
      task, err := tr.Get("OFF-1")
      if err != nil {
          t.Fatalf("задача не прочитана: %v", err)
      }
      if len(task.DependsOn) != 1 {
          t.Errorf("DependsOn %v — связь задвоилась", task.DependsOn)
      }
  }
  ```

- [x] **Step 2: Убедиться, что тест падает**

  ```bash
  go test ./internal/tracker/mock/... -run 'TestLinkDependsOn' -v
  ```

- [x] **Step 3: Реализовать**

  В `taskFile` (после `Labels []string`) и в `readTask`/`writeTask` добавить
  `DependsOn`:

  ```go
  type taskFile struct {
      Project     string   `yaml:"project"`
      Summary     string   `yaml:"summary"`
      Description string   `yaml:"description,omitempty"`
      Status      string   `yaml:"status"`
      Labels      []string `yaml:"labels,omitempty"`
      DependsOn   []string `yaml:"depends_on,omitempty"`
      Owner       string   `yaml:"owner,omitempty"`
      Attempts    int      `yaml:"attempts"`
      HumanFlag   bool     `yaml:"human_flag"`
  }
  ```

  В `readTask`, в возвращаемом `tracker.Task{...}` добавить `DependsOn: f.DependsOn,`.
  В `writeTask`, в `taskFile{...}` добавить `DependsOn: task.DependsOn,`.

  Заменить заглушку `LinkDependsOn` из задачи 1 на:

  ```go
  // LinkDependsOn связывает key с dependsOnKey. Идемпотентно само по себе:
  // повторный вызов для уже записанной пары ничего не дублирует — completeSplit
  // (internal/pipeline/splits.go) не хранит отдельного флага «уже связано»
  // и может звать LinkDependsOn повторно при повторе после сбоя.
  func (t *Tracker) LinkDependsOn(key, dependsOnKey string, by tracker.Actor) error {
      return t.mutate(key, by, func(task *tracker.Task) {
          if !slices.Contains(task.DependsOn, dependsOnKey) {
              task.DependsOn = append(task.DependsOn, dependsOnKey)
          }
      })
  }
  ```

  `slices` уже импортирован в `mock.go`.

- [x] **Step 4: Тесты проходят**

  ```bash
  go test ./internal/tracker/mock/... -run 'TestLinkDependsOn' -v
  ```

- [x] **Step 5: Полный прогон пакета — `var _ tracker.Tracker = (*Tracker)(nil)` компилируется**

  ```bash
  go build ./... && go test ./internal/tracker/mock/...
  ```

- [x] **Step 6: Commit**

  ```bash
  git add internal/tracker/mock/mock.go internal/tracker/mock/mock_test.go
  git commit -m "feat(mock): implement LinkDependsOn, add Task.DependsOn field"
  ```

---

## Task 5: `jira` — `CreateTask`, `FindByMarker`, `Config.IssueType`

**Files:**
- Modify: `internal/tracker/jira/jira.go`
- Modify: `internal/tracker/jira/jira_test.go`
- Modify: `tracker.example.yaml`

**Interfaces:**
- Consumes: `t.call`, `t.Get`, `t.searchProject`, `Config`.
- Produces: `(*Tracker).CreateTask`, `(*Tracker).FindByMarker`,
  `Config.IssueType`, `(*Tracker).issueType() string`.

- [x] **Step 1: Расширить `fakeJira` тестовой поддержкой создания**

  В `internal/tracker/jira/jira_test.go` добавить поля в `fakeJira`:

  ```go
      // nextKey — ключ, который вернёт POST /issue. Пусто — по умолчанию VO-2.
      nextKey string
      created []fakeIssue
  ```

  Тип рядом с `fakeJira`:

  ```go
  // fakeIssue — задача, заведённая через POST /issue в этом тесте.
  type fakeIssue struct {
      key    string
      fields map[string]any
  }
  ```

  В `ServeHTTP`, после кейса `strings.HasPrefix(r.URL.Path, "/rest/api/2/project/")`
  и до кейса `r.URL.Path == "/rest/api/2/issue/VO-1" && r.Method == http.MethodGet`,
  добавить:

  ```go
      case r.URL.Path == "/rest/api/2/issue" && r.Method == http.MethodPost:
          fields, _ := body["fields"].(map[string]any)
          key := f.nextKey
          if key == "" {
              key = "VO-2"
          }
          f.created = append(f.created, fakeIssue{key: key, fields: fields})
          w.WriteHeader(http.StatusCreated)
          write(map[string]any{"id": "10100", "key": key})
  ```

  И перед финальным кейсом `strings.HasPrefix(r.URL.Path, "/rest/api/2/issue/")`
  (тот, что отвечает 404 на всё непойманное под `/issue/`), добавить кейс
  для GET уже созданной задачи — иначе `CreateTask`'s внутренний `t.Get`
  после `POST /issue` не найдёт её:

  ```go
      case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/rest/api/2/issue/") &&
          !strings.Contains(strings.TrimPrefix(r.URL.Path, "/rest/api/2/issue/"), "/"):
          key := strings.TrimPrefix(r.URL.Path, "/rest/api/2/issue/")
          for _, c := range f.created {
              if c.key != key {
                  continue
              }
              write(map[string]any{"key": key, "fields": map[string]any{
                  "summary": c.fields["summary"], "description": c.fields["description"],
                  "status": map[string]any{"name": "Backlog"}, "project": map[string]any{"key": f.knownProject},
                  "labels": c.fields["labels"], "updated": "2026-08-17T12:00:00.000+0000",
              }})
              return
          }
          w.WriteHeader(http.StatusNotFound)
          write(map[string]any{"errorMessages": []string{"Issue Does Not Exist"}})
  ```

  (VO-1's собственный GET перехватывается раньше, точным совпадением пути —
  этот кейс его не касается.)

  В `fixture()`, в литерале `Config{...}`, добавить `IssueType: "Task"`.

- [x] **Step 2: Написать падающий тест**

  ```go
  func TestCreateTaskPostsIssueAndReturnsRef(t *testing.T) {
      tr, fake := fixture(t)
      fake.nextKey = "VO-2"

      ref, err := tr.CreateTask("VO", tracker.TaskInput{
          Summary: "Category CRUD", Description: "Модель, миграция, CRUD категорий.",
          Labels: []string{"split-child:VO-1:category-crud"},
      })
      if err != nil {
          t.Fatalf("задача не создана: %v", err)
      }
      if ref.Key != "VO-2" {
          t.Errorf("ключ %q, ожидался VO-2", ref.Key)
      }
      if len(fake.created) != 1 {
          t.Fatalf("создание не отправлено: %+v", fake.created)
      }
      issuetype, _ := fake.created[0].fields["issuetype"].(map[string]any)
      if issuetype["name"] != "Task" {
          t.Errorf("issuetype %v, ожидался Task", issuetype)
      }
      labels, _ := fake.created[0].fields["labels"].([]string)
      if len(labels) != 1 || labels[0] != "split-child:VO-1:category-crud" {
          t.Errorf("labels %v", fake.created[0].fields["labels"])
      }
  }

  func TestFindByMarkerSearchesByLabel(t *testing.T) {
      tr, fake := fixture(t)
      fake.labels = []string{"split-child:VO-1:category-crud"}

      found, err := tr.FindByMarker("VO", "split-child:VO-1:category-crud")
      if err != nil {
          t.Fatalf("поиск не удался: %v", err)
      }
      if len(found) != 1 || found[0].Key != "VO-1" {
          t.Errorf("найдено %+v", found)
      }
      if !strings.Contains(fake.lastJQL, `labels = "split-child:VO-1:category-crud"`) {
          t.Errorf("JQL %q не фильтрует по метке", fake.lastJQL)
      }
  }

  func TestFindByMarkerEmptyWhenNoIssues(t *testing.T) {
      tr, fake := fixture(t)
      fake.noIssues = true

      found, err := tr.FindByMarker("VO", "split-child:VO-1:none")
      if err != nil {
          t.Fatalf("поиск не удался: %v", err)
      }
      if len(found) != 0 {
          t.Errorf("найдено %+v, ожидался пустой список", found)
      }
  }
  ```

- [x] **Step 3: Убедиться, что тесты падают**

  ```bash
  go test ./internal/tracker/jira/... -run 'TestCreateTask|TestFindByMarker' -v
  ```

- [x] **Step 4: Реализовать**

  В `Config` (после `HumanFlagLabel`):

  ```go
      // IssueType — тип задачи для CreateTask. JIRA v2 требует issuetype
      // в теле POST /issue. Пусто — код берёт "Task" (issueType()): на
      // большинстве инстансов он есть из коробки, и заставлять заполнять
      // поле ради дефолтного значения незачем.
      IssueType string `yaml:"issue_type"`
  ```

  Заменить заглушки `CreateTask`/`FindByMarker` из задачи 1 на:

  ```go
  // issueType — тип задачи для CreateTask, с дефолтом.
  func (t *Tracker) issueType() string {
      if t.cfg.IssueType != "" {
          return t.cfg.IssueType
      }
      return "Task"
  }

  // CreateTask заводит новую задачу. Статус создания решает workflow проекта
  // на инстансе — POST /issue не умеет задать статус, и эта реализация не
  // пытается: см. живую проверку (Task 8 плана
  // docs/superpowers/plans/2026-09-05-split-autocreate-tickets.md).
  func (t *Tracker) CreateTask(project string, input tracker.TaskInput) (tracker.TaskRef, error) {
      var created struct {
          Key string `json:"key"`
      }
      fields := map[string]any{
          "project":     map[string]any{"key": project},
          "summary":     input.Summary,
          "description": wiki(input.Description),
          "issuetype":   map[string]any{"name": t.issueType()},
      }
      if len(input.Labels) > 0 {
          fields["labels"] = input.Labels
      }
      if err := t.call(http.MethodPost, "/issue", map[string]any{"fields": fields}, &created); err != nil {
          return tracker.TaskRef{}, err
      }

      task, err := t.Get(created.Key)
      if err != nil {
          return tracker.TaskRef{}, err
      }
      return task.Ref(), nil
  }

  // FindByMarker — задачи проекта с данной меткой, тем же JQL-поиском, что
  // ListReady/List.
  func (t *Tracker) FindByMarker(project, marker string) ([]tracker.TaskRef, error) {
      jql := fmt.Sprintf(`project = %q AND labels = %q`, project, marker)
      return t.searchProject(project, jql, searchPage, func(tracker.Task) bool { return true })
  }
  ```

- [x] **Step 5: Тесты проходят**

  ```bash
  go test ./internal/tracker/jira/... -run 'TestCreateTask|TestFindByMarker' -v
  ```

- [x] **Step 6: Пример конфигурации**

  В `tracker.example.yaml`, после блока `human_flag_label`, добавить:

  ```yaml

  # Тип задачи для тикетов, которые заводит CreateTask (авто-создание детей
  # разбиения). Пусто — код сам возьмёт "Task": на большинстве инстансов он
  # есть из коробки.
  issue_type: Task
  ```

- [x] **Step 7: Полный прогон пакета**

  ```bash
  go test ./internal/tracker/jira/...
  ```

- [x] **Step 8: Commit**

  ```bash
  git add internal/tracker/jira/jira.go internal/tracker/jira/jira_test.go tracker.example.yaml
  git commit -m "feat(jira): implement CreateTask and FindByMarker, add Config.IssueType"
  ```

---

## Task 6: `jira` — `AddAttachment`/`GetAttachment`

REST v2 не отдаёт и не принимает вложения как JSON: запись — `multipart/form-data`,
чтение — сырые байты по ссылке из метаданных. `t.call` работает только с JSON,
поэтому нужны собственные низкоуровневые вызовы.

**Files:**
- Modify: `internal/tracker/jira/jira.go`
- Modify: `internal/tracker/jira/jira_test.go`

**Interfaces:**
- Consumes: `t.client`, `t.user`/`t.secret`, `statusError`, `snippet`.
- Produces: `(*Tracker).AddAttachment`, `(*Tracker).GetAttachment`,
  приватные `(*Tracker).upload`, `(*Tracker).download`.

- [x] **Step 1: Расширить `fakeJira` поддержкой вложений**

  В `internal/tracker/jira/jira_test.go`, поля `fakeJira`:

  ```go
      baseURL     string
      attachments []fakeAttachment
  ```

  Тип рядом с `fakeIssue`:

  ```go
  // fakeAttachment — вложение, принятое через POST /issue/{key}/attachments.
  type fakeAttachment struct {
      id, name string
      data     []byte
  }
  ```

  В `fixture()`, сразу после `server := httptest.NewServer(fake)`:

  ```go
      fake.baseURL = server.URL
  ```

  В `ServeHTTP`, перед финальным catch-all `strings.HasPrefix(r.URL.Path, "/rest/api/2/issue/")`,
  добавить (и обязательно **выше** уже добавленного в задаче 5 кейса "GET на
  только что созданный тикет" — этот путь имеет суффикс `/attachments`,
  который тот кейс исключает через проверку на `/`, так что порядок между
  ними не важен, но кейс должен стоять выше generic catch-all):

  ```go
      case r.URL.Path == "/rest/api/2/issue/VO-1/attachments" && r.Method == http.MethodPost:
          if got := r.Header.Get("X-Atlassian-Token"); got != "no-check" {
              f.t.Errorf("вложение отправлено без X-Atlassian-Token: no-check, получено %q", got)
          }
          if err := r.ParseMultipartForm(10 << 20); err != nil {
              f.t.Fatalf("вложение не разобрано: %v", err)
          }
          file, header, err := r.FormFile("file")
          if err != nil {
              f.t.Fatalf("файла нет в форме вложения: %v", err)
          }
          defer file.Close()
          data, _ := io.ReadAll(file)
          id := strconv.Itoa(20000 + len(f.attachments))
          f.attachments = append(f.attachments, fakeAttachment{id: id, name: header.Filename, data: data})
          write([]map[string]any{{"id": id, "filename": header.Filename}})

      case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/rest/api/2/attachment/"):
          id := strings.TrimPrefix(r.URL.Path, "/rest/api/2/attachment/")
          for _, a := range f.attachments {
              if a.id == id {
                  write(map[string]any{"id": id, "content": f.baseURL + "/secure/attachment/" + id})
                  return
              }
          }
          w.WriteHeader(http.StatusNotFound)

      case strings.HasPrefix(r.URL.Path, "/secure/attachment/"):
          id := strings.TrimPrefix(r.URL.Path, "/secure/attachment/")
          for _, a := range f.attachments {
              if a.id == id {
                  w.Write(a.data)
                  return
              }
          }
          w.WriteHeader(http.StatusNotFound)
  ```

- [x] **Step 2: Написать падающий тест**

  Добавить `"bytes"` в импорты `jira_test.go`, затем:

  ```go
  func TestAddAttachmentUploadsMultipart(t *testing.T) {
      tr, fake := fixture(t)
      id, err := tr.AddAttachment("VO-1", tracker.BySystem(), "split.json", []byte(`{"children":[]}`))
      if err != nil {
          t.Fatalf("вложение не отправлено: %v", err)
      }
      if len(fake.attachments) != 1 || fake.attachments[0].id != id {
          t.Fatalf("вложение не сохранено на сервере: %+v", fake.attachments)
      }
      if fake.attachments[0].name != "split.json" {
          t.Errorf("имя файла %q, ожидалось split.json", fake.attachments[0].name)
      }
  }

  func TestGetAttachmentDownloadsContent(t *testing.T) {
      tr, _ := fixture(t)
      data := []byte(`{"children":[{"id":"a"}]}`)
      id, err := tr.AddAttachment("VO-1", tracker.BySystem(), "split.json", data)
      if err != nil {
          t.Fatalf("вложение не отправлено: %v", err)
      }

      got, err := tr.GetAttachment("VO-1", id)
      if err != nil {
          t.Fatalf("вложение не прочитано: %v", err)
      }
      if !bytes.Equal(got, data) {
          t.Errorf("вложение %q, ожидалось %q", got, data)
      }
  }
  ```

- [x] **Step 3: Убедиться, что тесты падают**

  ```bash
  go test ./internal/tracker/jira/... -run 'TestAddAttachment|TestGetAttachment' -v
  ```

- [x] **Step 4: Реализовать**

  Добавить `"mime/multipart"` в импорты `jira.go`. Заменить заглушки
  `AddAttachment`/`GetAttachment` из задачи 1 на:

  ```go
  // upload выполняет multipart-запрос: вложения не JSON, и t.call им не
  // годится. X-Atlassian-Token обязателен — без него JIRA отклонит запись
  // вложения так же, как отклоняет её без basic-авторизации (см. call).
  func (t *Tracker) upload(path, filename string, data []byte) ([]byte, error) {
      var body bytes.Buffer
      w := multipart.NewWriter(&body)
      part, err := w.CreateFormFile("file", filename)
      if err != nil {
          return nil, fmt.Errorf("вложение не собрано: %w", err)
      }
      if _, err := part.Write(data); err != nil {
          return nil, fmt.Errorf("вложение не собрано: %w", err)
      }
      if err := w.Close(); err != nil {
          return nil, fmt.Errorf("вложение не собрано: %w", err)
      }

      req, err := http.NewRequest(http.MethodPost, t.cfg.BaseURL+apiPath+path, &body)
      if err != nil {
          return nil, fmt.Errorf("запрос не собран: %w", err)
      }
      req.SetBasicAuth(t.user, t.secret)
      req.Header.Set("Accept", "application/json")
      req.Header.Set("Content-Type", w.FormDataContentType())
      req.Header.Set("X-Atlassian-Token", "no-check")

      resp, err := t.client.Do(req)
      if err != nil {
          return nil, fmt.Errorf("%s %s: %w", http.MethodPost, path, err)
      }
      defer resp.Body.Close()

      raw, _ := io.ReadAll(resp.Body)
      if resp.StatusCode >= 300 {
          return nil, statusError(http.MethodPost, path, resp.StatusCode, raw)
      }
      return raw, nil
  }

  // download читает вложение по прямой ссылке из ответа GET /attachment/{id}:
  // она не под /rest/api/2 и не отдаёт JSON, поэтому не годится t.call.
  func (t *Tracker) download(url string) ([]byte, error) {
      req, err := http.NewRequest(http.MethodGet, url, nil)
      if err != nil {
          return nil, fmt.Errorf("запрос вложения не собран: %w", err)
      }
      req.SetBasicAuth(t.user, t.secret)
      req.Header.Set("X-Atlassian-Token", "no-check")

      resp, err := t.client.Do(req)
      if err != nil {
          return nil, fmt.Errorf("GET %s: %w", url, err)
      }
      defer resp.Body.Close()

      raw, err := io.ReadAll(resp.Body)
      if err != nil {
          return nil, fmt.Errorf("вложение не прочитано: %w", err)
      }
      if resp.StatusCode >= 300 {
          return nil, statusError(http.MethodGet, url, resp.StatusCode, raw)
      }
      return raw, nil
  }

  // AddAttachment сохраняет сырые данные вложением. Ответ JIRA на создание —
  // массив из одного элемента; возвращается его id.
  func (t *Tracker) AddAttachment(key string, by tracker.Actor, name string, data []byte) (string, error) {
      if _, err := t.owned(key, by); err != nil {
          return "", err
      }

      raw, err := t.upload("/issue/"+key+"/attachments", name, data)
      if err != nil {
          return "", err
      }

      var created []struct {
          ID string `json:"id"`
      }
      if err := json.Unmarshal(raw, &created); err != nil {
          return "", fmt.Errorf("вложение %s: ответ не разобран: %w\n%s", key, err, snippet(raw))
      }
      if len(created) == 0 {
          return "", fmt.Errorf("вложение %s: сервер не назвал идентификатор", key)
      }
      return created[0].ID, nil
  }

  // GetAttachment читает вложение обратно. key не используется: идентификаторы
  // вложений в JIRA глобальны — параметр входит в контракт ради файлового
  // трекера, которому путь по ключу задачи и нужен.
  func (t *Tracker) GetAttachment(_, id string) ([]byte, error) {
      var meta struct {
          Content string `json:"content"`
      }
      if err := t.call(http.MethodGet, "/attachment/"+id, nil, &meta); err != nil {
          return nil, err
      }
      return t.download(meta.Content)
  }
  ```

- [x] **Step 5: Тесты проходят**

  ```bash
  go test ./internal/tracker/jira/... -run 'TestAddAttachment|TestGetAttachment' -v
  ```

- [x] **Step 6: Полный прогон пакета**

  ```bash
  go test ./internal/tracker/jira/...
  ```

- [x] **Step 7: Commit**

  ```bash
  git add internal/tracker/jira/jira.go internal/tracker/jira/jira_test.go
  git commit -m "feat(jira): implement AddAttachment/GetAttachment via multipart"
  ```

---

## Task 7: `jira` — `LinkDependsOn` и `Config.DependsOnLink`

**Files:**
- Modify: `internal/tracker/jira/jira.go`
- Modify: `internal/tracker/jira/jira_test.go`
- Modify: `tracker.example.yaml`

**Interfaces:**
- Consumes: `t.owned`, `t.call`, `Config`.
- Produces: `(*Tracker).LinkDependsOn`, `Config.DependsOnLink`.

- [x] **Step 1: Расширить `fakeJira` поддержкой `issueLink`**

  В `internal/tracker/jira/jira_test.go`, поле `fakeJira`:

  ```go
      issueLinks []map[string]any
  ```

  В `ServeHTTP`, рядом с прочими кейсами верхнего уровня (не под `/issue/`):

  ```go
      case r.URL.Path == "/rest/api/2/issueLink" && r.Method == http.MethodPost:
          f.issueLinks = append(f.issueLinks, body)
          w.WriteHeader(http.StatusCreated)
  ```

  В `fixture()`, в литерале `Config{...}`, добавить `DependsOnLink: "Depends"`.

- [x] **Step 2: Написать падающий тест**

  ```go
  func TestLinkDependsOnPostsIssueLink(t *testing.T) {
      tr, fake := fixture(t)
      if err := tr.LinkDependsOn("VO-1", "VO-2", tracker.BySystem()); err != nil {
          t.Fatalf("связь не записана: %v", err)
      }
      if len(fake.issueLinks) != 1 {
          t.Fatalf("issueLink не отправлен: %+v", fake.issueLinks)
      }
      link := fake.issueLinks[0]
      linkType, _ := link["type"].(map[string]any)
      if linkType["name"] != "Depends" {
          t.Errorf("тип связи %v, ожидался Depends", linkType)
      }
      outward, _ := link["outwardIssue"].(map[string]any)
      inward, _ := link["inwardIssue"].(map[string]any)
      if outward["key"] != "VO-1" || inward["key"] != "VO-2" {
          t.Errorf("направление связи %v/%v: VO-1 «зависит от» VO-2, значит VO-1 — outward", outward, inward)
      }
  }

  func TestLinkDependsOnRequiresOwnership(t *testing.T) {
      tr, fake := fixture(t)
      fake.status = "In Progress"
      fake.runID = "прогон-1"
      fake.leaseUntil = "2026-08-17T12:30:00.000+0000"

      if err := tr.LinkDependsOn("VO-1", "VO-2", tracker.ByRun("чужой")); !errors.Is(err, tracker.ErrNotOwner) {
          t.Errorf("ошибка %v, ожидался ErrNotOwner", err)
      }
  }
  ```

- [x] **Step 3: Убедиться, что тесты падают**

  ```bash
  go test ./internal/tracker/jira/... -run 'TestLinkDependsOn' -v
  ```

- [x] **Step 4: Реализовать**

  В `Config` (после `IssueType`):

  ```go
      // DependsOnLink — имя типа связи "зависит от" на инстансе
      // (LinkDependsOn, POST /issueLink). Обязателен: без него нечем собрать
      // тело запроса. Заводится или подбирается на полигоне — см. живую
      // проверку, Task 8 плана
      // docs/superpowers/plans/2026-09-05-split-autocreate-tickets.md.
      DependsOnLink string `yaml:"depends_on_link"`
  ```

  В `LoadConfig`, рядом с проверкой `HumanFlagLabel`, добавить:

  ```go
      if cfg.DependsOnLink == "" {
          errs = append(errs, errors.New("depends_on_link не задан: без него не собрать тип связи для LinkDependsOn"))
      }
  ```

  Заменить заглушку `LinkDependsOn` из задачи 1 на:

  ```go
  // LinkDependsOn связывает key с dependsOnKey типом связи из конфигурации.
  //
  // key — исходящая сторона (key "зависит от" dependsOnKey): подобрано под
  // связь, чьё outward-описание читается как "depends on" — так называют
  // стандартный тип "Depends", если он есть на инстансе. Если на инстансе
  // заведён свой тип с обратным направлением, поменяйте местами
  // outwardIssue/inwardIssue здесь — решается по факту живой проверки
  // (Task 8 плана).
  //
  // Идемпотентность повторного POST для той же пары не проверена в коде:
  // Task 8 подтверждает её на реальном инстансе и, если понадобится,
  // добавляет проверку существующих issuelinks перед созданием.
  func (t *Tracker) LinkDependsOn(key, dependsOnKey string, by tracker.Actor) error {
      if _, err := t.owned(key, by); err != nil {
          return err
      }
      return t.call(http.MethodPost, "/issueLink", map[string]any{
          "type":         map[string]any{"name": t.cfg.DependsOnLink},
          "outwardIssue": map[string]any{"key": key},
          "inwardIssue":  map[string]any{"key": dependsOnKey},
      }, nil)
  }
  ```

- [x] **Step 5: Тесты проходят**

  ```bash
  go test ./internal/tracker/jira/... -run 'TestLinkDependsOn' -v
  ```

- [x] **Step 6: Пример конфигурации**

  В `tracker.example.yaml`, после только что добавленного `issue_type`:

  ```yaml

  # Тип связи "зависит от" для LinkDependsOn (POST /issueLink). Если на
  # инстансе нет готового — заводится в Administration → Issues → Issue
  # linking, имя переносится сюда (см. живую проверку в плане Change 1).
  depends_on_link: Depends
  ```

- [x] **Step 7: Полный прогон пакета, все заглушки заменены**

  ```bash
  go build ./... && go vet ./... && go test ./internal/tracker/...
  ```

  На этом шаге в `jira.go` и `mock.go` не должно остаться ни одной строки
  «пока не реализовано» — проверить:

  ```bash
  grep -rn "пока не реализовано" internal/tracker/
  ```

  Ожидаемо: пусто.

- [x] **Step 8: Commit**

  ```bash
  git add internal/tracker/jira/jira.go internal/tracker/jira/jira_test.go tracker.example.yaml
  git commit -m "feat(jira): implement LinkDependsOn, add Config.DependsOnLink"
  ```

---

## Task 8: Живая проверка `jira`-методов на полигоне Server 8.13

Это не юнит-тест — риски 1 и 2 из design doc/proposal.md касаются
конкретного JIRA-инстанса, и узнать их можно только на нём. `fakeJira` в
задачах 5–7 проверил форму запросов, а не то, что реальный сервер с ними
сделает.

**Files:** нет кодовых изменений на этом шаге (кроме возможного отката,
см. критерий неуспеха ниже); при необходимости правки возвращают к Task 7.

- [x] **Step 1: Тип связи «зависит от» — есть ли на инстансе**

  ```bash
  curl -s -u "$JIRA_USER:$JIRA_PASSWORD" \
    http://localhost:2990/jira/rest/api/2/issueLinkType | jq '.issueLinkTypes[] | {name, inward, outward}'
  ```

  Критерий успеха: в списке есть тип, чьё поле `outward` читается как
  «depends on» (по-английски или в локализации инстанса), или любой другой
  однозначно подходящий по смыслу.

  - Нашёлся → записать точное значение `name` в
    `${OFFICE_HOME}/tracker.yaml`'s `depends_on_link`, сверить с направлением
    `outward`/`inward`: если у найденного типа `outward` означает **обратное**
    («is depended on by» вместо «depends on»), в `LinkDependsOn`
    (Task 7, `internal/tracker/jira/jira.go`) поменять местами
    `outwardIssue`/`inwardIssue` и пересобрать unit-тест
    `TestLinkDependsOnPostsIssueLink` под новое направление.
  - Не нашёлся → завести тип операционным шагом: Jira Administration →
    Issues → Issue Linking → Add Issue Link Type, назвать `Depends`,
    Outward description `depends on`, Inward description `is depended on by`.
    Не блокирует код — это настройка инстанса, а не правка репозитория.

- [x] **Step 2: Идемпотентность повторного `POST /issueLink`**

  Завести два тестовых тикета (или использовать уже существующие на
  полигоне) и связать их дважды подряд тем же типом и направлением:

  ```bash
  curl -s -u "$JIRA_USER:$JIRA_PASSWORD" -X POST \
    -H 'Content-Type: application/json' \
    -d '{"type":{"name":"Depends"},"outwardIssue":{"key":"EXP-90"},"inwardIssue":{"key":"EXP-91"}}' \
    http://localhost:2990/jira/rest/api/2/issueLink -w '\n%{http_code}\n'

  # повторить тот же запрос ровно тем же телом
  curl -s -u "$JIRA_USER:$JIRA_PASSWORD" -X POST \
    -H 'Content-Type: application/json' \
    -d '{"type":{"name":"Depends"},"outwardIssue":{"key":"EXP-90"},"inwardIssue":{"key":"EXP-91"}}' \
    http://localhost:2990/jira/rest/api/2/issueLink -w '\n%{http_code}\n'
  ```

  Затем проверить фактическое число связей:

  ```bash
  curl -s -u "$JIRA_USER:$JIRA_PASSWORD" \
    "http://localhost:2990/jira/rest/api/2/issue/EXP-90?fields=issuelinks" | jq '.fields.issuelinks | length'
  ```

  - Второй `POST` вернул тот же код и связь осталась одна → `LinkDependsOn`
    (Task 7) уже идемпотентен как есть, менять код не нужно.
  - Связь задвоилась (список длиннее одного) → вернуться к `LinkDependsOn`
    в `internal/tracker/jira/jira.go` и добавить проверку перед созданием:

    ```go
    func (t *Tracker) LinkDependsOn(key, dependsOnKey string, by tracker.Actor) error {
        if _, err := t.owned(key, by); err != nil {
            return err
        }

        var existing struct {
            Fields struct {
                Issuelinks []struct {
                    Type         struct{ Name string `json:"name"` } `json:"type"`
                    OutwardIssue struct{ Key string `json:"key"` } `json:"outwardIssue"`
                } `json:"issuelinks"`
            } `json:"fields"`
        }
        if err := t.call(http.MethodGet, "/issue/"+key+"?fields=issuelinks", nil, &existing); err != nil {
            return err
        }
        for _, link := range existing.Fields.Issuelinks {
            if link.Type.Name == t.cfg.DependsOnLink && link.OutwardIssue.Key == key {
                return nil // связь уже есть
            }
        }

        return t.call(http.MethodPost, "/issueLink", map[string]any{
            "type":         map[string]any{"name": t.cfg.DependsOnLink},
            "outwardIssue": map[string]any{"key": key},
            "inwardIssue":  map[string]any{"key": dependsOnKey},
        }, nil)
    }
    ```

    Написать/дополнить unit-тест: `fakeJira`'s кейс GET `/issue/VO-1?fields=issuelinks`
    отдаёт уже записанную связь, второй вызов `LinkDependsOn` не шлёт `POST
    /issueLink` повторно (`len(fake.issueLinks) == 1` после двух вызовов).
    Прогнать `go test ./internal/tracker/jira/...`, закоммитить как отдельный
    коммит `fix(jira): guard LinkDependsOn against duplicate issueLink`.

- [x] **Step 3: Живой round-trip остальных четырёх методов**

  На том же полигоне, под учёткой офиса:

  ```bash
  export OFFICE_HOME=~/.office   # или актуальный путь этой машины
  ```

  Написать одноразовый скрипт (не коммитить, `docs/superpowers/plans/2026-09-05-split-autocreate-tickets.md`
  как ссылка в комментарии, если он останется в истории команд) или
  воспользоваться `go run` с временным `main.go`, вызывающим по очереди
  `jira.Open(cfg)` → `CreateTask` → `FindByMarker` → `AddAttachment` →
  `GetAttachment` на только что созданном тикете. Критерий успеха: все пять
  вызовов возвращают без ошибки, `FindByMarker` находит только что
  созданный тикет по его собственной метке, `GetAttachment` возвращает
  байт-в-байт то, что записал `AddAttachment`.

  Находки (если есть отклонения от unit-тестов на `fakeJira`) вписать в
  `docs/notes/analyst-task-splitting.md` — этим же файлом займётся Task 18
  итоговой записью, здесь — только пометка при необходимости.

---

## Task 9: `marker.go` — поле `Attachment` и `SplitConfirmed`

**Files:**
- Modify: `internal/tracker/marker.go`
- Modify: `internal/tracker/marker_test.go`

**Interfaces:**
- Produces: `Marker.Attachment string`; `SplitConfirmed(comments []Comment, role string) (confirmed bool, attachmentID string)`;
  `EventSplitCreated = "split-created"`; `EventSplitCreateFailed = "split-create-failed"`.

- [x] **Step 1: Написать падающие тесты**

  В `internal/tracker/marker_test.go` добавить рядом с `TestMarkerCarriesNextOwner`:

  ```go
  // Вложение — как next: значимо только у отчётов, и пустое в строку не идёт.
  func TestMarkerCarriesAttachment(t *testing.T) {
      m := Marker{RunID: runID, Role: "analyst", Outcome: "split", Attachment: "10042", ConfigSHA: "5bc6a3b0"}
      line := "[office run:488e8d8f role:analyst outcome:split attachment:10042 config:5bc6a3b0]"

      if got := m.String(); got != line {
          t.Errorf("маркер %q, ожидался %q", got, line)
      }
      parsed, ok := ParseMarker(line)
      if !ok {
          t.Fatalf("свой же маркер не разобран: %s", line)
      }
      if parsed.Attachment != "10042" {
          t.Errorf("attachment разобран как %q", parsed.Attachment)
      }

      without := Marker{RunID: runID, Role: "analyst", Outcome: "split", ConfigSHA: "5bc6a3b0"}
      if got := without.String(); strings.Contains(got, "attachment:") {
          t.Errorf("пустой attachment попал в маркер: %s", got)
      }
  }
  ```

  В `TestParseMarkerRejectsForeignLines`, в срез `lines`, добавить ещё одну
  строку (событие с непустым `attachment` — недопустимо тем же правилом, что
  и `next` у события):

  ```go
      "[office run:488e8d8f role:implementer event:lease-expired attachment:10042 config:5bc6a3b0]",
  ```

  Отдельным файлом-секцией (после существующего `TestWithoutMarker` или в
  конце файла) — тесты `SplitConfirmed`:

  ```go
  // splitReport — комментарий-маркер outcome:split с данным вложением:
  // так выглядит и первое предложение, и подтверждение — разница только
  // в том, какой по счёту в переписке.
  func splitReport(role, attachment string, minute int) Comment {
      m := Marker{RunID: runID, Role: role, Outcome: "split", Attachment: attachment, ConfigSHA: "5bc6a3b0"}
      return comment("office", m.String()+"\nПредложение разбивки.", minute)
  }

  func TestSplitConfirmedNeedsSecondMarker(t *testing.T) {
      comments := []Comment{splitReport("analyst", "10001", 1)}
      if confirmed, _ := SplitConfirmed(comments, "analyst"); confirmed {
          t.Error("одно предложение не должно считаться подтверждением")
      }
  }

  // Между двумя split-маркерами лежит системная запись event:human-reply,
  // подписанная той же ролью (unblock() ставит Role роли, которой был задан
  // вопрос) — она не должна обрывать счёт: SplitConfirmed считает по всей
  // истории, а не серией с конца.
  func TestSplitConfirmedSurvivesInterveningHumanReply(t *testing.T) {
      comments := []Comment{
          splitReport("analyst", "10001", 1),
          comment("human", "Да, разбивай.", 2),
          notice("analyst", EventHumanReply, 3),
          splitReport("analyst", "10002", 4),
      }
      confirmed, attachment := SplitConfirmed(comments, "analyst")
      if !confirmed {
          t.Fatal("второе подряд предложение должно быть подтверждением")
      }
      if attachment != "10002" {
          t.Errorf("вложение %q, ожидалось последнее — 10002", attachment)
      }
  }

  func TestSplitConfirmedIgnoresOtherRole(t *testing.T) {
      comments := []Comment{
          splitReport("implementer", "10001", 1),
          splitReport("implementer", "10002", 2),
      }
      if confirmed, _ := SplitConfirmed(comments, "analyst"); confirmed {
          t.Error("split чужой роли не считается")
      }
  }
  ```

- [x] **Step 2: Убедиться, что тесты падают**

  ```bash
  go test ./internal/tracker/... -run 'TestMarkerCarriesAttachment|TestParseMarkerRejectsForeignLines|TestSplitConfirmed' -v
  ```

  Ожидаемо: `TestMarkerCarriesAttachment` и `TestSplitConfirmed*` — ошибки
  компиляции (`Attachment`/`SplitConfirmed` не существуют).

- [x] **Step 3: Реализовать**

  В `Marker` (после `Next string`):

  ```go
      Next string
      // Attachment — id вложения с сырыми данными исхода (сегодня только
      // split.children[]): второй раунд подтверждения split читает его,
      // не переразбирая человекочитаемый текст комментария (SplitConfirmed).
      // Значим только у отчётов, как и Next — у системных записей вложения
      // не бывает.
      Attachment string
      ConfigSHA  string // SHA конфига, возможно с суффиксом -dirty
  ```

  В `String()`:

  ```go
      fields := []string{"run:" + shorten(m.RunID), "role:" + m.Role, kind + ":" + value}
      // Пустое значение поля маркером не является вовсе (см. ParseMarker),
      // поэтому пустые next и attachment в строку не идут.
      if m.Next != "" && m.Outcome != "" {
          fields = append(fields, "next:"+m.Next)
      }
      if m.Attachment != "" && m.Outcome != "" {
          fields = append(fields, "attachment:"+m.Attachment)
      }
      return Prefix + strings.Join(append(fields, "config:"+shortenSHA(m.ConfigSHA)), " ") + "]"
  ```

  В `Valid()`:

  ```go
  func (m Marker) Valid() bool {
      oneKind := (m.Outcome == "") != (m.Event == "")
      return oneKind && m.Role != "" && m.RunID != "" &&
          (m.Next == "" || m.Outcome != "") && (m.Attachment == "" || m.Outcome != "")
  }
  ```

  В `ParseMarker`, в `switch key`:

  ```go
          case "next":
              m.Next = value
          case "attachment":
              m.Attachment = value
          case "config":
  ```

  Новые события — в блок `const` рядом с `EventPRSkipped`/`EventMergeConflict`:

  ```go
      // EventSplitCreated — CompleteSplits досоздал и связал всех детей
      // подтверждённого split-предложения, родитель закрыт.
      EventSplitCreated = "split-created"
      // EventSplitCreateFailed — попытка CompleteSplits на этом тикете не
      // удалась; идемпотентный опрос трекера делает повтор безопасным,
      // это не расход попытки агента.
      EventSplitCreateFailed = "split-create-failed"
  ```

  `SplitConfirmed` — рядом с `HasEvent`:

  ```go
  // SplitConfirmed решает, подтверждён ли split этой роли: считает все
  // комментарии-маркеры outcome:split от role в переписке — второй такой
  // маркер и есть подтверждение (тот же приём, что DESIGN.md §2.8 использует
  // для состояния pull request).
  //
  // Считает по всей истории, а не суффиксом с конца (в отличие от
  // eventStreak/ReturnRounds): между двумя split-маркерами роли лежит
  // системная запись event:human-reply с тем же Role (unblock() подписывает
  // её ролью, которой был задан вопрос) — суффиксный счёт оборвался бы на
  // ней, посчитав её «другой записью этой роли». Повторное «пересмотреть»
  // несколько раз подряд этот плоский счёт не отличает от подтверждения —
  // принятое упрощение этой волны, не забытый случай
  // (docs/notes/analyst-task-splitting.md, «открытый вопрос» волны 1).
  //
  // attachmentID берётся из тега **последнего** такого маркера: если человек
  // просил пересмотреть несколько раз, старые вложения остаются в истории,
  // актуально только последнее.
  func SplitConfirmed(comments []Comment, role string) (confirmed bool, attachmentID string) {
      count := 0
      for _, c := range comments {
          m, ok := MarkerOf(c.Body)
          if !ok || m.Role != role || m.Outcome != "split" {
              continue
          }
          count++
          attachmentID = m.Attachment
      }
      return count >= 2, attachmentID
  }
  ```

- [x] **Step 4: Тесты проходят**

  ```bash
  go test ./internal/tracker/... -run 'TestMarkerCarriesAttachment|TestParseMarkerRejectsForeignLines|TestSplitConfirmed' -v
  ```

- [x] **Step 5: Полный прогон пакета**

  ```bash
  go test ./internal/tracker/...
  ```

- [x] **Step 6: Commit**

  ```bash
  git add internal/tracker/marker.go internal/tracker/marker_test.go
  git commit -m "feat(tracker): add Marker.Attachment and SplitConfirmed"
  ```

---

## Task 10: `pipeline.go` — вложение на исход `split`

**Files:**
- Modify: `internal/pipeline/pipeline.go`
- Modify: `internal/pipeline/pipeline_test.go`

**Interfaces:**
- Consumes: `Marker.Attachment` (задача 9), `Tracker.AddAttachment` (задачи 3/6).
- Produces: `finish()` тегирует `outcome:split`-отчёт вложением.

- [x] **Step 1: Написать падающий тест**

  В `internal/pipeline/pipeline_test.go`, сразу после `TestTickSplitBlocksAndFlags`:

  ```go
  // Второе предложение находит своё же вложение по attachment:<id> из тега
  // последнего маркера split — тест проверяет round-trip через сам Tracker,
  // а не сравнением строк.
  func TestTickSplitAttachesRawChildren(t *testing.T) {
      o := newOffice(t)
      split := &runner.Split{Children: []runner.SplitChild{
          {ID: "category-crud", Title: "Category CRUD", Description: "Модель, миграция, CRUD категорий."},
          {ID: "transaction-crud", Title: "Transaction CRUD", Description: "Модель, миграция, CRUD операций.", DependsOn: []string{"category-crud"}},
      }}
      o.agent.result = runner.Result{
          Outcome: runner.OutcomeSplit, Summary: "Постановка описывает две сущности.", NextOwner: "human",
          Questions: []runner.Question{{ID: "Q1", Text: "Разбить на 2, как предложено?"}},
          Split:     split,
      }

      o.tick(t)

      task := o.get(t, "OFF-1")
      body := lastComment(t, task).Body
      marker, ok := tracker.MarkerOf(body)
      if !ok || marker.Attachment == "" {
          t.Fatalf("в маркере нет attachment:<id>:\n%s", body)
      }
      if !strings.Contains(body, "attachment:"+marker.Attachment) {
          t.Errorf("тег комментария не содержит attachment:%s:\n%s", marker.Attachment, body)
      }

      raw, err := o.tasks.GetAttachment(task.Key, marker.Attachment)
      if err != nil {
          t.Fatalf("вложение не прочитано: %v", err)
      }
      var got runner.Split
      if err := json.Unmarshal(raw, &got); err != nil {
          t.Fatalf("вложение не разобрано: %v", err)
      }
      if len(got.Children) != 2 || got.Children[1].DependsOn[0] != "category-crud" {
          t.Errorf("вложение потеряло данные: %+v", got)
      }
  }
  ```

  (`encoding/json` уже импортирован в `pipeline_test.go`.)

- [x] **Step 2: Убедиться, что тест падает**

  ```bash
  go test ./internal/pipeline/... -run TestTickSplitAttachesRawChildren -v
  ```

  Ожидаемо: FAIL — `marker.Attachment` пуст (`finish()` ещё не пишет вложение).

- [x] **Step 3: Реализовать**

  В `internal/pipeline/pipeline.go` добавить `"encoding/json"` в импорты
  (после `"context"`). В `finish()`, заменить:

  ```go
      by := tracker.ByRun(runID)
      marker := tracker.Marker{
          RunID: runID, Role: roleName, Outcome: string(result.Outcome),
          Next: result.NextOwner, ConfigSHA: o.ConfigSHA,
      }
  ```

  на:

  ```go
      by := tracker.ByRun(runID)

      // Вложение — раньше маркера: тегу attachment:<id> нужен уже готовый id,
      // а второй раунд подтверждения split читает его именно оттуда, не
      // переразбирая человекочитаемый текст комментария
      // (internal/pipeline/splits.go, tracker.SplitConfirmed).
      var attachmentID string
      if result.Outcome == runner.OutcomeSplit && result.Split != nil {
          data, err := json.Marshal(result.Split)
          if err != nil {
              return "", fmt.Errorf("вложение с разбивкой не собрано: %w", err)
          }
          attachmentID, err = o.Tracker.AddAttachment(task.Key, by, "split.json", data)
          if err != nil {
              return "", fmt.Errorf("вложение с разбивкой не сохранено: %w", err)
          }
      }

      marker := tracker.Marker{
          RunID: runID, Role: roleName, Outcome: string(result.Outcome),
          Next: result.NextOwner, Attachment: attachmentID, ConfigSHA: o.ConfigSHA,
      }
  ```

- [x] **Step 4: Тест проходит**

  ```bash
  go test ./internal/pipeline/... -run TestTickSplitAttachesRawChildren -v
  ```

- [x] **Step 5: Старые тесты на split не сломаны**

  ```bash
  go test ./internal/pipeline/... -run 'TestTickSplitBlocksAndFlags|TestTickSplitAttachesRawChildren' -v
  ```

- [x] **Step 6: Полный прогон пакета**

  ```bash
  go test ./internal/pipeline/...
  ```

- [x] **Step 7: Commit**

  ```bash
  git add internal/pipeline/pipeline.go internal/pipeline/pipeline_test.go
  git commit -m "feat(pipeline): attach raw split.children on outcome:split"
  ```

---

## Task 11: `internal/pipeline/splits.go` — `CompleteSplits`, счастливый путь

**Files:**
- Create: `internal/pipeline/splits.go`
- Create: `internal/pipeline/splits_test.go`

**Interfaces:**
- Consumes: `tracker.SplitConfirmed`, `Tracker.List`, `Tracker.Get`,
  `Tracker.GetAttachment`, `Tracker.FindByMarker`, `Tracker.CreateTask`,
  `Tracker.LinkDependsOn`, `o.record`, `o.move`, `o.skipProject`, `o.projects()`.
- Produces: `(*Office).CompleteSplits(ctx context.Context) error`,
  приватные `completeSplit`, `splitChildren`, `ensureChildren`,
  `linkChildren`, `closeSplitParent`, `splitFailed`, `splitChildMarker`.

- [x] **Step 1: Написать падающие тесты**

  Создать `internal/pipeline/splits_test.go`:

  ```go
  package pipeline

  import (
      "context"
      "slices"
      "strings"
      "testing"

      "github.com/kao73/virtual-office/internal/runner"
      "github.com/kao73/virtual-office/internal/tracker"
  )

  // tickAs прогоняет цикл названной роли и падает на инфраструктурной ошибке —
  // как tick() в pipeline_test.go, но с явной ролью: split-предложения
  // и их подтверждение приходят только от analyst, а не от implementer,
  // которого использует общий tick().
  func (o *office) tickAs(t *testing.T, role string) bool {
      t.Helper()
      worked, err := o.Tick(context.Background(), role)
      if err != nil {
          t.Fatalf("цикл %s не прошёл: %v", role, err)
      }
      return worked
  }

  // splitResult — стандартное предложение из двух детей с зависимостью:
  // общий фикстур для тестов CompleteSplits.
  func splitResult() runner.Result {
      return runner.Result{
          Outcome: runner.OutcomeSplit, Summary: "Постановка описывает две сущности.", NextOwner: "human",
          Questions: []runner.Question{{ID: "Q1", Text: "Разбить на 2, как предложено?"}},
          Split: &runner.Split{Children: []runner.SplitChild{
              {ID: "category-crud", Title: "Category CRUD", Description: "Модель, миграция, CRUD категорий."},
              {ID: "transaction-crud", Title: "Transaction CRUD", Description: "Модель, миграция, CRUD операций.", DependsOn: []string{"category-crud"}},
          }},
      }
  }

  // confirmSplit проводит OFF-1 через оба круга: предложение и подтверждение —
  // тем же путём, что и реальный аналитик, до состояния «два split-маркера
  // подряд, задача в Blocked».
  func confirmSplit(t *testing.T, o *office) {
      t.Helper()
      if err := o.tasks.Move("OFF-1", "Analysis"); err != nil {
          t.Fatalf("подготовка не удалась: %v", err)
      }
      o.agent.result = splitResult()
      if !o.tickAs(t, "analyst") {
          t.Fatal("первое предложение не взято в работу")
      }
      if err := o.tasks.AddComment("OFF-1", "human", "Да, разбивай."); err != nil {
          t.Fatalf("ответ не записан: %v", err)
      }
      if !o.tickAs(t, "analyst") {
          t.Fatal("подтверждение не взято в работу")
      }
  }

  func TestCompleteSplitsCreatesAndLinksChildren(t *testing.T) {
      o := newOffice(t)
      confirmSplit(t, o)

      if err := o.CompleteSplits(context.Background()); err != nil {
          t.Fatalf("проход не прошёл: %v", err)
      }

      parent := o.get(t, "OFF-1")
      if parent.Status != o.Workflow.PR.Merged {
          t.Errorf("статус родителя %q, ожидался %q", parent.Status, o.Workflow.PR.Merged)
      }

      category, err := o.tasks.Get("OFF-2")
      if err != nil {
          t.Fatalf("категория не создана: %v", err)
      }
      if category.Summary != "Category CRUD" || category.Status != "Analysis" {
          t.Errorf("категория заведена неверно: %+v", category)
      }
      if !slices.Contains(category.Labels, "split-child:OFF-1:category-crud") {
          t.Errorf("на категории нет метки: %v", category.Labels)
      }

      transaction, err := o.tasks.Get("OFF-3")
      if err != nil {
          t.Fatalf("операции не заведены: %v", err)
      }
      if !slices.Contains(transaction.DependsOn, "OFF-2") {
          t.Errorf("зависимость не записана: %v", transaction.DependsOn)
      }

      comment := lastComment(t, parent)
      if !strings.Contains(comment.Body, "OFF-2") || !strings.Contains(comment.Body, "OFF-3") {
          t.Errorf("в отчёте о закрытии нет ключей детей:\n%s", comment.Body)
      }
  }

  func TestCompleteSplitsSkipsUnconfirmed(t *testing.T) {
      o := newOffice(t)
      if err := o.tasks.Move("OFF-1", "Analysis"); err != nil {
          t.Fatalf("подготовка не удалась: %v", err)
      }
      o.agent.result = splitResult()
      if !o.tickAs(t, "analyst") {
          t.Fatal("первое предложение не взято в работу")
      }

      if err := o.CompleteSplits(context.Background()); err != nil {
          t.Fatalf("проход не прошёл: %v", err)
      }

      if _, err := o.tasks.Get("OFF-2"); err == nil {
          t.Error("дети не должны создаваться на первом предложении")
      }
      parent := o.get(t, "OFF-1")
      if parent.Status != "Blocked" {
          t.Errorf("статус родителя %q, ожидался Blocked", parent.Status)
      }
  }
  ```

- [x] **Step 2: Убедиться, что тесты падают**

  ```bash
  go test ./internal/pipeline/... -run 'TestCompleteSplits' -v
  ```

  Ожидаемо: ошибка компиляции — `CompleteSplits` не существует.

- [x] **Step 3: Реализовать**

  Создать `internal/pipeline/splits.go`:

  ```go
  package pipeline

  import (
      "context"
      "encoding/json"
      "fmt"
      "strings"

      "github.com/kao73/virtual-office/internal/runner"
      "github.com/kao73/virtual-office/internal/tracker"
  )

  // splitAnalystRole — роль, чьи split-предложения достраивает этот проход.
  // Разбиение — решение только analyst'а (roles/analyst/role.md): у
  // implementer/reviewer в графе для исхода split есть только формальный
  // маршрут ради Workflow.Validate(), они его не эмитят никогда.
  const splitAnalystRole = "analyst"

  // splitChildMarker — метка на созданном тикете-ребёнке, связывающая его
  // с родителем и с id из split.children[]. Одна строка, не две: JQL/
  // slices.Contains проще, а родитель+id вместе уже однозначны.
  func splitChildMarker(parentKey, childID string) string {
      return "split-child:" + parentKey + ":" + childID
  }

  // CompleteSplits — системный проход: досоздаёт и связывает тикеты-детей
  // подтверждённых split-предложений аналитика, по образцу Reap.
  //
  // Второе подряд outcome:split от analyst на одном тикете — подтверждение,
  // не первое предложение (tracker.SplitConfirmed). Раннер сам, без прогона
  // агента, создаёт недостающих детей, связывает их по depends_on и
  // переводит родителя в терминальный статус — тем же путём, каким prSkipped
  // уже уводит задачу без pull request (нет кода, нет PR, но офис с задачей
  // закончил).
  func (o *Office) CompleteSplits(ctx context.Context) error {
      flow, err := o.Workflow.Role(splitAnalystRole)
      if err != nil {
          // Графа без analyst не бывает в реальном workflow.yaml, но
          // синтетические тестовые графы могут его не иметь — тогда
          // достраивать split-предложения некому, и это не повод падать.
          return nil
      }

      for _, project := range o.projects() {
          refs, err := o.Tracker.List(project, []string{flow.Blocked()})
          if o.skipProject(project, err) {
              continue
          }
          if err != nil {
              return err
          }
          for _, ref := range refs {
              task, err := o.Tracker.Get(ref.Key)
              if err != nil {
                  return err
              }
              confirmed, attachmentID := tracker.SplitConfirmed(task.Comments, splitAnalystRole)
              if !confirmed {
                  continue
              }
              if err := o.completeSplit(task, attachmentID); err != nil {
                  return err
              }
          }
      }
      return nil
  }

  // completeSplit достраивает одно подтверждённое split-предложение: читает
  // вложение, доводит до конца создание детей и связей, закрывает родителя.
  //
  // Ошибка любого шага уходит системной записью в тикет (splitFailed) и не
  // прерывает обход остальных задач в CompleteSplits — тем же приёмом, что
  // Reap не роняет весь проход из-за одной беды.
  func (o *Office) completeSplit(task tracker.Task, attachmentID string) error {
      children, err := o.splitChildren(task, attachmentID)
      if err != nil {
          return o.splitFailed(task, fmt.Sprintf("вложение %s не прочитано: %v", attachmentID, err))
      }

      byID, err := o.ensureChildren(task, children)
      if err != nil {
          return o.splitFailed(task, fmt.Sprintf("тикеты-дети не досозданы: %v", err))
      }
      if err := o.linkChildren(children, byID); err != nil {
          return o.splitFailed(task, fmt.Sprintf("связи depends_on не записаны: %v", err))
      }

      keys := make([]string, len(children))
      for i, child := range children {
          keys[i] = byID[child.ID]
      }
      return o.closeSplitParent(task, keys)
  }

  // splitChildren скачивает вложение подтверждённого split и разбирает его
  // в исходный список подзадач.
  func (o *Office) splitChildren(task tracker.Task, attachmentID string) ([]runner.SplitChild, error) {
      data, err := o.Tracker.GetAttachment(task.Key, attachmentID)
      if err != nil {
          return nil, err
      }
      var split runner.Split
      if err := json.Unmarshal(data, &split); err != nil {
          return nil, fmt.Errorf("вложение не разобрано: %w", err)
      }
      return split.Children, nil
  }

  // ensureChildren заводит недостающих детей и отвечает ключом каждого по
  // id из split.children[]. Идемпотентно: перед каждым созданием — опрос
  // трекера по метке, а не хранимый флаг, так прерванная на середине пачка
  // чинится следующим тиком сама, без дублей.
  func (o *Office) ensureChildren(task tracker.Task, children []runner.SplitChild) (map[string]string, error) {
      keys := make(map[string]string, len(children))
      for _, child := range children {
          marker := splitChildMarker(task.Key, child.ID)
          found, err := o.Tracker.FindByMarker(task.Project, marker)
          if err != nil {
              return nil, err
          }
          if len(found) > 0 {
              keys[child.ID] = found[0].Key
              continue
          }

          ref, err := o.Tracker.CreateTask(task.Project, tracker.TaskInput{
              Summary: child.Title, Description: child.Description, Labels: []string{marker},
          })
          if err != nil {
              return nil, err
          }
          keys[child.ID] = ref.Key
      }
      return keys, nil
  }

  // linkChildren связывает уже существующих детей по depends_on. Отдельным
  // подпроходом после того, как **все** дети существуют: ребёнок может
  // зависеть от того, кто в split.children[] идёт позже него, и связывать
  // раньше, чем существуют оба конца, нечем.
  func (o *Office) linkChildren(children []runner.SplitChild, keys map[string]string) error {
      by := tracker.BySystem()
      for _, child := range children {
          for _, dep := range child.DependsOn {
              if err := o.Tracker.LinkDependsOn(keys[child.ID], keys[dep], by); err != nil {
                  return err
              }
          }
      }
      return nil
  }

  // closeSplitParent сообщает о готовых детях и закрывает родителя — тем же
  // терминальным статусом, что и prSkipped: задача, которой нечего сливать,
  // заканчивает жизнь так же, как слитая.
  func (o *Office) closeSplitParent(task tracker.Task, keys []string) error {
      runID, err := runner.NewRunID()
      if err != nil {
          return err
      }
      by, to := tracker.BySystem(), o.Workflow.PR.Merged
      if err := o.record(task.Key, by, tracker.Marker{
          RunID: runID, Role: splitAnalystRole, Event: tracker.EventSplitCreated, ConfigSHA: o.ConfigSHA,
      }, fmt.Sprintf("Разбита на: %s. Тикеты-дети созданы и связаны по depends_on автоматически, "+
          "задача уходит в %s.", strings.Join(keys, ", "), to)); err != nil {
          return err
      }
      o.logf("%s: разбита на %s, уходит в %s", task.Key, strings.Join(keys, ", "), to)
      return o.move(task, by, to)
  }

  // splitFailed пишет системную запись о неудавшейся попытке достроить
  // split. Родитель остаётся в Blocked, а не уходит к человеку: идемпотентный
  // опрос трекера делает повтор безопасным всегда, и это не прогон агента —
  // тратить attempts или звать человека здесь не за что (тот же довод, что
  // у Archive/открытия PR). Следующий цикл Loop (или ручной
  // runner complete-splits) попробует снова.
  func (o *Office) splitFailed(task tracker.Task, text string) error {
      runID, err := runner.NewRunID()
      if err != nil {
          return err
      }
      if err := o.record(task.Key, tracker.BySystem(), tracker.Marker{
          RunID: runID, Role: splitAnalystRole, Event: tracker.EventSplitCreateFailed, ConfigSHA: o.ConfigSHA,
      }, text); err != nil {
          return err
      }
      o.logf("%s: %s", task.Key, text)
      return nil
  }
  ```

- [x] **Step 4: Тесты проходят**

  ```bash
  go test ./internal/pipeline/... -run 'TestCompleteSplits' -v
  ```

- [x] **Step 5: Полный прогон пакета**

  ```bash
  go test ./internal/pipeline/...
  ```

- [x] **Step 6: Commit**

  ```bash
  git add internal/pipeline/splits.go internal/pipeline/splits_test.go
  git commit -m "feat(pipeline): add CompleteSplits happy path"
  ```

---

## Task 12: `CompleteSplits` — устойчивость к прерванной пачке

**Files:**
- Modify: `internal/pipeline/splits_test.go`

Код `ensureChildren`/`FindByMarker` из задачи 11 уже идемпотентен по
построению — эта задача пишет тест, доказывающий это конкретным сценарием
из design doc (`TestCompleteSplitsResumesInterruptedBatch`), а не меняет код.

**Interfaces:**
- Consumes: всё из задачи 11, `Tracker.CreateTask` напрямую (для подготовки
  «уже созданного ранее» ребёнка).

- [x] **Step 1: Написать тест**

  В `internal/pipeline/splits_test.go`:

  ```go
  func TestCompleteSplitsResumesInterruptedBatch(t *testing.T) {
      o := newOffice(t)
      confirmSplit(t, o)

      // Прошлая попытка успела создать только первого ребёнка — так
      // выглядит пачка, прерванная на середине.
      if _, err := o.tasks.CreateTask("OFF", tracker.TaskInput{
          Summary: "Category CRUD", Description: "Модель, миграция, CRUD категорий.",
          Labels: []string{"split-child:OFF-1:category-crud"},
      }); err != nil {
          t.Fatalf("подготовка не удалась: %v", err)
      }

      if err := o.CompleteSplits(context.Background()); err != nil {
          t.Fatalf("проход не прошёл: %v", err)
      }

      category, err := o.tasks.FindByMarker("OFF", "split-child:OFF-1:category-crud")
      if err != nil {
          t.Fatalf("поиск не удался: %v", err)
      }
      if len(category) != 1 {
          t.Errorf("категория задвоена: %+v", category)
      }

      transaction, err := o.tasks.FindByMarker("OFF", "split-child:OFF-1:transaction-crud")
      if err != nil {
          t.Fatalf("поиск не удался: %v", err)
      }
      if len(transaction) != 1 {
          t.Errorf("операции не досозданы или задвоены: %+v", transaction)
      }

      linked, err := o.tasks.Get(transaction[0].Key)
      if err != nil {
          t.Fatalf("операции не прочитаны: %v", err)
      }
      if !slices.Contains(linked.DependsOn, category[0].Key) {
          t.Errorf("зависимость не связана после докатки: %v", linked.DependsOn)
      }

      parent := o.get(t, "OFF-1")
      if parent.Status != o.Workflow.PR.Merged {
          t.Errorf("статус родителя %q, ожидался %q", parent.Status, o.Workflow.PR.Merged)
      }
  }
  ```

- [x] **Step 2: Проверить, что тест ПРОХОДИТ уже сейчас**

  ```bash
  go test ./internal/pipeline/... -run TestCompleteSplitsResumesInterruptedBatch -v
  ```

  Ожидаемо: PASS без изменений в `splits.go` — идемпотентность заложена в
  `ensureChildren` задачи 11 (`FindByMarker` перед каждым `CreateTask`).
  Если тест падает — значит `ensureChildren` в задаче 11 реализован не так,
  как в этом плане (например, пропущена проверка `FindByMarker` перед
  созданием); вернуться к задаче 11 и сверить код дословно.

- [x] **Step 3: Commit**

  ```bash
  git add internal/pipeline/splits_test.go
  git commit -m "test(pipeline): prove CompleteSplits resumes an interrupted batch"
  ```

---

## Task 13: `CompleteSplits` — сбой не прерывает обход остальных задач

**Files:**
- Modify: `internal/pipeline/splits_test.go`

**Interfaces:**
- Produces: `flakyCreate` — тестовый трекер, роняющий `CreateTask` для
  одного конкретного ребёнка (по образцу `flakyClaim` в `pipeline_test.go`).

- [x] **Step 1: Написать падающий тест**

  В `internal/pipeline/splits_test.go` добавить `"errors"` и
  `"github.com/kao73/virtual-office/internal/tracker/mock"` в импорты, затем:

  ```go
  // flakyCreate роняет CreateTask для ребёнка с данным заголовком: так
  // выглядит частичный сбой пакетного создания.
  type flakyCreate struct {
      *mock.Tracker
      failOn string
  }

  func (f *flakyCreate) CreateTask(project string, input tracker.TaskInput) (tracker.TaskRef, error) {
      if input.Summary == f.failOn {
          return tracker.TaskRef{}, errors.New("сеть недоступна")
      }
      return f.Tracker.CreateTask(project, input)
  }

  func TestCompleteSplitsRecordsFailureNoticeAndContinues(t *testing.T) {
      o := newOffice(t)
      confirmSplit(t, o)

      // Второй, независимый подтверждённый тикет — доказывает, что беда
      // на OFF-1 не роняет весь проход CompleteSplits.
      o.add("OFF-9", "Analysis")
      o.agent.result = runner.Result{
          Outcome: runner.OutcomeSplit, Summary: "Другая постановка.", NextOwner: "human",
          Questions: []runner.Question{{ID: "Q1", Text: "Разбить на 2, как предложено?"}},
          Split: &runner.Split{Children: []runner.SplitChild{
              {ID: "one", Title: "One", Description: "Первая половина."},
              {ID: "two", Title: "Two", Description: "Вторая половина."},
          }},
      }
      worked, err := o.Tick(context.Background(), "analyst")
      if err != nil || !worked {
          t.Fatalf("предложение OFF-9 не взято в работу: worked=%v err=%v", worked, err)
      }
      if err := o.tasks.AddComment("OFF-9", "human", "Да, разбивай."); err != nil {
          t.Fatalf("ответ не записан: %v", err)
      }
      if worked, err := o.Tick(context.Background(), "analyst"); err != nil || !worked {
          t.Fatalf("подтверждение OFF-9 не взято в работу: worked=%v err=%v", worked, err)
      }

      o.useTracker(&flakyCreate{Tracker: o.tasks, failOn: "Category CRUD"})

      if err := o.CompleteSplits(context.Background()); err != nil {
          t.Fatalf("проход не должен падать целиком: %v", err)
      }

      parent := o.get(t, "OFF-1")
      if parent.Status != "Blocked" {
          t.Errorf("статус родителя %q, ожидался Blocked — сбой не должен двигать задачу", parent.Status)
      }
      comment := lastComment(t, parent)
      marker, ok := tracker.MarkerOf(comment.Body)
      if !ok || marker.Event != tracker.EventSplitCreateFailed {
          t.Errorf("нет записи о сбое:\n%s", comment.Body)
      }

      other := o.get(t, "OFF-9")
      if other.Status != o.Workflow.PR.Merged {
          t.Errorf("вторая задача %q, ожидался %q — беда первой не должна её касаться",
              other.Status, o.Workflow.PR.Merged)
      }
  }
  ```

  Важен порядок: OFF-9 заводится и проводится через оба круга **после**
  того, как `confirmSplit` уже увела OFF-1 из очереди `analyst`'а (обратно
  в `Blocked`) — иначе `claim()` в `tickAs`/`Tick` будет раз за разом
  забирать OFF-1 первым (ключи сортируются по возрастанию), и до OFF-9
  ни один тик не доберётся.

- [x] **Step 2: Убедиться, что тест падает**

  ```bash
  go test ./internal/pipeline/... -run TestCompleteSplitsRecordsFailureNoticeAndContinues -v
  ```

  Ожидаемо: FAIL — до этой задачи `splits.go` уже пишет `EventSplitCreateFailed`
  (задача 11), так что упасть тест может только на порядке подготовки или
  отсутствии `mock` в импортах; если тест падает по другой причине —
  разобраться, прежде чем продолжать (это не «ожидаемый provisional fail»,
  а сигнал, что фикстура собрана неверно).

- [x] **Step 3: Тест проходит**

  ```bash
  go test ./internal/pipeline/... -run TestCompleteSplitsRecordsFailureNoticeAndContinues -v
  ```

- [x] **Step 4: Полный прогон пакета**

  ```bash
  go test ./internal/pipeline/...
  ```

- [x] **Step 5: Commit**

  ```bash
  git add internal/pipeline/splits_test.go
  git commit -m "test(pipeline): CompleteSplits failure notice does not stop the pass"
  ```

---

## Task 14: Подключить `CompleteSplits` к `Loop` и CLI `runner complete-splits`

Design doc называет это найденным пробелом Open-фазы: без отдельного
триггера «сам починится следующим тиком» неверно — `CompleteSplits` без
вызывающего кода никогда бы не исполнился. `tasks.md` этот пункт отдельной
строкой не называет (он появился при более глубокой технической проработке
в Design Doc) — план добавляет его явно, потому что без него вся работа
задач 9–13 остаётся мёртвым кодом.

**Files:**
- Modify: `internal/pipeline/pipeline.go`
- Modify: `cmd/runner/office.go`
- Modify: `cmd/runner/main.go`

**Interfaces:**
- Consumes: `(*Office).CompleteSplits` (задача 11).
- Produces: `completeSplitsCommand(args []string, out io.Writer) error`;
  подкоманда `runner complete-splits`.

- [ ] **Step 1: Подключить в `Loop`**

  В `internal/pipeline/pipeline.go`, `Loop`:

  ```go
  func (o *Office) Loop(ctx context.Context, every time.Duration, roleName string) error {
      for {
          if err := o.Reap(ctx); err != nil {
              o.logf("reap: %v", err)
          }
          if err := o.CompleteSplits(ctx); err != nil {
              o.logf("complete-splits: %v", err)
          }
          if err := o.tickOnce(ctx, roleName); err != nil {
              o.logf("tick: %v", err)
          }
  ```

  (Остальное тело `Loop` не меняется.)

  Прецедента отдельного unit-теста на то, что `Loop` зовёт `Reap`, в
  кодовой базе нет (`Loop` не тестируется на уровне вызовов — только через
  `Reap`/`tickOnce` по отдельности, что уже покрыто существующими тестами и
  задачами 11–13 этого плана). Не заводить такой тест и здесь — проверка
  этого шага: сборка и `go vet`.

- [ ] **Step 2: CLI-команда**

  В `cmd/runner/office.go`, сразу после `reapCommand`:

  ```go
  // completeSplitsCommand достраивает и связывает тикеты-детей подтверждённых
  // split-предложений — отдельно от loop, вручную или по cron (по образцу reap).
  func completeSplitsCommand(args []string, out io.Writer) error {
      o, err := office(flags("complete-splits"), args, out)
      if err != nil {
          return err
      }
      return o.CompleteSplits(context.Background())
  }
  ```

  В `cmd/runner/main.go`, в `switch args[0]`, после `case "reap":`:

  ```go
      case "reap":
          return reapCommand(args[1:], os.Stdout)
      case "complete-splits":
          return completeSplitsCommand(args[1:], os.Stdout)
  ```

  В константе `usage`, после строки `runner reap`:

  ```
    runner reap                   вернуть задачи с истёкшей арендой
    runner complete-splits        достроить и связать тикеты-детей подтверждённого split
  ```

- [ ] **Step 3: Сборка и статическая проверка**

  ```bash
  go build ./... && go vet ./...
  ```

- [ ] **Step 4: Ручная проверка CLI на файловом трекере**

  ```bash
  export OFFICE_HOME=$(mktemp -d)
  go run ./cmd/runner mock add OFF-1 --summary "Тест" --status Analysis --project OFF
  go run ./cmd/runner complete-splits
  ```

  Ожидаемо: команда завершается без ошибки (задача не подтверждена — проход
  просто ничего не находит и молчит). Это не автоматический тест, а
  дымовая проверка того, что подкоманда реально вызывается и не падает на
  пустом хозяйстве.

- [ ] **Step 5: Полный прогон тестов модуля**

  ```bash
  go test ./...
  ```

- [ ] **Step 6: Commit**

  ```bash
  git add internal/pipeline/pipeline.go cmd/runner/office.go cmd/runner/main.go
  git commit -m "feat(runner): wire CompleteSplits into Loop and add complete-splits CLI"
  ```

---

## Task 15: `roles/analyst/role.md` — убрать мёртвые ветки резюме-цикла

Design doc, решение №6: обе нижние ветки резюме-цикла («подзадачи уже
заведены?» и «что дальше с этим тикетом?») становятся недостижимы разом —
тикеты появляются раньше, чем человек успел бы ответить на вопрос о них.
Убираются вместе, не по отдельности.

**Files:**
- Modify: `roles/analyst/role.md`

- [ ] **Step 1: Убрать две нижние ветки, поправить первую**

  Заменить блок (от `**Резюмируешь тикет...` до `...новый заход на «крупная
  ли задача».`, три ветки резюме-цикла целиком) на:

  ```markdown
  **Резюмируешь тикет, на котором ты сама (в прежнем прогоне) уже задавала
  вопрос «разбить на N, как предложено?», и `context.md` несёт ответ
  человека?** Не исследуй заново — сразу разбирай ответ по правилу ниже:

  **Прошлый вопрос был «разбить на N, как предложено?»:**
  - подтверждает (выбран вариант «да»/«разбить как предложено», либо прямой
    текст того же смысла) → снова исход `split` (или уточнённым, если в
    ответе была новая информация про сами подзадачи), с **тем же самым**
    вопросом «разбить на N, как предложено?» — не изобретай новый вопрос
    про заведение подзадач: их досоздаёт и связывает раннер сам,
    детерминированным кодом, без нового прогона на этот шаг. В `summary`
    так и скажи — что тикеты-дети появятся и свяжутся автоматически;
  - отклоняет («нет», «вести как одну задачу») → решение человека
    перекрывает эвристику ниже: продолжай с шага 1, `comet native new` на
    исходную постановку целиком, дальше — обычный Shape, как будто ранней
    проверки размера не было вовсе;
  - что-то ещё — это новая информация, а не да/нет/другое: реши между
    `split` (уточнённым) и `needs_human` по общей таблице «Исходы» ниже,
    ровно как для любого неожиданного ответа на вопрос роли, не как с
    отдельным случаем. Ни один из вариантов не требует `brief.md`: этот
    вопрос задан до `comet native new`, и на нём его ещё нет.
  ```

- [ ] **Step 2: Поправить раздел «Исходы»**

  В разделе `## Исходы`, пункт про `split`, заменить:

  ```markdown
  - задача крупная и её надо резать (реши это до входа в Comet Native, см.
    «Comet Native: фаза Shape» выше) → `split` с вопросом «разбить на N, как
    предложено?» и структурированным предложением в `split.children[]`
    (`id`/`title`/`description`/`depends_on`, контракт
    `docs/contracts/agent-io.md`) — спеки на этот момент ещё нет, разбивать её
    не из чего. Подзадачи заводит человек: офис в трекере ничего не создаёт.
    Резюме после подтверждённого «да» — тоже `split`, но с другим вопросом
    («подзадачи заведены?», см. «Comet Native: фаза Shape» выше) — не повторяй
    вопрос, на который уже ответили. Это не длится бесконечно: подтверждение,
    что подзадачи заведены, — `needs_human`, не `split` (см. там же); резюме
    после этого выхода следует указанию человека, не решает размер заново;
  ```

  на:

  ```markdown
  - задача крупная и её надо резать (реши это до входа в Comet Native, см.
    «Comet Native: фаза Shape» выше) → `split` с вопросом «разбить на N, как
    предложено?» и структурированным предложением в `split.children[]`
    (`id`/`title`/`description`/`depends_on`, контракт
    `docs/contracts/agent-io.md`) — спеки на этот момент ещё нет, разбивать её
    не из чего. Тикеты по этому предложению ты не заводишь: если человек
    подтвердит разбиение во второй раз подряд, раннер сам создаст и свяжет
    тикеты-детей и переведёт эту задачу в `Done` — без нового прогона на
    этот шаг. Резюме после подтверждённого «да» — снова `split`, с тем же
    вопросом (см. «Comet Native: фаза Shape» выше), не с новым про заведение
    подзадач;
  ```

- [ ] **Step 3: Проверить, что упоминаний мёртвых веток не осталось**

  ```bash
  grep -n "уже заведены\|что дальше с этим тикетом" roles/analyst/role.md
  ```

  Ожидаемо: пусто.

- [ ] **Step 4: Golden-кейсы роли не сломаны**

  ```bash
  ./bin/eval-roles --role analyst
  ```

  Ожидаемо: все существующие кейсы `analyst` зелёные — правка резюме-цикла
  не должна задеть их косвенно (никакой golden-кейс волны 1 не проверял
  вторую ветку резюме, но общая формулировка вопроса `split` могла попасть
  в сравнение текста; если какой-то кейс упал именно на тексте вопроса —
  поправить golden-файл кейса тем же текстом, что теперь пишет role.md, не
  откатывать правку).

- [ ] **Step 5: Commit**

  ```bash
  git add roles/analyst/role.md
  git commit -m "docs(analyst): remove dead resume branches after split auto-creation"
  ```

---

## Task 16: Сверить `role.md` с дельта-спекой перед архивацией

Это задача сверки, а не кодирования: дельта-спека
`docs/openspec/changes/split-autocreate-tickets/specs/role-native-workflow/spec.md`
уже написана Open-фазой; правка `role.md` в задаче 15 обязана описывать то
же поведение. Слияние дельты в живую `docs/openspec/specs/role-native-workflow/spec.md`
— работа фазы Archive (`comet-archive`), не этой задачи.

**Files:** нет кодовых изменений; при найденном расхождении — доработка
`roles/analyst/role.md` (см. критерий ниже).

- [ ] **Step 1: Прочитать оба текста рядом**

  ```bash
  cat docs/openspec/changes/split-autocreate-tickets/specs/role-native-workflow/spec.md
  sed -n '1,90p' roles/analyst/role.md
  ```

- [ ] **Step 2: Сверить по требованиям дельты**

  Дельта-спека («MODIFIED Requirements», сценарий «Confirmed split ends the
  resumed run without invoking Comet Native») требует:

  1. Подтверждение снова даёт `outcome: split`, `comet native new` не
     вызывается — **закрыто**: ветка «подтверждает» в задаче 15 оставляет
     `comet native new` невызванным.
  2. `summary` сообщает, что раннер сам создаст и свяжет тикеты-детей —
     **закрыто**: явный текст «В `summary` так и скажи — что тикеты-дети
     появятся и свяжутся автоматически» в правке задачи 15.
  3. Новый вопрос про «созданы ли подзадачи» не изобретается, вопрос —
     тот же самый — **закрыто**: правка задачи 15 явно требует «тем же
     самым» вопросом и запрещает «новый вопрос про заведение подзадач».
  4. Сценарий «Declined split proceeds with ordinary Shape» — ветка
     «отклоняет» не меняется этим планом вовсе, значит остаётся как была —
     **закрыто** без действий.

  Если сверка находит расхождение (например, будущая правка `role.md`
  случайно оставила текст «подзадачи заводит человек» где-то ещё) — вернуться
  к `roles/analyst/role.md` и поправить, затем повторить `grep`:

  ```bash
  grep -n "заводит человек\|заведёт человек" roles/analyst/role.md
  ```

  Ожидаемо после задачи 15: не находит упоминаний про заведение подзадач
  человеком в контексте `split` (единственное допустимое использование
  слова «человек» рядом с подзадачами — в разделе «Comet Native: фаза
  Shape», где написано «раннер сам создаст и свяжет», а не «человек»).

- [ ] **Step 3: Зафиксировать результат сверки**

  Если расхождений не найдено — коммитить нечего, задача закрывается без
  изменений в репозитории (это ожидаемый исход: дельта-спека и роль писались
  по одному и тому же Design Doc). Если правка потребовалась — она уже
  закоммичена шагом 2.

---

## Task 17: Полная верификация

**Files:** нет кодовых изменений (кроме исправления найденных проблем).

- [ ] **Step 1: Сборка, статический анализ, форматирование**

  ```bash
  go build ./...
  go vet ./...
  gofmt -l .
  ```

  Критерий: все три команды без вывода ошибок; `gofmt -l .` — пустой вывод
  (иначе прогнать `gofmt -w` на перечисленных файлах и повторить).

- [ ] **Step 2: Весь модуль тестами**

  ```bash
  go test ./...
  ```

  Критерий: все пакеты `ok`, ни одного `FAIL`/`SKIP`, которого не было до
  начала этого плана.

- [ ] **Step 3: Golden-кейсы ролей**

  ```bash
  ./bin/eval-roles --role analyst
  ```

  Критерий: все кейсы `analyst` зелёные (см. также Task 15, Step 4 — это
  повторный прогон после ВСЕХ изменений плана, не только правки `role.md`).

- [ ] **Step 4: Проверить отсутствие временных заглушек**

  ```bash
  grep -rn "пока не реализовано\|TBD\|TODO" internal/tracker/ internal/pipeline/splits.go
  ```

  Критерий: пусто.

- [ ] **Step 5: Commit (если что-то потребовало правки)**

  Если шаги 1–4 прошли сразу — коммитить нечего. Если потребовалась правка
  (например, `gofmt -w` что-то переформатировал) — закоммитить точечно:

  ```bash
  git add -u
  git commit -m "chore: gofmt and vet cleanup after split-autocreate-tickets"
  ```

---

## Task 18: Живая проверка на реальном трекере

Финальные шаги — ручная/полу-ручная проверка на реальном JIRA-полигоне
(Server 8.13) или на существующем `EXP`-тикете с уже предложенным разбиением
(волна 1). Это не юнит-тесты: поведение здесь зависит от реального
состояния конкретного инстанса, которое нельзя заранее знать из кода.

**Files:**
- Modify: `docs/notes/analyst-task-splitting.md`

- [ ] **Step 1: Двойное подтверждение на реальном тикете**

  На реальном проекте (или на `EXP`-тикете, где `analyst` волны 1 уже
  предложил разбиение и ждёт ответа) выполнить:

  1. Ответить на вопрос подтверждающе (например, комментарием «Да, разбивай
     как предложено» под учёткой человека).
  2. Дать `analyst` отработать ещё раз (`runner tick --role analyst` или
     дождаться `loop`) — должен появиться **второй** `outcome: split`
     маркер с тем же вопросом, без нового вопроса про заведение подзадач.
  3. Запустить `runner complete-splits` (или дождаться следующего цикла
     `loop`, который зовёт его сам).

  Критерий успеха: тикеты-дети появились в трекере, связаны по `depends_on`
  (видно в JIRA как issuelinks нужного типа), родительский тикет ушёл в
  `Done`, и всё это — **без единого прогона агента** на сам шаг создания
  (в переписке родителя нет отчёта `analyst`/`implementer` между
  подтверждением и записью `event:split-created`).

- [ ] **Step 2: Прерванная пачка на реальном инстансе**

  Искусственно прервать создание партии на середине — например, временно
  отозвать креды учётки офиса (`unset JIRA_PASSWORD` в среде, где идёт
  `complete-splits`, или временно испортить `depends_on_link` в
  `tracker.yaml` на заведомо несуществующее имя) после того, как первый
  ребёнок уже создан, затем вернуть креды/конфигурацию в порядок и прогнать
  `runner complete-splits` снова.

  Критерий успеха: досоздаются только недостающие тикеты, ни одного
  дубликата с той же меткой `split-child:<PARENT>:<id>`; связи по
  `depends_on`, которые не успели встать до сбоя, встают на повторном
  проходе.

- [ ] **Step 3: Записать находки**

  Дописать раздел «Волна 2 — брейншторм 2026-09-05, подраздел «Change 1»»
  файла `docs/notes/analyst-task-splitting.md`: что подтвердилось как
  спроектировано, какой реальный тип issuelink использован
  (`depends_on_link` из `tracker.yaml` этого полигона), понадобилась ли
  защита от повторного `POST /issueLink` (Task 8) и какой конкретно текст
  ошибки/поведение наблюдались при прерывании. Обновить таблицу «Статус по
  кускам» в конце того же файла: строки 2, 3 и «5 — авто-`Done`» переводятся
  из «спроектировано... готово к спеку и плану» в «реализовано и
  подтверждено живьём», тем же приёмом, что уже сделан для волны 1
  (`git log` — коммит `docs(notes): mark wave 1 splitting as shipped and
  live-verified` как образец формулировок).

  ```bash
  git add docs/notes/analyst-task-splitting.md
  git commit -m "docs(notes): mark Change 1 (split auto-create) as shipped and live-verified"
  ```
