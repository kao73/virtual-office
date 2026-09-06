---
change: ticket-attachment-visibility
design-doc: docs/superpowers/specs/2026-09-06-ticket-attachment-visibility-design.md
base-ref: a3a504caeb3f24756e0be5f5712fad75accbea1d
---

# Видимость вложений тикета — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans
> to implement this plan task-by-task (build_mode=direct — inline execution
> in the main session, no subagent dispatch). Steps use checkbox (`- [ ]`)
> syntax for tracking.

**Goal:** Закрыть дыру видимости вложений тикета — материализовать
человеческие вложения в рабочую папку агента и научить split-детей
наследовать вложения родителя, идемпотентно.

**Architecture:** `tracker.Task.Attachments` заполняется бесплатно из уже
идущего `Get()`; `Office.humanAttachments` — единственный фильтр служебных
вложений, используемый в трёх местах (упоминание в `task.md`,
материализация своей задачи, наследование детьми); `runner.PrepareInput`
кладёт вложения настоящими файлами в `.agent/attachments/`.

**Tech Stack:** Go, существующие пакеты `internal/tracker`,
`internal/runner`, `internal/pipeline`.

**Spec:** `docs/superpowers/specs/2026-09-06-ticket-attachment-visibility-design.md`
(глубокий дизайн); `docs/openspec/changes/ticket-attachment-visibility/design.md`
(высокоуровневые решения и альтернативы).

## Global Constraints

- Служебные вложения (сегодня — только `runner.SplitAttachmentName =
  "split.json"`) никогда не попадают в `.agent/attachments/` и не
  упоминаются в `task.md` — фильтрует только `Office.humanAttachments`.
- Наследование вложений детьми (`ensureChildAttachments`) обязано быть
  идемпотентным на **каждом** проходе `CompleteSplits`, не только при
  первом создании ребёнка — сверка по имени вложения через свежий `Get()`.
- Санитация имени файла вложения — `filepath.Base` плюс явная проверка
  `""`/`"."`/`".."`/голого разделителя путей.

---

## Уже сделано (группы 1–2 tasks.md, эта же сессия, коммиты ещё не сделаны — работает в дереве)

Не переделывать, не проверять заново — только держать в контексте:

- `tracker.AttachmentRef{ID, Name}`, `Task.Attachments []AttachmentRef`
  (`internal/tracker/tracker.go`).
- `jira.toTask` разбирает `fields["attachment"]`
  (`internal/tracker/jira/jira.go`), тест `TestGetMapsAttachments` зелёный.
- `mock.AddAttachment` хранит имя вложения сайдкар-файлом `<id>.yaml`,
  `Get()` его читает (`internal/tracker/mock/mock.go`), тест
  `TestAddAttachmentPreservesNameInGet` зелёный.
- `runner.SplitAttachmentName`, `DirAttachments`
  (`internal/runner/agentio.go`).
- `runner.InputAttachment`, `Input.Attachments`, `writeAttachments`
  (`internal/runner/input.go`) — санитация имени, разрешение коллизий,
  очистка устаревших вложений. Тесты `TestPrepareInputWritesAttachments`,
  `TestPrepareInputSanitizesAttachmentName`,
  `TestPrepareInputDisambiguatesDuplicateAttachmentNames`,
  `TestPrepareInputClearsStaleAttachments` — все зелёные.
- `Office.humanAttachments`, `Office.fetchAttachments`, правка `taskBody`,
  правка `Office.work()` (`internal/pipeline/pipeline.go`) — код написан,
  свой тест ещё не подтверждён (Task 1 ниже).
- `Office.ensureChildAttachments`, подключено в `completeSplit`
  (`internal/pipeline/splits.go`) — код написан, тестов нет (Task 2–4
  ниже).

---

### Task 1: Тест материализации вложений в обычном прогоне (tasks.md 3.3)

**Files:**
- Modify: `internal/pipeline/pipeline_test.go` (добавить тест рядом с
  `TestTickFeedsAgentTaskAndContext`)

**Interfaces:**
- Consumes: `o.tasks.AddAttachment(key, tracker.BySystem(), name, data)`
  (мок-трекер, уже существует), `runner.SplitAttachmentName`,
  `runner.Dir`, `runner.DirAttachments`, `runner.FileTask` (уже
  существуют), `o.tick(t)` и `o.agent.seen` (существующий тестовый
  харнесс пакета `pipeline`).
- Produces: ничего для последующих задач — независимый тест.

- [x] **Step 1: Добавить тест**

Вставить сразу после `TestTickFeedsAgentTaskAndContext` в
`internal/pipeline/pipeline_test.go`:

