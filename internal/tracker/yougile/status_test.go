package yougile

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kao73/virtual-office/internal/tracker"
)

func keys(refs []tracker.TaskRef) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.Key)
	}
	return out
}

func TestListReadyFiltersByColumnServerSide(t *testing.T) {
	tr, fake := fixture(t)
	fake.addTask(&fakeTask{ID: "in-review", ColumnID: colReview, Timestamp: now.UnixMilli()})
	refs, err := tr.ListReady(testProject, "Ready")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(keys(refs), []string{testKey}) {
		t.Errorf("ListReady = %v", keys(refs))
	}
	if refs[0].Status != "Ready" || refs[0].Project != testProject {
		t.Errorf("ref: %+v", refs[0])
	}
	if fake.count("GET /api-v2/task-list?columnId="+colReady) != 1 {
		t.Errorf("фильтр по колонке не ушёл на сервер: %v", fake.requests)
	}
}

func TestListReadyDropsLiveLeaseKeepsExpired(t *testing.T) {
	tr, fake := fixture(t)
	fake.addTask(&fakeTask{ID: "expired", ColumnID: colReady, Timestamp: now.UnixMilli()})
	fake.setLease(testKey, "run-live", now.Add(time.Minute))
	fake.setLease("expired", "run-old", now.Add(-time.Minute))
	refs, err := tr.ListReady(testProject, "Ready")
	if err != nil || !slices.Equal(keys(refs), []string{"expired"}) {
		t.Errorf("ListReady = %v, %v", keys(refs), err)
	}
}

func TestListReadySortsByCreation(t *testing.T) {
	tr, fake := fixture(t)
	fake.addTask(&fakeTask{ID: "oldest", ColumnID: colReady, Timestamp: now.Add(-3 * time.Hour).UnixMilli()})
	fake.addTask(&fakeTask{ID: "newest", ColumnID: colReady, Timestamp: now.UnixMilli()})
	refs, _ := tr.ListReady(testProject, "Ready")
	if !slices.Equal(keys(refs), []string{"oldest", testKey, "newest"}) {
		t.Errorf("порядок: %v", keys(refs))
	}
}

// Review Focus #4.
func TestListReadySkipsArchivedAndDeleted(t *testing.T) {
	tr, fake := fixture(t)
	fake.addTask(&fakeTask{ID: "archived", ColumnID: colReady, Timestamp: now.UnixMilli(), Archived: true})
	fake.addTask(&fakeTask{ID: "deleted", ColumnID: colReady, Timestamp: now.UnixMilli(), Deleted: true})
	refs, _ := tr.ListReady(testProject, "Ready")
	if !slices.Equal(keys(refs), []string{testKey}) {
		t.Errorf("ListReady = %v", keys(refs))
	}
	all, _ := tr.List(testProject, []string{"Ready"})
	if !slices.Equal(keys(all), []string{testKey}) {
		t.Errorf("List = %v", keys(all))
	}
}

// Review Focus #5.
func TestListReadyPaginates(t *testing.T) {
	tr, fake := fixture(t)
	fake.pageCap = 2
	for i := range 5 {
		fake.addTask(&fakeTask{ID: fmt.Sprintf("extra-%d", i), ColumnID: colReady, Timestamp: now.UnixMilli() + int64(i)})
	}
	refs, err := tr.ListReady(testProject, "Ready")
	if err != nil || len(refs) != 6 {
		t.Errorf("ListReady за страницами: %d задач, %v", len(refs), err)
	}
}

func TestListReadyUnknownStatusFails(t *testing.T) {
	tr, _ := fixture(t)
	_, err := tr.ListReady(testProject, "Backlog")
	if err == nil || !strings.Contains(err.Error(), "Backlog") {
		t.Errorf("незнакомый статус дал %v", err)
	}
}

func TestListReadyUnknownProject(t *testing.T) {
	tr, _ := fixture(t)
	if _, err := tr.ListReady("OTHER", "Ready"); !errors.Is(err, tracker.ErrNoProject) {
		t.Errorf("чужой проект дал %v", err)
	}
}

func TestListKeepsLeasedTasksAcrossStatuses(t *testing.T) {
	tr, fake := fixture(t)
	fake.addTask(&fakeTask{ID: "in-review", ColumnID: colReview, Timestamp: now.UnixMilli()})
	fake.setLease(testKey, "run-live", now.Add(time.Minute))
	refs, err := tr.List(testProject, []string{"Ready", "Review"})
	if err != nil || !slices.Equal(keys(refs), []string{testKey, "in-review"}) {
		t.Errorf("List = %v, %v", keys(refs), err)
	}
	if refs[0].RunID != "run-live" {
		t.Errorf("аренда не видна в List: %+v", refs[0])
	}
}

func TestListWithoutStatusesAsksNothing(t *testing.T) {
	tr, fake := fixture(t)
	before := len(fake.requests)
	refs, err := tr.List(testProject, nil)
	if refs != nil || err != nil || len(fake.requests) != before {
		t.Errorf("List(nil) = %v, %v; запросов %d", refs, err, len(fake.requests)-before)
	}
}

