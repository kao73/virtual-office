//go:build yougile_live

// Живая проверка против песочницы office-polygon. Никогда — против Clens:
// это живая клиентская доска. Запуск:
//
//	YOUGILE_API_KEY=… YOUGILE_PROJECT_ID=<office-polygon> \
//	YOUGILE_COLUMNS='Ready=<id>,InProgress=<id>' \
//	go test -tags yougile_live -run TestLive -count=1 -v ./internal/tracker/yougile/
//
// Запросов ~25 — под rate limit 50/мин на компанию; два запуска подряд
// могут в него упереться.
package yougile

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kao73/virtual-office/internal/tracker"
)

func liveTracker(t *testing.T) *Tracker {
	t.Helper()
	key, project, columns := os.Getenv("YOUGILE_API_KEY"), os.Getenv("YOUGILE_PROJECT_ID"), os.Getenv("YOUGILE_COLUMNS")
	if key == "" || project == "" || columns == "" {
		t.Fatal("нужны YOUGILE_API_KEY, YOUGILE_PROJECT_ID, YOUGILE_COLUMNS (Status=columnId,…) — только песочница office-polygon")
	}
	base := os.Getenv("YOUGILE_BASE_URL")
	if base == "" {
		base = "https://yougile.com"
	}
	ids := map[string]string{}
	for _, pair := range strings.Split(columns, ",") {
		status, id, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok {
			t.Fatalf("YOUGILE_COLUMNS: %q не вида Status=columnId", pair)
		}
		ids[status] = id
	}
	for _, need := range []string{"Ready", "InProgress"} {
		if ids[need] == "" {
			t.Fatalf("YOUGILE_COLUMNS: нет колонки для %s", need)
		}
	}
	tr, err := Open(Config{BaseURL: base, APIKey: key, ProjectID: project, ColumnIDs: ids, CreateStatus: "Ready"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return tr
}

func TestLiveLifecycle(t *testing.T) {
	tr := liveTracker(t)
	project := tr.cfg.ProjectID

	who, err := tr.Whoami()
	if err != nil || who == "" {
		t.Fatalf("Whoami = %q, %v", who, err)
	}

	stamp := time.Now().UTC().Format("20060102T150405.000")
	marker := "office-live:" + stamp
	input := tracker.TaskInput{Summary: "office live smoke " + stamp, Description: "created by live_test.go", Labels: []string{marker}}

	// Spec: повторное создание возвращает ту же задачу.
	first, err := tr.CreateTask(project, input)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	t.Cleanup(func() { _ = tr.putTask(first.Key, map[string]any{"deleted": true}) })
	second, err := tr.CreateTask(project, input)
	if err != nil || second.Key != first.Key {
		t.Fatalf("повторное создание: %s vs %s, %v", first.Key, second.Key, err)
	}
	if first.Status != "Ready" {
		t.Errorf("новая задача в статусе %q", first.Status)
	}

	// Чужой ключ apiData — должен пережить все наши записи.
	raw, err := tr.getRaw(first.Key)
	if err != nil {
		t.Fatal(err)
	}
	data, err := decodeAPIData(raw.APIData)
	if err != nil {
		t.Fatal(err)
	}
	data.extra = map[string]json.RawMessage{"live_probe": json.RawMessage(`"keep-me"`)}
	if err := tr.putTask(first.Key, map[string]any{"apiData": data.encode()}); err != nil {
		t.Fatal(err)
	}

	// Claim: аренда + рабочая колонка одним PUT, перечитывание.
	runID := "live-run-" + stamp
	if err := tr.Claim(tracker.ClaimRequest{
		Key: first.Key, RunID: runID, Owner: "live", LeaseUntil: time.Now().Add(10 * time.Minute),
		ExpectStatus: "Ready", WorkingStatus: "InProgress",
	}); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	later := time.Now().Add(20 * time.Minute).UTC().Truncate(time.Millisecond)
	if err := tr.Renew(first.Key, runID, later); err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if err := tr.SetAttempts(first.Key, tracker.ByRun(runID), 2); err != nil {
		t.Fatalf("SetAttempts: %v", err)
	}
	if err := tr.SetHumanFlag(first.Key, tracker.ByRun(runID), true); err != nil {
		t.Fatalf("SetHumanFlag: %v", err)
	}

	body := "[office run:" + runID + " role:live]\nпроза с <угловыми> & амперсандом\n\n## Вопросы\n1. да?"
	if err := tr.Comment(first.Key, tracker.ByRun(runID), body); err != nil {
		t.Fatalf("Comment: %v", err)
	}

	task, err := tr.Get(first.Key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if task.Status != "InProgress" || task.RunID != runID || !task.LeaseUntil.Equal(later) ||
		task.Attempts != 2 || !task.HumanFlag {
		t.Errorf("после захвата/продления: %+v", task)
	}
	if len(task.Comments) == 0 {
		t.Fatal("комментарий не прочитан обратно")
	}
	last := task.Comments[len(task.Comments)-1]
	if last.Body != body {
		t.Errorf("text исказился на сервере:\n%q\nожидалось\n%q", last.Body, body)
	}
	if last.Author != who {
		t.Errorf("автор %q, а Whoami %q", last.Author, who)
	}

	// Release: аренда снята явным null, статус и счётчики на месте, чужой ключ цел.
	if err := tr.Release(first.Key, tracker.ByRun(runID)); err != nil {
		t.Fatalf("Release: %v", err)
	}
	raw, err = tr.getRaw(first.Key)
	if err != nil {
		t.Fatal(err)
	}
	released, data, err := tr.toTask(raw)
	if err != nil {
		t.Fatal(err)
	}
	if released.RunID != "" || released.Status != "InProgress" || released.Attempts != 2 || !released.HumanFlag {
		t.Errorf("после Release: %+v", released)
	}
	if string(data.extra["live_probe"]) != `"keep-me"` {
		t.Errorf("чужой ключ apiData потерян: %s", raw.APIData)
	}

	found, err := tr.FindByMarker(project, marker)
	if err != nil || len(found) != 1 || found[0].Key != first.Key {
		t.Errorf("FindByMarker = %+v, %v", found, err)
	}
	ready, err := tr.ListReady(project, "InProgress")
	if err != nil || !slices.ContainsFunc(ready, func(r tracker.TaskRef) bool { return r.Key == first.Key }) {
		t.Errorf("ListReady(InProgress) не видит освобождённую задачу: %v", err)
	}
}
