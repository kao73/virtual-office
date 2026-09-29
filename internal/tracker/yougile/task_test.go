package yougile

import (
	"encoding/hex"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kao73/virtual-office/internal/tracker"
)

func TestGetMapsColumnAndAPIData(t *testing.T) {
	tr, fake := fixture(t)
	until := now.Add(30 * time.Minute)
	fake.tasks[testKey].APIData = officeAPIData(map[string]any{
		"lease":    map[string]any{"owner": "implementer", "run_id": "run-1", "lease_until": until.Format(time.RFC3339Nano)},
		"attempts": 2, "human_wait": true, "labels": []any{"m-1"},
	})

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
	fake.tasks[testKey].APIData = officeAPIData(map[string]any{"lease": "garbage"})
	_, err := tr.Get(testKey)
	if err == nil || !strings.Contains(err.Error(), testKey) {
		t.Errorf("битый apiData дал %v, ожидалась ошибка с ключом задачи", err)
	}
}

func TestCreateTaskPostsIntoCreateColumn(t *testing.T) {
	tr, fake := fixture(t)
	ref, err := tr.CreateTask(testProject, tracker.TaskInput{
		Summary: "Child", Description: "prose", DescriptionAppend: "parent verbatim", Labels: []string{"split:VO-1:a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Key != "task-new-1" || ref.Status != "Ready" || ref.Summary != "Child" || ref.Project != testProject {
		t.Errorf("ref: %+v", ref)
	}
	body := fake.posts[0]
	if body["title"] != "Child" || body["description"] != "prose\n\nparent verbatim" || body["columnId"] != colReady {
		t.Errorf("тело POST: %#v", body)
	}
	labels := body["apiData"].(map[string]any)[keyNamespace].(map[string]any)["labels"]
	if !reflect.DeepEqual(labels, []any{"split:VO-1:a"}) {
		t.Errorf("метки в apiData: %#v", labels)
	}
	key, _ := body["idempotencyKey"].(string)
	if _, err := hex.DecodeString(key); err != nil || len(key) != 64 {
		t.Errorf("idempotencyKey = %q, ожидался hex SHA-256", key)
	}
}

// Spec: «A repeated create returns the original task».
func TestCreateTaskRepeatedReturnsSameTask(t *testing.T) {
	tr, fake := fixture(t)
	input := tracker.TaskInput{Summary: "Child", Description: "prose", Labels: []string{"m-1"}}
	first, err := tr.CreateTask(testProject, input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := tr.CreateTask(testProject, input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Key != second.Key {
		t.Errorf("повтор создал новую задачу: %s и %s", first.Key, second.Key)
	}
	if len(fake.tasks) != 2 { // testKey + одна созданная
		t.Errorf("задач в трекере %d, ожидалось 2", len(fake.tasks))
	}
}

// Два ребёнка split с одинаковым текстом, но разными метками — разные задачи.
func TestCreateTaskDifferentLabelsAreDifferentTasks(t *testing.T) {
	tr, _ := fixture(t)
	a, _ := tr.CreateTask(testProject, tracker.TaskInput{Summary: "Same", Description: "same", Labels: []string{"split:P:a"}})
	b, _ := tr.CreateTask(testProject, tracker.TaskInput{Summary: "Same", Description: "same", Labels: []string{"split:P:b"}})
	if a.Key == b.Key {
		t.Errorf("разные метки слились в одну задачу %s", a.Key)
	}
}

func TestIdempotencyKeySeparatesParts(t *testing.T) {
	if idempotencyKey("ab", "c") == idempotencyKey("a", "bc") {
		t.Error("части склеились без разделителя")
	}
	if idempotencyKey("a", "b") != idempotencyKey("a", "b") {
		t.Error("ключ не детерминирован")
	}
}

func TestJoinDescription(t *testing.T) {
	cases := map[[2]string]string{
		{"prose", ""}:       "prose",
		{"", "appendix"}:    "appendix",
		{"prose", "append"}: "prose\n\nappend",
	}
	for in, want := range cases {
		if got := joinDescription(in[0], in[1]); got != want {
			t.Errorf("joinDescription(%q, %q) = %q", in[0], in[1], got)
		}
	}
}

func TestCreateTaskWithoutCreateStatusPostsNothing(t *testing.T) {
	fake := newFake(t)
	tr, err := openWith(t, fake, func(c *Config) { c.CreateStatus = "" })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tr.CreateTask(testProject, tracker.TaskInput{Summary: "x"}); err == nil {
		t.Error("создание без create_status принято")
	}
	if len(fake.posts) != 0 {
		t.Error("POST ушёл без create_status")
	}
}

func TestCreateTaskUnknownProject(t *testing.T) {
	tr, _ := fixture(t)
	if _, err := tr.CreateTask("OTHER", tracker.TaskInput{Summary: "x"}); !errors.Is(err, tracker.ErrNoProject) {
		t.Errorf("чужой проект дал %v", err)
	}
}

// Сбой, кроме 404, при поиске автора — ошибка Get, а не «автор = id»: иначе
// комментарий офиса сошёл бы за слова человека. И сбой не кэшируется.
func TestGetFailsOnTransientAuthorLookupError(t *testing.T) {
	tr, fake := fixture(t)
	fake.messages[testKey] = []fakeMessage{{ID: 1, From: officeUserID, Text: "вопрос"}}
	fake.fail["GET /api-v2/users/"+officeUserID] = http.StatusTooManyRequests
	if _, err := tr.Get(testKey); err == nil {
		t.Fatal("429 на авторе не дал ошибки")
	}
	delete(fake.fail, "GET /api-v2/users/"+officeUserID)
	task, err := tr.Get(testKey)
	if err != nil {
		t.Fatal(err)
	}
	if task.Comments[0].Author != "office@example.com" {
		t.Errorf("после сбоя автор = %q, сбой закэширован", task.Comments[0].Author)
	}
}

// Пользователь без email — как пропавший: автор — его id, а не пустая строка.
func TestGetNamesAuthorWithoutEmailByID(t *testing.T) {
	tr, fake := fixture(t)
	fake.users["user-noemail"] = ""
	fake.messages[testKey] = []fakeMessage{{ID: 1, From: "user-noemail", Text: "привет"}}
	task, err := tr.Get(testKey)
	if err != nil {
		t.Fatal(err)
	}
	if task.Comments[0].Author != "user-noemail" {
		t.Errorf("автор без email = %q", task.Comments[0].Author)
	}
}

// Ответ на создание без id: задача, возможно, уже есть — сказать об этом,
// а не падать на GET /tasks/ с непонятной ошибкой.
func TestCreateTaskRejectsAnswerWithoutID(t *testing.T) {
	tr, fake := fixture(t)
	fake.createWithoutID = true
	_, err := tr.CreateTask(testProject, tracker.TaskInput{Summary: "Child", Labels: []string{"m-1"}})
	if err == nil || !strings.Contains(err.Error(), "без id") {
		t.Errorf("ответ без id дал %v", err)
	}
	if fake.count("GET /api-v2/tasks/") != 0 {
		t.Error("пошли читать задачу с пустым id")
	}
}

// Порядок меток не меняет задачу: повтор с переставленными метками — тот же ключ.
func TestCreateTaskLabelOrderDoesNotMatter(t *testing.T) {
	tr, _ := fixture(t)
	a, _ := tr.CreateTask(testProject, tracker.TaskInput{Summary: "Same", Labels: []string{"x", "y"}})
	b, _ := tr.CreateTask(testProject, tracker.TaskInput{Summary: "Same", Labels: []string{"y", "x"}})
	if a.Key != b.Key {
		t.Errorf("перестановка меток дала новую задачу: %s и %s", a.Key, b.Key)
	}
}

// Ключ идемпотентности общий на компанию: одинаковый ввод в разных проектах —
// разные задачи, иначе трекер получил бы задачу соседа.
func TestCreateTaskKeyDependsOnProject(t *testing.T) {
	fake := newFake(t)
	fake.projects["proj-2"] = true
	fake.boards = append(fake.boards, map[string]any{"id": "board-2", "title": "Two", "projectId": "proj-2"})
	fake.columns = append(fake.columns, map[string]any{"id": "col-2", "title": "Ready", "boardId": "board-2"})
	root := serve(t, fake)
	one, err := Open(testConfig(root))
	if err != nil {
		t.Fatal(err)
	}
	two, err := Open(Config{BaseURL: root, APIKey: "k", ProjectID: "proj-2",
		ColumnIDs: map[string]string{"Ready": "col-2"}, CreateStatus: "Ready"})
	if err != nil {
		t.Fatal(err)
	}
	input := tracker.TaskInput{Summary: "Same", Labels: []string{"m"}}
	a, errA := one.CreateTask(testProject, input)
	b, errB := two.CreateTask("proj-2", input)
	if errA != nil || errB != nil || a.Key == b.Key {
		t.Errorf("проекты делят задачу: %v/%v, %v/%v", a.Key, errA, b.Key, errB)
	}
}

// Системные сообщения чата (перенос карточки, смена исполнителя) — не
// реплики: попади они в Comments, HumanReply принял бы их за ответ человека.
// По умолчанию API их не отдаёт; просим явно, чтобы не зависеть от умолчания.
func TestGetAsksChatWithoutSystemMessages(t *testing.T) {
	tr, fake := fixture(t)
	if _, err := tr.Get(testKey); err != nil {
		t.Fatal(err)
	}
	if fake.count("GET /api-v2/chats/"+testKey+"/messages?includeSystem=false") != 1 {
		t.Errorf("чат запрошен без includeSystem=false: %v", fake.requests)
	}
}

// Whoami уже знает id и email офиса — свои комментарии не стоят запроса к /users.
// Email в YouGile может прийти в любом регистре, а also_agents и учётку
// офиса сравнивают с автором строкой: обе стороны — в нижнем регистре.
func TestAuthorAndWhoamiEmailsAreLowercased(t *testing.T) {
	tr, fake := fixture(t)
	fake.users[officeUserID] = "Office@Example.com"
	fake.users[humanUserID] = " Human@Example.COM "
	fake.messages[testKey] = []fakeMessage{{ID: 1, From: humanUserID, Text: "вопрос"}}
	who, err := tr.Whoami()
	if err != nil || who != "office@example.com" {
		t.Errorf("Whoami = %q, %v", who, err)
	}
	task, err := tr.Get(testKey)
	if err != nil || task.Comments[0].Author != "human@example.com" {
		t.Errorf("автор = %+v, %v", task.Comments, err)
	}
}

func TestWhoamiSeedsAuthorCache(t *testing.T) {
	tr, fake := fixture(t)
	fake.messages[testKey] = []fakeMessage{{ID: 1, From: officeUserID, Text: "вопрос"}}
	if _, err := tr.Whoami(); err != nil {
		t.Fatal(err)
	}
	task, err := tr.Get(testKey)
	if err != nil || task.Comments[0].Author != "office@example.com" {
		t.Fatalf("Get: %+v, %v", task.Comments, err)
	}
	if n := fake.count("GET /api-v2/users/" + officeUserID); n != 0 {
		t.Errorf("автор-офис запрошен %d раз", n)
	}
}

// Файл, прикреплённый человеком в чат, и ссылка в описании — вложения
// задачи, по порядку: описание, потом чат.
func TestGetListsAttachmentsFromDescriptionAndChat(t *testing.T) {
	tr, fake := fixture(t)
	fake.tasks[testKey].Description = `<p><a href="https://ru.yougile.com/user-data/` + uuid1 +
		`/%D0%A2%D0%97.pdf?previews[]=x">ТЗ.pdf</a></p>`
	fake.messages[testKey] = []fakeMessage{
		{ID: 1000, From: humanUserID, Text: "/root/#file:/user-data/" + uuid2 + "/%25D1%2581%25D1%2585%25D0%25B5%25D0%25BC%25D0%25B0.png"},
	}
	task, err := tr.Get(testKey)
	if err != nil {
		t.Fatal(err)
	}
	want := []tracker.AttachmentRef{{ID: uuid1, Name: "ТЗ.pdf"}, {ID: uuid2, Name: "схема.png"}}
	if !reflect.DeepEqual(task.Attachments, want) {
		t.Errorf("Attachments = %+v, ожидалось %+v", task.Attachments, want)
	}
}

// Сообщение-файл остаётся в переписке — ответ файлом тоже ответ, — но
// телом «[вложение: имя]», а не сырой служебной строкой.
func TestGetRendersFileMessageAsAttachmentComment(t *testing.T) {
	tr, fake := fixture(t)
	fake.messages[testKey] = []fakeMessage{
		{ID: 1000, From: officeUserID, Text: "[office run:r1 role:analyst outcome:question]\n## Вопросы\n1. Где ТЗ?"},
		{ID: 2000, From: humanUserID, Text: "/root/#file:/user-data/" + uuid1 + "/%25D0%25A2%25D0%2597.txt"},
	}
	task, err := tr.Get(testKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(task.Comments) != 2 || task.Comments[1].Body != "[вложение: ТЗ.txt]" || task.Comments[1].Author != "human@example.com" {
		t.Fatalf("переписка: %+v", task.Comments)
	}
	reply, _, found := tracker.HumanReply(task.Comments, []string{"office@example.com"})
	if !found || reply.ID != "2000" {
		t.Errorf("ответ человека файлом не засчитан: %+v, %v", reply, found)
	}
}

// Удалённое сообщение-файл не даёт ни вложения, ни реплики: фильтр живёт
// в chat(), до fileLinks и comments.
func TestGetDeletedChatFileMessageYieldsNoAttachmentNoComment(t *testing.T) {
	tr, fake := fixture(t)
	fake.messages[testKey] = []fakeMessage{
		{ID: 1000, From: humanUserID, Text: "/root/#file:/user-data/" + uuid1 + "/a.txt", Deleted: true},
	}
	task, err := tr.Get(testKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(task.Attachments) != 0 || len(task.Comments) != 0 {
		t.Errorf("Attachments = %+v, Comments = %+v; ожидались пустые", task.Attachments, task.Comments)
	}
}
