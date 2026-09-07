package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kao73/virtual-office/internal/runner"
	"github.com/kao73/virtual-office/internal/tracker"
	"github.com/kao73/virtual-office/internal/tracker/mock"
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
	// finish() поднял HumanFlag, отправляя подтверждённый split в Blocked
	// (workflow.yaml: split → {to: Blocked, human: true}). closeSplitParent
	// обязан его снять — иначе закрытый тикет остаётся с меткой «ждёт
	// человека» на живой доске, хотя ждать уже нечего.
	if parent.HumanFlag {
		t.Error("HumanFlag не снят при закрытии родителя")
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

// TestEnsureChildrenAppendsParentDescription доказывает, что ребёнок несёт не
// только свой короткий срез, но и дословную исходную постановку родителя —
// analyst у ребёнка не видит трекер и не может сам её прочитать, если
// в собственном описании ребёнка её нет (docs/notes/analyst-task-splitting.md,
// живой прогон EXP-15→EXP-16..20: «вне рамок» и сквозные ограничения
// потерялись, потому что нигде, кроме родителя, не сохранялись).
func TestEnsureChildrenAppendsParentDescription(t *testing.T) {
	o := newOffice(t)
	confirmSplit(t, o)

	if err := o.CompleteSplits(context.Background()); err != nil {
		t.Fatalf("проход не прошёл: %v", err)
	}

	category, err := o.tasks.Get("OFF-2")
	if err != nil {
		t.Fatalf("категория не создана: %v", err)
	}
	if !strings.Contains(category.Description, "Модель, миграция, CRUD категорий.") {
		t.Errorf("свой срез описания потерян:\n%s", category.Description)
	}
	if !strings.Contains(category.Description, "Сделать что-нибудь полезное.") {
		t.Errorf("исходная постановка родителя (OFF-1) не найдена в описании ребёнка:\n%s", category.Description)
	}
}

// TestCompleteSplitsCopiesParentAttachmentsToChildren доказывает, что
// человеческие вложения родителя копируются на каждого созданного ребёнка —
// та же дыра, что уже закрыли для текста описания (childDescription),
// только для другого носителя.
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

// TestCompleteSplitsCopiesOnlyOneOfSameNamedParentAttachments — у родителя
// два разных вложения (разные ID — JIRA не следит за уникальностью имени
// файла) с одинаковым именем. ensureChildAttachments сверяется по имени
// (доккомент к ней это явно оговаривает: "докатится только одно") — но
// карта уже занятых имён обязана обновляться сразу после каждой успешной
// заливки внутри одного прохода по родительским вложениям, иначе оба
// пройдут проверку has[name] независимо и оба закатятся одному ребёнку.
func TestCompleteSplitsCopiesOnlyOneOfSameNamedParentAttachments(t *testing.T) {
	o := newOffice(t)
	if _, err := o.tasks.AddAttachment("OFF-1", tracker.BySystem(), "schema.png", []byte("первая версия")); err != nil {
		t.Fatalf("первое вложение не добавлено: %v", err)
	}
	if _, err := o.tasks.AddAttachment("OFF-1", tracker.BySystem(), "schema.png", []byte("вторая версия")); err != nil {
		t.Fatalf("второе вложение не добавлено: %v", err)
	}
	confirmSplit(t, o)

	if err := o.CompleteSplits(context.Background()); err != nil {
		t.Fatalf("проход не прошёл: %v", err)
	}

	child, err := o.tasks.Get("OFF-2")
	if err != nil {
		t.Fatalf("OFF-2 не прочитан: %v", err)
	}
	count := 0
	for _, a := range child.Attachments {
		if a.Name == "schema.png" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("вложений schema.png у ребёнка %d, ожидалось 1 — одноимённые вложения родителя "+
			"не должны дублироваться на одном ребёнке", count)
	}
}

// countingGetAttachment считает вызовы GetAttachment на чужие id (не
// вложение самого split.json — его читает splitChildren отдельно, и это
// не тот вызов, что здесь считается).
type countingGetAttachment struct {
	*mock.Tracker
	skip  string
	calls int
}

func (f *countingGetAttachment) GetAttachment(key, id string) ([]byte, error) {
	if id != f.skip {
		f.calls++
	}
	return f.Tracker.GetAttachment(key, id)
}

// TestEnsureChildAttachmentsDownloadsSameNamedParentAttachmentOnlyOnce —
// раз докатится только одно из двух одноимённых вложений родителя (см.
// TestCompleteSplitsCopiesOnlyOneOfSameNamedParentAttachments), скачивать
// оба ради этого незачем — needed сверяется по имени, а data по id, и без
// отдельной отметки "уже скачано" оба одноимённых вложения всё равно
// уходили бы отдельным запросом (внешнее ревью, pr-converge раунд 2).
func TestEnsureChildAttachmentsDownloadsSameNamedParentAttachmentOnlyOnce(t *testing.T) {
	o := newOffice(t)
	if _, err := o.tasks.AddAttachment("OFF-1", tracker.BySystem(), "schema.png", []byte("первая версия")); err != nil {
		t.Fatalf("первое вложение не добавлено: %v", err)
	}
	if _, err := o.tasks.AddAttachment("OFF-1", tracker.BySystem(), "schema.png", []byte("вторая версия")); err != nil {
		t.Fatalf("второе вложение не добавлено: %v", err)
	}
	confirmSplit(t, o)

	parent := o.get(t, "OFF-1")
	splitMarker, ok := tracker.MarkerOf(lastComment(t, parent).Body)
	if !ok || splitMarker.Attachment == "" {
		t.Fatalf("вложение с разбивкой не найдено в маркере отчёта")
	}

	counter := &countingGetAttachment{Tracker: o.tasks, skip: splitMarker.Attachment}
	o.useTracker(counter)

	if err := o.CompleteSplits(context.Background()); err != nil {
		t.Fatalf("проход не прошёл: %v", err)
	}
	if counter.calls != 1 {
		t.Errorf("GetAttachment вызван %d раз(а), ожидался 1 — второе одноимённое вложение всё равно не докатится ни к кому", counter.calls)
	}
}

// noGetAttachment роняет тест, если вложение родителя вообще скачивается —
// сверка "нужно ли докатывать" обязана справляться по одним лишь именам
// уже имеющихся у детей вложений, не читая содержимое заново.
type noGetAttachment struct {
	*mock.Tracker
	t *testing.T
}

func (f *noGetAttachment) GetAttachment(key, id string) ([]byte, error) {
	f.t.Errorf("GetAttachment(%s, %s) вызван — у всех детей уже есть все вложения родителя, скачивать было нечего", key, id)
	return f.Tracker.GetAttachment(key, id)
}

// TestEnsureChildAttachmentsSkipsDownloadWhenAllChildrenAlreadyHaveEverything
// — застрявший тикет подбирается каждым циклом Loop заново (splitFailed,
// например), и до этой правки ensureChildAttachments каждый раз качала
// все вложения родителя заново, даже когда всем детям уже всего хватает
// (внешнее ревью, pr-converge раунд 1).
func TestEnsureChildAttachmentsSkipsDownloadWhenAllChildrenAlreadyHaveEverything(t *testing.T) {
	o := newOffice(t)
	if _, err := o.tasks.AddAttachment("OFF-1", tracker.BySystem(), "schema.png", []byte("данные")); err != nil {
		t.Fatalf("вложение не добавлено: %v", err)
	}
	confirmSplit(t, o)
	if err := o.CompleteSplits(context.Background()); err != nil {
		t.Fatalf("проход не прошёл: %v", err)
	}

	parent := o.get(t, "OFF-1")
	keys := map[string]string{"category-crud": "OFF-2", "transaction-crud": "OFF-3"}
	o.useTracker(&noGetAttachment{Tracker: o.tasks, t: t})
	if err := o.ensureChildAttachments(parent, keys); err != nil {
		t.Fatalf("повторный вызов не должен падать: %v", err)
	}
}

// TestCompleteSplitsBackfillsAttachmentsOnAlreadyCreatedChild воспроизводит
// ребёнка, созданного прошлым прерванным прогоном ДО того, как вложения
// родителя успели скопироваться, — следующий проход обязан докатить
// недостающее, а не решить, что раз ребёнок уже найден по метке, ему
// больше ничего не нужно. У этого же ребёнка уже случайно есть вложение
// с тем же именем — повторной заливки этого имени быть не должно.
func TestCompleteSplitsBackfillsAttachmentsOnAlreadyCreatedChild(t *testing.T) {
	o := newOffice(t)
	if _, err := o.tasks.AddAttachment("OFF-1", tracker.BySystem(), "schema.png", []byte("данные схемы")); err != nil {
		t.Fatalf("вложение родителя не добавлено: %v", err)
	}
	confirmSplit(t, o)

	categoryRef, err := o.tasks.CreateTask("OFF", tracker.TaskInput{
		Summary: "Category CRUD", Description: "Модель, миграция, CRUD категорий.",
		Labels: []string{"split-child:OFF-1:category-crud"},
	})
	if err != nil {
		t.Fatalf("подготовка не удалась: %v", err)
	}
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

// TestCompleteSplitsDoesNotInheritSplitJSON доказывает, что служебное
// вложение с предложением разбивки (split.json), которое confirmSplit
// уже оставляет на родителе как часть обычного цикла подтверждения, не
// копируется детям — в отличие от человеческих вложений.
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

// TestCompleteSplitsResumesInterruptedBatch воспроизводит пачку, прерванную
// на середине: прошлый проход успел создать только первого ребёнка. Новый
// проход должен досоздать только недостающего, а не задвоить первого —
// идемпотентность заложена в ensureChildren задачи 11 (FindByMarker перед
// каждым CreateTask), этот тест доказывает это конкретным сценарием.
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

// duplicateFind добавляет к настоящему результату FindByMarker ещё одну,
// заведомо постороннюю задачу по той же метке: имитирует коллизию — то,
// что по одной метке нашлось больше одной задачи, — которую ensureChildren
// не должен проглатывать молча.
type duplicateFind struct {
	*mock.Tracker
	marker string
	extra  tracker.TaskRef
}

func (f *duplicateFind) FindByMarker(project, marker string) ([]tracker.TaskRef, error) {
	found, err := f.Tracker.FindByMarker(project, marker)
	if err != nil || marker != f.marker {
		return found, err
	}
	return append(found, f.extra), nil
}

// TestEnsureChildrenLogsWhenMarkerMatchesMultiple доказывает, что коллизия
// по метке — по одной метке нашлось больше одной задачи — попадает в лог,
// а не проглатывается молча. Поведение при этом не меняется: берётся
// по-прежнему первый найденный.
func TestEnsureChildrenLogsWhenMarkerMatchesMultiple(t *testing.T) {
	o := newOffice(t)
	confirmSplit(t, o)

	marker := splitChildMarker("OFF-1", "category-crud")
	if _, err := o.tasks.CreateTask("OFF", tracker.TaskInput{
		Summary: "Category CRUD", Description: "Модель, миграция, CRUD категорий.",
		Labels: []string{marker},
	}); err != nil {
		t.Fatalf("подготовка не удалась: %v", err)
	}

	var log strings.Builder
	o.Office.Log = &log
	o.useTracker(&duplicateFind{Tracker: o.tasks, marker: marker, extra: tracker.TaskRef{Key: "OFF-99", Project: "OFF"}})

	if err := o.CompleteSplits(context.Background()); err != nil {
		t.Fatalf("проход не должен падать: %v", err)
	}

	if !strings.Contains(log.String(), marker) {
		t.Errorf("коллизия по метке не залогирована:\n%s", log.String())
	}

	// Поведение не изменилось: связь ушла на настоящего первого найденного
	// (созданную выше "Category CRUD"), а не на постороннего OFF-99.
	transaction, err := o.tasks.FindByMarker("OFF", splitChildMarker("OFF-1", "transaction-crud"))
	if err != nil {
		t.Fatalf("поиск не удался: %v", err)
	}
	if len(transaction) != 1 {
		t.Fatalf("операции не найдены или задвоены: %+v", transaction)
	}
	linked, err := o.tasks.Get(transaction[0].Key)
	if err != nil {
		t.Fatalf("операции не прочитаны: %v", err)
	}
	if slices.Contains(linked.DependsOn, "OFF-99") || !slices.Contains(linked.DependsOn, "OFF-2") {
		t.Errorf("зависимость ушла не на того: %v, ожидался OFF-2, не OFF-99", linked.DependsOn)
	}
}

// TestCompleteSplitsRejectsCorruptedAttachment воспроизводит вложение,
// испорченное между записью и вторым чтением (правка руками, порча
// хранилища): depends_on ссылается на несуществующий id. splitChildren
// обязан перепроверить граф той же проверкой, что agentio.Result.Validate
// применяет к свежему результату агента, — иначе LinkDependsOn получил бы
// пустой ключ (dep, которого нет в keys), а jira на пустом ключе ответила
// бы 400.
func TestCompleteSplitsRejectsCorruptedAttachment(t *testing.T) {
	o := newOffice(t)
	confirmSplit(t, o)

	parent := o.get(t, "OFF-1")
	comment := lastComment(t, parent)
	marker, ok := tracker.MarkerOf(comment.Body)
	if !ok || marker.Attachment == "" {
		t.Fatalf("вложение не найдено в маркере отчёта:\n%s", comment.Body)
	}

	corrupted := runner.Split{Children: []runner.SplitChild{
		{ID: "category-crud", Title: "Category CRUD", Description: "Модель, миграция, CRUD категорий.",
			DependsOn: []string{"нет-такого-id"}},
	}}
	data, err := json.Marshal(corrupted)
	if err != nil {
		t.Fatalf("вложение не собрано: %v", err)
	}
	path := filepath.Join(o.tasks.Root(), "OFF-1", "attachments", marker.Attachment)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("вложение не подменено: %v", err)
	}

	if err := o.CompleteSplits(context.Background()); err != nil {
		t.Fatalf("проход не должен падать целиком: %v", err)
	}

	if _, err := o.tasks.Get("OFF-2"); err == nil {
		t.Error("дети не должны создаваться на испорченном вложении")
	}

	fresh := o.get(t, "OFF-1")
	if fresh.Status != "Blocked" {
		t.Errorf("статус родителя %q, ожидался Blocked — испорченное вложение не должно двигать задачу", fresh.Status)
	}
	failComment := lastComment(t, fresh)
	failMarker, ok := tracker.MarkerOf(failComment.Body)
	if !ok || failMarker.Event != tracker.EventSplitCreateFailed {
		t.Errorf("нет записи о сбое:\n%s", failComment.Body)
	}
}

// TestCompleteSplitsRefusesToCloseWithoutPRBlock воспроизводит офис без
// блока pr: в workflow.yaml — граф без прохода pull request легален
// (config.go, checkPR: пустой блок целиком — офис, который PR не открывает).
// Без блока pr закрывать разбитые задачи некуда — CompleteSplits обязан
// отказаться в самом начале, до первой мутации, а не создать детей,
// скопировать вложения и связать их, и только на последнем шаге узнать,
// что закрывать родителя некуда (внешнее ревью, pr-converge раунд 1:
// раньше проверка стояла в closeSplitParent — самом последнем шаге —
// и каждый цикл Loop повторял всю необратимую работу заново, чтобы
// упасть в том же месте).
func TestCompleteSplitsRefusesToCloseWithoutPRBlock(t *testing.T) {
	o := newOffice(t)
	confirmSplit(t, o)
	o.Workflow.PR = tracker.PRFlow{}

	if err := o.CompleteSplits(context.Background()); err != nil {
		t.Fatalf("проход не должен падать целиком: %v", err)
	}

	parent := o.get(t, "OFF-1")
	if parent.Status != "Blocked" {
		t.Errorf("статус родителя %q, ожидался Blocked — без pr-блока к нему вообще не притрагиваются", parent.Status)
	}
	if _, err := o.tasks.Get("OFF-2"); err == nil {
		t.Error("дети не должны создаваться, пока не выяснено, что закрывать родителя всё равно некуда")
	}
}

// TestCompleteSplitsRejectsEmptySplitChildren — та же порча вложения между
// записью и вторым чтением, что и выше, но другой формой: пустой
// split.children[]. agentio.Result.Validate это отсекает при первой публикации
// агентом, но splitChildren читает вложение заново из трекера (runner.Split.
// Validate) — без собственной проверки на пустоту родитель закрылся бы,
// не создав ни одного ребёнка, и задача бы бесследно пропала.
func TestCompleteSplitsRejectsEmptySplitChildren(t *testing.T) {
	o := newOffice(t)
	confirmSplit(t, o)

	parent := o.get(t, "OFF-1")
	comment := lastComment(t, parent)
	marker, ok := tracker.MarkerOf(comment.Body)
	if !ok || marker.Attachment == "" {
		t.Fatalf("вложение не найдено в маркере отчёта:\n%s", comment.Body)
	}

	empty := runner.Split{Children: []runner.SplitChild{}}
	data, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("вложение не собрано: %v", err)
	}
	path := filepath.Join(o.tasks.Root(), "OFF-1", "attachments", marker.Attachment)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("вложение не подменено: %v", err)
	}

	if err := o.CompleteSplits(context.Background()); err != nil {
		t.Fatalf("проход не должен падать целиком: %v", err)
	}

	fresh := o.get(t, "OFF-1")
	if fresh.Status != "Blocked" {
		t.Errorf("статус родителя %q, ожидался Blocked — пустой список детей не должен закрывать задачу", fresh.Status)
	}
	failComment := lastComment(t, fresh)
	failMarker, ok := tracker.MarkerOf(failComment.Body)
	if !ok || failMarker.Event != tracker.EventSplitCreateFailed {
		t.Errorf("нет записи о сбое:\n%s", failComment.Body)
	}
}

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

