package yougile

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kao73/virtual-office/internal/tracker"
)

func TestGetMapsColumnAndAPIData(t *testing.T) {
	tr, fake := fixture(t)
	until := now.Add(30 * time.Minute)
	fake.tasks[testKey].APIData = map[string]any{
		"lease":    map[string]any{"owner": "implementer", "run_id": "run-1", "lease_until": until.Format(time.RFC3339Nano)},
		"attempts": 2, "human_wait": true, "labels": []any{"m-1"},
	}

	task, err := tr.Get(testKey)
	if err != nil {
		t.Fatal(err)
	}
	want := tracker.Task{
		Key: testKey, Project: testProject, Summary: "First task", Description: "Do it", Status: "Ready",
		Labels: []string{"m-1"}, Owner: "implementer", RunID: "run-1", LeaseUntil: until,
		Attempts: 2, HumanFlag: true,
		Comments: []tracker.Comment{}, // Get отдаёт пустую, не nil, переписку
	}
	task.LeaseUntil = task.LeaseUntil.UTC()
	if !reflect.DeepEqual(task, want) {
		t.Errorf("Get =\n%+v\nожидалось\n%+v", task, want)
	}
}

func TestGetReadsCommentsOldestFirstWithAuthorEmails(t *testing.T) {
	tr, fake := fixture(t)
	fake.messages[testKey] = []fakeMessage{
		{ID: 1000, From: humanUserID, Text: "вопрос человека"},
		{ID: 2000, From: officeUserID, Text: "[office run:r1 role:analyst]\nответ"},
	}
	task, err := tr.Get(testKey)
	if err != nil {
		t.Fatal(err)
	}
	want := []tracker.Comment{
		{ID: "1000", Author: "human@example.com", Created: time.UnixMilli(1000).UTC(), Body: "вопрос человека"},
		{ID: "2000", Author: "office@example.com", Created: time.UnixMilli(2000).UTC(), Body: "[office run:r1 role:analyst]\nответ"},
	}
	if !reflect.DeepEqual(task.Comments, want) {
		t.Errorf("комментарии:\n%+v\nожидалось\n%+v", task.Comments, want)
	}
}

func TestGetSkipsDeletedMessages(t *testing.T) {
	tr, fake := fixture(t)
	fake.messages[testKey] = []fakeMessage{{ID: 1000, From: humanUserID, Text: "стёрто", Deleted: true}}
	task, err := tr.Get(testKey)
	if err != nil || len(task.Comments) != 0 {
		t.Errorf("удалённое сообщение попало в переписку: %+v, %v", task.Comments, err)
	}
}

func TestGetCachesAuthorLookups(t *testing.T) {
	tr, fake := fixture(t)
	fake.messages[testKey] = []fakeMessage{
		{ID: 1, From: humanUserID, Text: "a"}, {ID: 2, From: humanUserID, Text: "b"}, {ID: 3, From: humanUserID, Text: "c"},
	}
	for range 2 {
		if _, err := tr.Get(testKey); err != nil {
			t.Fatal(err)
		}
	}
	if n := fake.count("GET /api-v2/users/" + humanUserID); n != 1 {
		t.Errorf("email автора спрошен %d раз, ожидался 1", n)
	}
}

// Уволенного из компании автора сервер не отдаёт. Его id — не учётка офиса,
// значит слова человека; ронять из-за этого Get нельзя.
func TestGetKeepsCommentOfVanishedAuthor(t *testing.T) {
	tr, fake := fixture(t)
	fake.messages[testKey] = []fakeMessage{{ID: 1, From: "user-gone", Text: "старое"}}
	task, err := tr.Get(testKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(task.Comments) != 1 || task.Comments[0].Author != "user-gone" {
		t.Errorf("комментарий пропавшего автора: %+v", task.Comments)
	}
}

func TestGetUnknownTaskIsNotFound(t *testing.T) {
	tr, _ := fixture(t)
	if _, err := tr.Get("missing"); !errors.Is(err, tracker.ErrNotFound) {
		t.Errorf("Get(missing) дал %v", err)
	}
}

// Review Focus #1: карточку утащили в колонку вне графа — громкая ошибка,
// а не выдуманный статус.
func TestGetTaskInUnmappedColumnFails(t *testing.T) {
	tr, fake := fixture(t)
	fake.tasks[testKey].ColumnID = colOutside
	_, err := tr.Get(testKey)
	if !errors.Is(err, ErrUnmappedColumn) || !strings.Contains(err.Error(), colOutside) {
		t.Errorf("задача вне графа дала %v", err)
	}
}

func TestGetMalformedAPIDataNamesTask(t *testing.T) {
	tr, fake := fixture(t)
	fake.tasks[testKey].APIData = map[string]any{"lease": "garbage"}
	_, err := tr.Get(testKey)
	if err == nil || !strings.Contains(err.Error(), testKey) {
		t.Errorf("битый apiData дал %v, ожидалась ошибка с ключом задачи", err)
	}
}