func TestListingDoesNotRefetchColumns(t *testing.T) {
	tr, fake := fixture(t)
	for range 3 {
		if _, err := tr.List(testProject, []string{"Ready", "Review"}); err != nil {
			t.Fatal(err)
		}
	}
	if n := fake.count("GET /api-v2/columns"); n != 1 {
		t.Errorf("колонки спрошены %d раз, ожидался 1 (на Open)", n)
	}
}

func TestListExpiredScansEveryConfiguredColumn(t *testing.T) {
	tr, fake := fixture(t)
	fake.addTask(&fakeTask{ID: "stuck-review", ColumnID: colReview, Timestamp: now.UnixMilli()})
	fake.addTask(&fakeTask{ID: "live-work", ColumnID: colWork, Timestamp: now.UnixMilli()})
	fake.setLease("stuck-review", "run-dead", now.Add(-time.Minute))
	fake.setLease("live-work", "run-live", now.Add(time.Minute))
	// testKey без аренды вовсе — не истёкшая, а свободная.

	refs, err := tr.ListExpired(testProject, now)
	if err != nil || !slices.Equal(keys(refs), []string{"stuck-review"}) {
		t.Errorf("ListExpired = %v, %v", keys(refs), err)
	}
	if fake.count("GET /api-v2/task-list?columnId="+colOutside) != 0 {
		t.Error("ListExpired заглянул в колонку вне графа")
	}
}

func TestListExpiredUsesGivenNow(t *testing.T) {
	tr, fake := fixture(t)
	fake.setLease(testKey, "run-1", now.Add(time.Minute))
	refs, _ := tr.ListExpired(testProject, now.Add(2*time.Minute))
	if !slices.Equal(keys(refs), []string{testKey}) {
		t.Errorf("ListExpired(now+2m) = %v", keys(refs))
	}
}

func TestListExpiredUnknownProject(t *testing.T) {
	tr, _ := fixture(t)
	if _, err := tr.ListExpired("OTHER", now); !errors.Is(err, tracker.ErrNoProject) {
		t.Errorf("чужой проект дал %v", err)
	}
}

// Spec: «Transitioning a task moves it to the corresponding column».
func TestTransitionMovesColumnOnly(t *testing.T) {
	tr, fake := fixture(t)
	if err := tr.Transition(testKey, tracker.BySystem(), "Review"); err != nil {
		t.Fatal(err)
	}
	if fake.task(testKey).ColumnID != colReview {
		t.Errorf("колонка = %q", fake.task(testKey).ColumnID)
	}
	body := fake.puts[len(fake.puts)-1]
	if len(body) != 1 || body["columnId"] != colReview {
		t.Errorf("тело PUT: %#v, ожидался только columnId", body)
	}
}

func TestTransitionFollowsOwnership(t *testing.T) {
	tr, fake := fixture(t)
	fake.setLease(testKey, "run-1", now.Add(time.Minute))
	if err := tr.Transition(testKey, tracker.ByRun("run-2"), "Review"); !errors.Is(err, tracker.ErrNotOwner) {
		t.Errorf("чужой прогон дал %v", err)
	}
	if err := tr.Transition(testKey, tracker.ByRun("run-1"), "Review"); err != nil {
		t.Errorf("владелец не смог перевести: %v", err)
	}
}

func TestTransitionUnknownStatusAsksNothing(t *testing.T) {
	tr, fake := fixture(t)
	before := len(fake.requests)
	if err := tr.Transition(testKey, tracker.BySystem(), "Nowhere"); err == nil {
		t.Error("незнакомый статус принят")
	}
	if len(fake.requests) != before {
		t.Error("запрос ушёл до проверки статуса")
	}
}

// Сервер проигнорировал фильтр по колонке — в очередь всё равно не попадает
// ничего из других колонок.
func TestListReadyKeepsOnlyItsColumnEvenIfServerIgnoresFilter(t *testing.T) {
	tr, fake := fixture(t)
	fake.ignoreColumnFilter = true
	fake.addTask(&fakeTask{ID: "in-review", ColumnID: colReview, Timestamp: now.UnixMilli()})
	refs, err := tr.ListReady(testProject, "Ready")
	if err != nil || !slices.Equal(keys(refs), []string{testKey}) {
		t.Errorf("ListReady = %v, %v", keys(refs), err)
	}
}

// Повторённый статус не удваивает задачи.
func TestListDeduplicatesStatuses(t *testing.T) {
	tr, fake := fixture(t)
	refs, err := tr.List(testProject, []string{"Ready", "Ready"})
	if err != nil || !slices.Equal(keys(refs), []string{testKey}) {
		t.Errorf("List = %v, %v", keys(refs), err)
	}
	if n := fake.count("GET /api-v2/task-list"); n != 1 {
		t.Errorf("колонка прочитана %d раз", n)
	}
}