// TestSplitFailedDoesNotSpamRepeatedNotices воспроизводит тикет, застрявший
// в Blocked с постоянно падающим CreateTask: Loop зовёт CompleteSplits
// каждый цикл (по умолчанию раз в две минуты), и без дедупликации
// одинаковая запись о сбое копилась бы в переписке без конца.
func TestSplitFailedDoesNotSpamRepeatedNotices(t *testing.T) {
	o := newOffice(t)
	confirmSplit(t, o)
	o.useTracker(&flakyCreate{Tracker: o.tasks, failOn: "Category CRUD"})

	if err := o.CompleteSplits(context.Background()); err != nil {
		t.Fatalf("первый проход не должен падать: %v", err)
	}
	first := o.get(t, "OFF-1")
	firstCount := len(first.Comments)

	if err := o.CompleteSplits(context.Background()); err != nil {
		t.Fatalf("второй проход не должен падать: %v", err)
	}
	second := o.get(t, "OFF-1")
	if len(second.Comments) != firstCount {
		t.Errorf("после второго прохода комментариев %d, было %d: повторная запись о сбое "+
			"не должна дублироваться", len(second.Comments), firstCount)
	}

	failures := 0
	for _, c := range second.Comments {
		if m, ok := tracker.MarkerOf(c.Body); ok && m.Event == tracker.EventSplitCreateFailed {
			failures++
		}
	}
	if failures != 1 {
		t.Errorf("записей о сбое %d, ожидалась ровно одна", failures)
	}
}