```go
// TestTickMaterializesHumanAttachmentsButNotSplitJSON доказывает, что
// человеческое вложение задачи попадает в рабочую папку агента настоящим
// файлом и упоминается в постановке, а служебное (split.json — переписка
// раннера с самим собой) — нет ни там, ни там.
func TestTickMaterializesHumanAttachmentsButNotSplitJSON(t *testing.T) {
	o := newOffice(t)
	if _, err := o.tasks.AddAttachment("OFF-1", tracker.BySystem(), "schema.png", []byte("данные схемы")); err != nil {
		t.Fatalf("вложение не добавлено: %v", err)
	}
	if _, err := o.tasks.AddAttachment("OFF-1", tracker.BySystem(), runner.SplitAttachmentName, []byte(`{"children":[]}`)); err != nil {
		t.Fatalf("служебное вложение не добавлено: %v", err)
	}

	o.tick(t)

	req := o.agent.seen
	got, err := os.ReadFile(filepath.Join(req.Workdir, runner.Dir, runner.DirAttachments, "schema.png"))
	if err != nil {
		t.Fatalf("человеческое вложение не материализовано: %v", err)
	}
	if string(got) != "данные схемы" {
		t.Errorf("содержимое вложения %q, ожидалось %q", got, "данные схемы")
	}
	if _, err := os.Stat(filepath.Join(req.Workdir, runner.Dir, runner.DirAttachments, runner.SplitAttachmentName)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("служебное вложение материализовано в рабочую папку: %v", err)
	}

	task, err := os.ReadFile(filepath.Join(req.Workdir, runner.Dir, runner.FileTask))
	if err != nil {
		t.Fatalf("постановка не записана: %v", err)
	}
	if !strings.Contains(string(task), "schema.png") {
		t.Errorf("в постановке нет упоминания вложения:\n%s", task)
	}
	if strings.Contains(string(task), runner.SplitAttachmentName) {
		t.Errorf("служебное вложение упомянуто в постановке:\n%s", task)
	}
}
```

Проверить, что `"errors"` и `"io/fs"` уже в импортах файла — если нет,
добавить.

- [x] **Step 2: Прогнать**

Run: `go test ./internal/pipeline/... -run TestTickMaterializesHumanAttachmentsButNotSplitJSON -v`
Expected: PASS (код уже написан в этой сессии — это подтверждающий, не
red-green тест).

- [x] **Step 3: Commit**

Коммит по этой задаче не делаем отдельно — все тесты этого плана уходят
одним коммитом в Task 6.

---

### Task 2: Тест — подтверждённый split копирует вложения родителя на детей (tasks.md 4.2)

**Files:**
- Modify: `internal/pipeline/splits_test.go` (добавить тест рядом с
  `TestEnsureChildrenAppendsParentDescription`)

**Interfaces:**
- Consumes: `confirmSplit(t, o)` (существующий хелпер), `o.tasks.AddAttachment`,
  `o.tasks.Get`, `o.CompleteSplits(context.Background())` — всё уже
  существует.

- [x] **Step 1: Добавить тест**

```go
// TestCompleteSplitsCopiesParentAttachmentsToChildren доказывает, что
// человеческие вложения родителя копируются на каждого созданного ребёнка.
func TestCompleteSplitsCopiesParentAttachmentsToChildren(t *testing.T) {
	o := newOffice(t)
	if _, err := o.tasks.AddAttachment("OFF-1", tracker.BySystem(), "schema.png", []byte("данные схемы")); err != nil {
		t.Fatalf("вложение не добавлено: %v", err)
	}
	confirmSplit(t, o)

	if err := o.CompleteSplits(context.Background()); err != nil {
		t.Fatalf("проход не прошёл: %v", err)
	}

	for _, key := range []string{"OFF-2", "OFF-3"} {
		child, err := o.tasks.Get(key)
		if err != nil {
			t.Fatalf("%s не прочитан: %v", key, err)
		}
		found := false
		for _, a := range child.Attachments {
			if a.Name == "schema.png" {
				found = true
				data, err := o.tasks.GetAttachment(key, a.ID)
				if err != nil {
					t.Fatalf("%s: вложение не прочитано: %v", key, err)
				}
				if string(data) != "данные схемы" {
					t.Errorf("%s: содержимое вложения %q, ожидалось %q", key, data, "данные схемы")
				}
			}
		}
		if !found {
			t.Errorf("%s: вложение родителя не унаследовано, вложения: %+v", key, child.Attachments)
		}
	}
}
```

- [x] **Step 2: Прогнать**

