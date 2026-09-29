package yougile

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/kao73/virtual-office/internal/pipeline"
	"github.com/kao73/virtual-office/internal/tracker"
)

const depKey = "task-dep"

func withDependency(fake *fakeYouGile, idTaskProject string) {
	fake.addTask(&fakeTask{ID: depKey, Title: "Base", IDTaskProject: idTaskProject,
		ColumnID: colWork, Timestamp: now.Add(-2 * time.Hour).UnixMilli()})
}

func TestLinkDependsOnRecordsLinkAfterVisibleNote(t *testing.T) {
	tr, fake := fixture(t)
	withDependency(fake, "ID-7")
	if err := tr.LinkDependsOn(testKey, depKey, tracker.BySystem()); err != nil {
		t.Fatal(err)
	}
	if got := fake.officeOf(testKey)["depends_on"]; !slices.Equal(anyStrings(got), []string{depKey}) {
		t.Errorf("depends_on = %#v", got)
	}
	// Spec «A declared dependency is visible to a human reading the task».
	if len(fake.chatPosts) != 1 || fake.chatPosts[0]["text"] != "Зависит от: ID-7 «Base»" {
		t.Errorf("заметка в чате: %#v", fake.chatPosts)
	}
	ready, _ := tr.ListReady(testProject, "Ready")
	if len(ready) != 1 || !slices.Equal(ready[0].DependsOn, []string{depKey}) {
		t.Errorf("ListReady не видит связь: %+v", ready)
	}
}

func TestLinkDependsOnNoteFallsBackToTaskID(t *testing.T) {
	tr, fake := fixture(t)
	withDependency(fake, "")
	if err := tr.LinkDependsOn(testKey, depKey, tracker.BySystem()); err != nil {
		t.Fatal(err)
	}
	if fake.chatPosts[0]["text"] != "Зависит от: "+depKey+" «Base»" {
		t.Errorf("заметка: %#v", fake.chatPosts[0]["text"])
	}
}

// Как у mock: повтор для записанной пары — ничего не пишет, ни заметки, ни PUT.
func TestLinkDependsOnIsIdempotent(t *testing.T) {
	tr, fake := fixture(t)
	withDependency(fake, "ID-7")
	for range 2 {
		if err := tr.LinkDependsOn(testKey, depKey, tracker.BySystem()); err != nil {
			t.Fatal(err)
		}
	}
	if len(fake.chatPosts) != 1 || len(fake.puts) != 1 {
		t.Errorf("повтор записал: заметок %d, PUT %d", len(fake.chatPosts), len(fake.puts))
	}
}

// Spec «A task cannot depend on itself»; пустой ключ — тоже отказ, и оба —
// до единого запроса.
func TestLinkDependsOnRejectsEmptyAndSelfWithoutRequests(t *testing.T) {
	for name, dep := range map[string]string{"пустой": "", "сама на себя": testKey} {
		t.Run(name, func(t *testing.T) {
			tr, fake := fixture(t)
			before := len(fake.requests)
			if err := tr.LinkDependsOn(testKey, dep, tracker.BySystem()); err == nil {
				t.Error("принято")
			}
			if len(fake.requests) != before {
				t.Errorf("ушли запросы: %v", fake.requests[before:])
			}
		})
	}
}

func TestLinkDependsOnMissingDependencyIsNotFound(t *testing.T) {
	for name, prepare := range map[string]func(*fakeYouGile){
		"нет такой": func(*fakeYouGile) {},
		"удалена": func(f *fakeYouGile) {
			withDependency(f, "ID-7")
			f.tasks[depKey].Deleted = true
		},
	} {
		t.Run(name, func(t *testing.T) {
			tr, fake := fixture(t)
			prepare(fake)
			if err := tr.LinkDependsOn(testKey, depKey, tracker.BySystem()); !errors.Is(err, tracker.ErrNotFound) {
				t.Errorf("дало %v", err)
			}
			if len(fake.chatPosts)+len(fake.puts) != 0 {
				t.Error("записано при пропавшей зависимости")
			}
		})
	}
}

// Зависимость может стоять вне колонок графа — это не ошибка (getRaw, не load).
func TestLinkDependsOnAcceptsDependencyOutsideGraph(t *testing.T) {
	tr, fake := fixture(t)
	withDependency(fake, "ID-7")
	fake.tasks[depKey].ColumnID = colOutside
	if err := tr.LinkDependsOn(testKey, depKey, tracker.BySystem()); err != nil {
		t.Errorf("зависимость вне графа дала %v", err)
	}
}

// Заметка — до записи: linkChildren пропускает уже записанные id, так что
// сбой между ними после записи потерял бы заметку навсегда. Худший исход
// нашего порядка — видимый дубль заметки.
func TestLinkDependsOnPostsNoteBeforeWrite(t *testing.T) {
	tr, fake := fixture(t)
	withDependency(fake, "ID-7")
	fake.fail["PUT /api-v2/tasks/"+testKey] = 500
	if err := tr.LinkDependsOn(testKey, depKey, tracker.BySystem()); err == nil {
		t.Fatal("сбой записи не дошёл до вызывающего")
	}
	if len(fake.chatPosts) != 1 {
		t.Errorf("заметок %d, ожидалась одна — до записи", len(fake.chatPosts))
	}
	if fake.officeOf(testKey)["depends_on"] != nil {
		t.Error("depends_on записан, хотя PUT упал")
	}
}

func TestLinkDependsOnKeepsForeignKeys(t *testing.T) {
	tr, fake := fixture(t)
	withDependency(fake, "ID-7")
	fake.tasks[testKey].APIData = map[string]any{"crm": "keep"}
	if err := tr.LinkDependsOn(testKey, depKey, tracker.BySystem()); err != nil {
		t.Fatal(err)
	}
	if fake.task(testKey).APIData["crm"] != "keep" {
		t.Error("чужой ключ потерян")
	}
}

// Spec «A dependent task is not claimable while its dependency is open» и
// «…becomes claimable once its dependency resolves» — настоящим гейтом
// pipeline над ref'ами этого адаптера. Review здесь — терминальный статус.
func TestClaimGateBlocksUntilDependencyResolves(t *testing.T) {
	tr, fake := fixture(t)
	withDependency(fake, "ID-7") // стоит в InProgress
	if err := tr.LinkDependsOn(testKey, depKey, tracker.BySystem()); err != nil {
		t.Fatal(err)
	}
	terminal := func(status string) bool { return status == "Review" }
	unmet := func() []tracker.TaskRef {
		t.Helper()
		ready, err := tr.ListReady(testProject, "Ready")
		if err != nil || len(ready) != 1 {
			t.Fatalf("ListReady: %+v, %v", ready, err)
		}
		all, err := tr.List(testProject, []string{"Ready", "InProgress", "Review"})
		if err != nil {
			t.Fatal(err)
		}
		return pipeline.UnmetDependencies(ready[0], pipeline.ByKey(all), terminal)
	}

	if got := unmet(); len(got) != 1 || got[0].Key != depKey {
		t.Errorf("пока зависимость открыта, гейт видит %+v", got)
	}
	fake.tasks[depKey].ColumnID = colReview
	if got := unmet(); len(got) != 0 {
		t.Errorf("зависимость закрыта, а гейт держит: %+v", got)
	}
}

// anyStrings — []any из JSON-ответа как []string.
func anyStrings(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, x := range list {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}