// TestSplitFailedReportsANewReasonEvenAfterAnOldOne — дедупликация
// splitFailed сравнивает по факту события, не по тексту причины: первая
// же неудача навсегда занимает EventSplitCreateFailed, и если человек
// починил первую беду, а проход упал уже на другой, второй раз никто об
// этом не узнает (внешнее ревью, pr-converge раунд 1). Первая беда —
// испорченное вложение (дублирующий id, как в
// TestCompleteSplitsRejectsCorruptedAttachment), вторая — сбой закрытия
// родителя после того, как вложение починили руками.
func TestSplitFailedReportsANewReasonEvenAfterAnOldOne(t *testing.T) {
	o := newOffice(t)
	confirmSplit(t, o)

	parent := o.get(t, "OFF-1")
	comment := lastComment(t, parent)
	marker, ok := tracker.MarkerOf(comment.Body)
	if !ok || marker.Attachment == "" {
		t.Fatalf("вложение не найдено в маркере отчёта:\n%s", comment.Body)
	}
	attachmentPath := filepath.Join(o.tasks.Root(), "OFF-1", "attachments", marker.Attachment)

	corrupted := runner.Split{Children: []runner.SplitChild{
		{ID: "a", Title: "A", Description: "d", DependsOn: []string{"нет-такого-id"}},
	}}
	data, err := json.Marshal(corrupted)
	if err != nil {
		t.Fatalf("вложение не собрано: %v", err)
	}
	if err := os.WriteFile(attachmentPath, data, 0o644); err != nil {
		t.Fatalf("вложение не подменено: %v", err)
	}
	if err := o.CompleteSplits(context.Background()); err != nil {
		t.Fatalf("первый проход не должен падать: %v", err)
	}

	valid, err := json.Marshal(splitResult().Split)
	if err != nil {
		t.Fatalf("вложение не собрано: %v", err)
	}
	if err := os.WriteFile(attachmentPath, valid, 0o644); err != nil {
		t.Fatalf("вложение не починено: %v", err)
	}
	o.useTracker(&flakyClose{Tracker: o.tasks, failOn: "OFF-1"})
	if err := o.CompleteSplits(context.Background()); err != nil {
		t.Fatalf("второй проход не должен падать: %v", err)
	}

	fresh := o.get(t, "OFF-1")
	var failures []string
	for _, c := range fresh.Comments {
		if m, ok := tracker.MarkerOf(c.Body); ok && m.Event == tracker.EventSplitCreateFailed {
			_, text, _ := strings.Cut(c.Body, "\n")
			failures = append(failures, strings.TrimSpace(text))
		}
	}
	if len(failures) != 2 {
		t.Fatalf("записей о сбое %d, ожидалось 2 (разные причины): %+v", len(failures), failures)
	}
	if !strings.Contains(failures[0], "не прошло проверку") {
		t.Errorf("первая причина %q, ожидалась про испорченное вложение", failures[0])
	}
	if !strings.Contains(failures[1], "не закрыт") {
		t.Errorf("вторая причина %q, ожидалась про закрытие родителя", failures[1])
	}
}