// logInto — Logf, который копит строки для проверки.
func logInto(tr *Tracker) *[]string {
	var lines []string
	tr.Logf = func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
	return &lines
}

// Spec «One malformed task does not stop the queue»: листинги идут дальше,
// пропущенная карточка — в Logf, а не молча.
func TestListingsSkipCardWithUnreadableOfficeData(t *testing.T) {
	for name, bad := range map[string]map[string]any{
		"битые данные": officeAPIData(map[string]any{"attempts": "three"}),
		"новая версия": officeAPIData(map[string]any{"v": 2}),
	} {
		t.Run(name, func(t *testing.T) {
			tr, fake := fixture(t)
			lines := logInto(tr)
			fake.addTask(&fakeTask{ID: "broken", ColumnID: colReady, Timestamp: now.UnixMilli(), APIData: bad})
			fake.setLease(testKey, "run-dead", now.Add(-time.Minute))

			ready, err := tr.ListReady(testProject, "Ready")
			if err != nil || !slices.Equal(keys(ready), []string{testKey}) {
				t.Errorf("ListReady = %v, %v", keys(ready), err)
			}
			all, err := tr.List(testProject, []string{"Ready"})
			if err != nil || !slices.Equal(keys(all), []string{testKey}) {
				t.Errorf("List = %v, %v", keys(all), err)
			}
			expired, err := tr.ListExpired(testProject, now)
			if err != nil || !slices.Equal(keys(expired), []string{testKey}) {
				t.Errorf("ListExpired = %v, %v", keys(expired), err)
			}
			if len(*lines) != 3 {
				t.Fatalf("в Logf %d строк, ожидалось 3 (по одной на листинг): %q", len(*lines), *lines)
			}
			for _, line := range *lines {
				if !strings.HasPrefix(line, "yougile: задача broken пропущена: ") {
					t.Errorf("строка лога: %q", line)
				}
			}
		})
	}
}

// Карточка вне графа — не «данные офиса»: её листинг не глотает (сюда она
// попадает, только если сервер проигнорировал фильтр — tasksInColumn её
// отсеет раньше, так что проверяем, что skip узкий, на toTask напрямую).
func TestSkipIsOnlyForOfficeData(t *testing.T) {
	tr, _ := fixture(t)
	_, _, err := tr.toTask(taskDTO{ID: "x", ColumnID: colOutside})
	if errors.Is(err, ErrOfficeData) {
		t.Errorf("колонка вне графа выдана за ErrOfficeData: %v", err)
	}
}

// Узость самого collect: любая другая ошибка toTask (здесь — колонка,
// которой не сопоставлен статус) валит листинг громко и в Logf не уходит.
// tasksInColumn отсеивает чужие колонки, поэтому расхождение карты колонок
// и карты статусов создаём напрямую.
func TestListingsFailLoudOnErrorsOtherThanOfficeData(t *testing.T) {
	tr, _ := fixture(t)
	lines := logInto(tr)
	delete(tr.columnStatus, colReady)

	_, err := tr.ListReady(testProject, "Ready")
	if !errors.Is(err, ErrUnmappedColumn) {
		t.Errorf("ListReady дал %v, ожидался ErrUnmappedColumn", err)
	}
	_, err = tr.List(testProject, []string{"Ready"})
	if !errors.Is(err, ErrUnmappedColumn) {
		t.Errorf("List дал %v, ожидался ErrUnmappedColumn", err)
	}
	if len(*lines) != 0 {
		t.Errorf("чужая ошибка ушла в Logf: %q", *lines)
	}
}

func TestOpenDefaultsLogf(t *testing.T) {
	tr, _ := fixture(t)
	if tr.Logf == nil {
		t.Error("Logf по умолчанию не задан — пропуск карточки упал бы паникой")
	}
}

// Гейт очерёдности (pipeline.UnmetDependencies) и runner ls читают TaskRef
// из ListReady/List, а не Task из Get: зависимость обязана доехать до ref.
func TestDependsOnReachesRefsAndGet(t *testing.T) {
	tr, fake := fixture(t)
	fake.tasks[testKey].APIData = officeAPIData(map[string]any{"depends_on": []any{"task-base"}})
	want := []string{"task-base"}

	ready, err := tr.ListReady(testProject, "Ready")
	if err != nil || len(ready) != 1 || !slices.Equal(ready[0].DependsOn, want) {
		t.Errorf("ListReady: %+v, %v", ready, err)
	}
	all, err := tr.List(testProject, []string{"Ready"})
	if err != nil || len(all) != 1 || !slices.Equal(all[0].DependsOn, want) {
		t.Errorf("List: %+v, %v", all, err)
	}
	task, err := tr.Get(testKey)
	if err != nil || !slices.Equal(task.DependsOn, want) {
		t.Errorf("Get: %+v, %v", task.DependsOn, err)
	}
}