Run: `go test ./internal/pipeline/... -run TestCompleteSplitsCopiesParentAttachmentsToChildren -v`
Expected: PASS.

---

### Task 3: Тест — докатка вложений на ребёнка, созданного прерванным прогоном, без дублирования (tasks.md 4.3)

**Files:**
- Modify: `internal/pipeline/splits_test.go` (добавить рядом с
  `TestCompleteSplitsResumesInterruptedBatch`)

**Interfaces:**
- Consumes: то же, что Task 2, плюс `o.tasks.CreateTask` (для имитации
  ребёнка, уже созданного прошлым прогоном, — по образцу
  `TestCompleteSplitsResumesInterruptedBatch`).

- [x] **Step 1: Добавить тест**

```go
// TestCompleteSplitsBackfillsAttachmentsOnAlreadyCreatedChild
// воспроизводит ребёнка, созданного прошлым прерванным прогоном ДО того,
// как вложения родителя успели скопироваться, — следующий проход обязан
// докатить недостающее, а не решить, что раз ребёнок уже найден по
// метке, ему больше ничего не нужно. Второй ребёнок в этом же прогоне
// уже несёт своё собственное вложение с тем же именем — довеска второй
// копии тем же именем быть не должно (сверка по имени).
func TestCompleteSplitsBackfillsAttachmentsOnAlreadyCreatedChild(t *testing.T) {
	o := newOffice(t)
	if _, err := o.tasks.AddAttachment("OFF-1", tracker.BySystem(), "schema.png", []byte("данные схемы")); err != nil {
		t.Fatalf("вложение родителя не добавлено: %v", err)
	}
	confirmSplit(t, o)

	// Прошлая попытка успела создать первого ребёнка без вложений —
	// так выглядит пачка, прерванная между "ребёнок создан" и "вложения
	// скопированы".
	categoryRef, err := o.tasks.CreateTask("OFF", tracker.TaskInput{
		Summary: "Category CRUD", Description: "Модель, миграция, CRUD категорий.",
		Labels: []string{"split-child:OFF-1:category-crud"},
	})
	if err != nil {
		t.Fatalf("подготовка не удалась: %v", err)
	}
	// У этого же ребёнка уже случайно есть вложение с тем же именем
	// (например, от неродственного источника) — повторной заливки этого
	// имени быть не должно.
	if _, err := o.tasks.AddAttachment(categoryRef.Key, tracker.BySystem(), "schema.png", []byte("уже было")); err != nil {
		t.Fatalf("предварительное вложение не добавлено: %v", err)
	}

	if err := o.CompleteSplits(context.Background()); err != nil {
		t.Fatalf("проход не прошёл: %v", err)
	}

	category, err := o.tasks.Get(categoryRef.Key)
	if err != nil {
		t.Fatalf("категория не прочитана: %v", err)
	}
	count := 0
	for _, a := range category.Attachments {
		if a.Name == "schema.png" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("вложений schema.png у уже существовавшего ребёнка %d, ожидалось 1 (не задвоено)", count)
	}
	data, err := o.tasks.GetAttachment(categoryRef.Key, category.Attachments[0].ID)
	if err != nil {
		t.Fatalf("вложение не прочитано: %v", err)
	}
	if string(data) != "уже было" {
		t.Errorf("существующее вложение перезаписано: %q", data)
	}

	transaction, err := o.tasks.FindByMarker("OFF", splitChildMarker("OFF-1", "transaction-crud"))
	if err != nil {
		t.Fatalf("поиск не удался: %v", err)
	}
	if len(transaction) != 1 {
		t.Fatalf("операции не досозданы или задвоены: %+v", transaction)
	}
	linked, err := o.tasks.Get(transaction[0].Key)
	if err != nil {
		t.Fatalf("операции не прочитаны: %v", err)
	}
	found := false
	for _, a := range linked.Attachments {
		if a.Name == "schema.png" {
			found = true
		}
	}
	if !found {
		t.Errorf("вложение не докатилось на второго, только что созданного ребёнка: %+v", linked.Attachments)
	}
}
```

- [x] **Step 2: Прогнать**

Run: `go test ./internal/pipeline/... -run TestCompleteSplitsBackfillsAttachmentsOnAlreadyCreatedChild -v`
Expected: PASS.

---

### Task 4: Тест — split.json не наследуется детьми (tasks.md 4.4)

**Files:**
- Modify: `internal/pipeline/splits_test.go`

**Interfaces:**
- Consumes: то же, что Task 2. `confirmSplit(t, o)` уже создаёт
  `split.json`-вложение на родителе как часть обычного цикла подтверждения
  (`Office.record`, `internal/pipeline/pipeline.go`) — специально его
  добавлять не нужно, оно уже там.