// TestSplitFailedDedupsByStableCategoryNotFreeformText — дедупликация
// splitFailed сравнивала полный текст записи, прочитанный из трекера. На
// JIRA этот текст мог уехать и вернуться другим (wiki() экранирует
// квадратные скобки в теле ответа сервера, которое несёт свободный текст
// причины), а сам текст ещё и недетерминирован независимо от трекера —
// ensureChildAttachments раньше обходила детей картой, и первым в тексте
// сбоя называлось то, что попадётся (внешнее ревью, pr-converge раунд 2).
// Дедупликация обязана сравнивать только стабильную категорию шага,
// не обёрнутую ошибку целиком.
func TestSplitFailedDedupsByStableCategoryNotFreeformText(t *testing.T) {
	o := newOffice(t)
	confirmSplit(t, o)
	task := o.get(t, "OFF-1")

	if err := o.splitFailed(task, "вложения родителя не скопированы", errors.New("первая попытка: сеть недоступна")); err != nil {
		t.Fatalf("запись не удалась: %v", err)
	}
	task = o.get(t, "OFF-1")
	if err := o.splitFailed(task, "вложения родителя не скопированы", errors.New("вторая попытка: другой текст ошибки")); err != nil {
		t.Fatalf("запись не удалась: %v", err)
	}
	task = o.get(t, "OFF-1")

	failures := 0
	for _, c := range task.Comments {
		if m, ok := tracker.MarkerOf(c.Body); ok && m.Event == tracker.EventSplitCreateFailed {
			failures++
		}
	}
	if failures != 1 {
		t.Errorf("записей о сбое %d, ожидалась ровно одна — категория та же, текст ошибки менялся", failures)
	}

	if err := o.splitFailed(task, "родитель не закрыт", errors.New("третья попытка")); err != nil {
		t.Fatalf("запись не удалась: %v", err)
	}
	task = o.get(t, "OFF-1")
	failures = 0
	for _, c := range task.Comments {
		if m, ok := tracker.MarkerOf(c.Body); ok && m.Event == tracker.EventSplitCreateFailed {
			failures++
		}
	}
	if failures != 2 {
		t.Errorf("записей о сбое %d, ожидалось 2 — новая категория обязана быть записана", failures)
	}
}

// TestSplitFailedDedupsAgainstAllPastCategoriesNotJustLast — раунд 2 сравнивал
// со всей перепиской по одной причине; раунд 3 нашёл, что сравнение шло
// только с ПОСЛЕДНЕЙ записью (tracker.LastEventText), а не со множеством уже
// сказанных причин. Ранний шаг completeSplit, падающий изредка, и поздний,
// падающий стабильно, чередуют свои категории между циклами Loop — и каждая
// из них "новая" относительно предыдущей, хотя обе уже звучали.
func TestSplitFailedDedupsAgainstAllPastCategoriesNotJustLast(t *testing.T) {
	o := newOffice(t)
	confirmSplit(t, o)
	task := o.get(t, "OFF-1")

	// Цикл 1: ранняя причина.
	if err := o.splitFailed(task, "связи depends_on не записаны", errors.New("первая попытка")); err != nil {
		t.Fatalf("запись не удалась: %v", err)
	}
	task = o.get(t, "OFF-1")
	// Цикл 2: поздняя причина — ранняя починилась, дошли дальше.
	if err := o.splitFailed(task, "родитель не закрыт", errors.New("вторая попытка")); err != nil {
		t.Fatalf("запись не удалась: %v", err)
	}
	task = o.get(t, "OFF-1")
	// Цикл 3: ранняя причина снова — та же самая, что и в цикле 1.
	if err := o.splitFailed(task, "связи depends_on не записаны", errors.New("третья попытка")); err != nil {
		t.Fatalf("запись не удалась: %v", err)
	}
	task = o.get(t, "OFF-1")

	var categories []string
	for _, c := range task.Comments {
		if m, ok := tracker.MarkerOf(c.Body); ok && m.Event == tracker.EventSplitCreateFailed {
			_, rest, _ := strings.Cut(c.Body, "\n")
			category, _, _ := strings.Cut(strings.TrimSpace(rest), "\n")
			categories = append(categories, category)
		}
	}
	if len(categories) != 2 {
		t.Errorf("записей о сбое %d, ожидалось 2 (обе причины уже звучали): %+v", len(categories), categories)
	}
}