- [x] **Step 1: Добавить тест**

```go
// TestCompleteSplitsDoesNotInheritSplitJSON доказывает, что служебное
// вложение с предложением разбивки (split.json), которое confirmSplit
// уже оставляет на родителе как часть обычного цикла, не копируется
// детям — в отличие от человеческих вложений.
func TestCompleteSplitsDoesNotInheritSplitJSON(t *testing.T) {
	o := newOffice(t)
	confirmSplit(t, o)

	parent := o.get(t, "OFF-1")
	hasSplitJSON := false
	for _, a := range parent.Attachments {
		if a.Name == runner.SplitAttachmentName {
			hasSplitJSON = true
		}
	}
	if !hasSplitJSON {
		t.Fatal("подготовка теста не удалась: у родителя нет split.json")
	}

	if err := o.CompleteSplits(context.Background()); err != nil {
		t.Fatalf("проход не прошёл: %v", err)
	}

	for _, key := range []string{"OFF-2", "OFF-3"} {
		child, err := o.tasks.Get(key)
		if err != nil {
			t.Fatalf("%s не прочитан: %v", key, err)
		}
		for _, a := range child.Attachments {
			if a.Name == runner.SplitAttachmentName {
				t.Errorf("%s унаследовал служебное вложение split.json", key)
			}
		}
	}
}
```

- [x] **Step 2: Прогнать**

Run: `go test ./internal/pipeline/... -run TestCompleteSplitsDoesNotInheritSplitJSON -v`
Expected: PASS.

---

### Task 5: Документация (tasks.md 5.1–5.2)

**Files:**
- Modify: `docs/notes/analyst-task-splitting.md` (новый раздел в конце,
  по образцу уже существующих дневниковых записей)
- (память Claude — вне репозитория, обновляется отдельно от этого плана)

**Interfaces:** нет — чистая документация.

- [x] **Step 1: Дописать раздел в `docs/notes/analyst-task-splitting.md`**

Добавить в конец файла раздел `## 2026-09-06: видимость вложений тикета —
Comet-изменение ticket-attachment-visibility`, кратко: находка (ни одна
роль не видела вложений своего тикета; дети split не наследовали
вложения родителя — та же дыра, что уже закрыли для текста описания в
`a3a504c`), решение (материализация в `.agent/attachments/`, строгая
идемпотентность наследования), ссылка на
`docs/openspec/changes/ticket-attachment-visibility/` и на Design Doc.

- [x] **Step 2: Commit**

Коммит по документации входит в Task 6 (единый коммит на весь план).

---

### Task 6: Финальная проверка и коммит (tasks.md 6.1–6.2)

**Files:** нет новых — проверка всего, что накопилось.

- [x] **Step 1: Полная сборка и проверка**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: всё зелёное, без пропущенных пакетов.

- [x] **Step 2: gofmt**

Run: `gofmt -l .`
Expected: пусто (если нет — `gofmt -w` на перечисленные файлы).

- [x] **Step 3: Commit**

```bash
git add internal/pipeline/pipeline_test.go internal/pipeline/splits_test.go \
        docs/notes/analyst-task-splitting.md \
        docs/openspec/changes/ticket-attachment-visibility \
        docs/superpowers/specs/2026-09-06-ticket-attachment-visibility-design.md \
        docs/superpowers/plans/2026-09-06-ticket-attachment-visibility.md
git commit -m "test: cover attachment materialization and split-child inheritance"
```

(Основной код групп 1–4 уже лежит в рабочем дереве с прошлых шагов этой
сессии — если он ещё не закоммичен отдельно, включить его в этот же
коммит явным списком файлов, не `git add -A`.)

## Self-Review

- **Spec coverage**: все три требования `specs/ticket-attachment-visibility/spec.md`
  (материализация, исключение служебных, безопасность имени) уже покрыты
  тестами группы 1–3 (сделано) и Task 1 (этот план). Требование
  `specs/pipeline-split-autocreate/spec.md` (наследование, идемпотентность,
  отсутствие дублирования) покрыто Task 2–4.
- **Placeholder scan**: нет TBD/TODO, весь код в шагах — реальный,
  готовый к вставке.
- **Type consistency**: `tracker.AttachmentRef{ID, Name}`,
  `o.tasks.AddAttachment(key, by, name, data) (string, error)`,
  `o.tasks.GetAttachment(key, id) ([]byte, error)` — везде одна и та же
  сигнатура, что и в уже написанном коде групп 1–4.