// flakyClose роняет Transition для ключа задачи-родителя: так выглядит сбой
// самого последнего шага completeSplit — закрытия родителя в
// closeSplitParent, — уже после того как дети созданы, связаны и отчёт
// о них записан. Task 11 обернул splitFailed'ом только ранние шаги; этот
// тест — на сбой именно здесь.
type flakyClose struct {
	*mock.Tracker
	failOn string
}

func (f *flakyClose) Transition(key string, by tracker.Actor, toStatus string) error {
	if key == f.failOn {
		return errors.New("сеть недоступна")
	}
	return f.Tracker.Transition(key, by, toStatus)
}

// TestCompleteSplitsRecordsCloseFailureNoticeAndContinues доказывает, что
// сбой именно на закрытии родителя (Transition после того, как дети уже
// созданы и связаны) тоже уходит через splitFailed, а не наружу из
// CompleteSplits — до этой задачи closeSplitParent не был обёрнут, и такой
// сбой ронял бы весь проход, не давая дойти до OFF-9.
func TestCompleteSplitsRecordsCloseFailureNoticeAndContinues(t *testing.T) {
	o := newOffice(t)
	confirmSplit(t, o)

	// Второй, независимый подтверждённый тикет — доказывает, что беда
	// на закрытии OFF-1 не роняет весь проход CompleteSplits.
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

	o.useTracker(&flakyClose{Tracker: o.tasks, failOn: "OFF-1"})

	if err := o.CompleteSplits(context.Background()); err != nil {
		t.Fatalf("сбой закрытия родителя не должен ронять весь проход: %v", err)
	}

	parent := o.get(t, "OFF-1")
	if parent.Status != "Blocked" {
		t.Errorf("статус родителя %q, ожидался Blocked — сбой закрытия не должен двигать задачу", parent.Status)
	}
	comment := lastComment(t, parent)
	marker, ok := tracker.MarkerOf(comment.Body)
	if !ok || marker.Event != tracker.EventSplitCreateFailed {
		t.Errorf("нет записи о сбое закрытия:\n%s", comment.Body)
	}

	other := o.get(t, "OFF-9")
	if other.Status != o.Workflow.PR.Merged {
		t.Errorf("вторая задача %q, ожидался %q — беда закрытия первой не должна её касаться",
			other.Status, o.Workflow.PR.Merged)
	}
}

// TestCompleteSplitsDoesNotDuplicateCloseNoticeOnRetry — та же персистентная
// беда на Transition, что и выше, но проверяет другой угол: если закрытие
// падает раз за разом, каждый следующий проход CompleteSplits заново находит
// подтверждённого родителя (дети и связи уже на месте — не-op) и раньше
// доходил бы до o.record с "Разбита на: …" заново — без дедупликации,
// применённой к самой этой записи (в отличие от splitFailed, у которой
// дедупликация уже была), комментарий копился бы на каждом цикле Loop.
func TestCompleteSplitsDoesNotDuplicateCloseNoticeOnRetry(t *testing.T) {
	o := newOffice(t)
	confirmSplit(t, o)
	o.useTracker(&flakyClose{Tracker: o.tasks, failOn: "OFF-1"})

	if err := o.CompleteSplits(context.Background()); err != nil {
		t.Fatalf("первый проход не должен падать целиком: %v", err)
	}
	if err := o.CompleteSplits(context.Background()); err != nil {
		t.Fatalf("второй проход не должен падать целиком: %v", err)
	}

	parent := o.get(t, "OFF-1")
	successes := 0
	for _, c := range parent.Comments {
		if m, ok := tracker.MarkerOf(c.Body); ok && m.Event == tracker.EventSplitCreated {
			successes++
		}
	}
	if successes != 1 {
		t.Errorf("запись об успешном разбиении встречена %d раз(а), ожидался ровно 1 — "+
			"персистентный сбой закрытия не должен копить дубли", successes)
	}
}

// countingLinksFlakyClose комбинирует подсчёт вызовов LinkDependsOn (для
// проверки того, что повтор не шлёт уже записанную связь заново) с
// персистентным сбоем закрытия родителя (Transition) — тем же приёмом,
// что flakyClose выше, — чтобы CompleteSplits вызывался дважды на одном
// и том же застрявшем тикете, не полагаясь на реальный успех закрытия.
type countingLinksFlakyClose struct {
	*mock.Tracker
	failCloseOn string
	linkCalls   int
}

func (c *countingLinksFlakyClose) LinkDependsOn(key, dependsOnKey string, by tracker.Actor) error {
	c.linkCalls++
	return c.Tracker.LinkDependsOn(key, dependsOnKey, by)
}

func (c *countingLinksFlakyClose) Transition(key string, by tracker.Actor, toStatus string) error {
	if key == c.failCloseOn {
		return errors.New("сеть недоступна")
	}
	return c.Tracker.Transition(key, by, toStatus)
}

// TestLinkChildrenSkipsAlreadyLinkedPairOnRetry доказывает, что застрявший
// на закрытии тикет не шлёт POST /issueLink заново на каждый цикл Loop
// для пары, уже связанной прошлым проходом.
func TestLinkChildrenSkipsAlreadyLinkedPairOnRetry(t *testing.T) {
	o := newOffice(t)
	confirmSplit(t, o)
	wrap := &countingLinksFlakyClose{Tracker: o.tasks, failCloseOn: "OFF-1"}
	o.useTracker(wrap)

	if err := o.CompleteSplits(context.Background()); err != nil {
		t.Fatalf("первый проход не должен падать целиком: %v", err)
	}
	if wrap.linkCalls != 1 {
		t.Fatalf("после первого прохода ожидался 1 вызов LinkDependsOn, получено %d", wrap.linkCalls)
	}

	if err := o.CompleteSplits(context.Background()); err != nil {
		t.Fatalf("второй проход не должен падать целиком: %v", err)
	}
	if wrap.linkCalls != 1 {
		t.Errorf("повторный проход снова отправил уже записанную связь: всего вызовов %d, ожидался 1", wrap.linkCalls)
	}

	transaction, err := o.tasks.FindByMarker("OFF", splitChildMarker("OFF-1", "transaction-crud"))
	if err != nil || len(transaction) != 1 {
		t.Fatalf("операции не найдены: %+v, %v", transaction, err)
	}
	category, err := o.tasks.FindByMarker("OFF", splitChildMarker("OFF-1", "category-crud"))
	if err != nil || len(category) != 1 {
		t.Fatalf("категория не найдена: %+v, %v", category, err)
	}
	linked, err := o.tasks.Get(transaction[0].Key)
	if err != nil {
		t.Fatalf("операции не прочитаны: %v", err)
	}
	if !slices.Contains(linked.DependsOn, category[0].Key) {
		t.Errorf("связь потерялась после повторного прохода: %v", linked.DependsOn)
	}
}

// flakyComment роняет Comment для данного ключа: так выглядит сбой самой
// записи о неудаче — splitFailed пишет комментарий через o.record, и если
// падает уже эта запись (не шаг, который она описывает), ошибка раньше
// уходила из completeSplit наружу необёрнутой.
type flakyComment struct {
	*mock.Tracker
	failOn string
}

func (f *flakyComment) Comment(key string, by tracker.Actor, body string) error {
	if key == f.failOn {
		return errors.New("сеть недоступна")
	}
	return f.Tracker.Comment(key, by, body)
}

// TestCompleteSplitsContinuesPastTaskWhoseFailureNoticeCannotBeWritten
// доказывает, что беда внутри самого splitFailed (не beда, которую он
// описывает, а сбой записи о ней) не прерывает CompleteSplits: доккомент
// completeSplit прямо обещает, что ошибка любого шага "не прерывает обход
// остальных задач" — но раньше CompleteSplits возвращал результат
// completeSplit наружу без развилки, и такая ошибка обрывала весь проход
// по всем оставшимся тикетам и проектам, тем же способом, что Reap уже
// разбирает для ErrNotOwner (pipeline.go).
func TestCompleteSplitsContinuesPastTaskWhoseFailureNoticeCannotBeWritten(t *testing.T) {
	o := newOffice(t)
	confirmSplit(t, o)

	parent := o.get(t, "OFF-1")
	comment := lastComment(t, parent)
	marker, ok := tracker.MarkerOf(comment.Body)
	if !ok || marker.Attachment == "" {
		t.Fatalf("вложение не найдено в маркере отчёта:\n%s", comment.Body)
	}
	// Портим вложение OFF-1 (та же порча, что и в TestCompleteSplitsRejectsEmptySplitChildren),
	// чтобы completeSplit дошёл до splitFailed, — а Comment для OFF-1 роняем,
	// чтобы упала уже сама запись о сбое.
	empty := runner.Split{Children: []runner.SplitChild{}}
	data, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("вложение не собрано: %v", err)
	}
	path := filepath.Join(o.tasks.Root(), "OFF-1", "attachments", marker.Attachment)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("вложение не подменено: %v", err)
	}

	// Второй, независимый подтверждённый тикет — доказывает, что беда
	// на OFF-1 не роняет весь проход CompleteSplits.
	o.add("OFF-9", "Analysis")
	o.agent.result = splitResult()
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

	o.useTracker(&flakyComment{Tracker: o.tasks, failOn: "OFF-1"})

	// Проход по остальным задачам не должен обрываться беды ради, но сама
	// беда — не молчать: ручная runner complete-splits обязана вернуть
	// код возврата, отражающий реальный сбой, а не "всё в порядке" только
	// потому, что цикл дошёл до конца.
	err = o.CompleteSplits(context.Background())
	if err == nil {
		t.Fatal("проход должен вернуть беду с OFF-1, а не тихо её проглотить")
	}
	if !strings.Contains(err.Error(), "OFF-1") {
		t.Errorf("в возвращённой ошибке нет ключа сбойной задачи: %v", err)
	}

	other := o.get(t, "OFF-9")
	if other.Status != o.Workflow.PR.Merged {
		t.Errorf("OFF-9 %q, ожидался %q — беда с записью на OFF-1 не должна её касаться",
			other.Status, o.Workflow.PR.Merged)
	}
}
