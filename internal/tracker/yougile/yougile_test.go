package yougile

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kao73/virtual-office/internal/tracker"
)

var now = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

const (
	testProject  = "proj-1"
	testKey      = "task-1"
	colReady     = "col-ready"
	colWork      = "col-work"
	colReview    = "col-review"
	colOutside   = "col-ideas" // есть на доске проекта, но ни одному статусу не сопоставлена
	officeUserID = "user-office"
	humanUserID  = "user-human"
)

// fakeTask — задача в памяти фейкового YouGile.
type fakeTask struct {
	ID, Title, Description, ColumnID string
	Timestamp                        int64 // мс, как timestamp в TaskDto
	Archived, Deleted                bool
	APIData                          map[string]any
}

func (t *fakeTask) dto() map[string]any {
	m := map[string]any{
		"id": t.ID, "title": t.Title, "description": t.Description, "columnId": t.ColumnID,
		"timestamp": t.Timestamp, "archived": t.Archived, "deleted": t.Deleted,
	}
	if t.APIData != nil {
		m["apiData"] = t.APIData
	}
	return m
}

// fakeMessage — сообщение чата задачи. ID — время создания в мс, как в ChatMessageDto.
type fakeMessage struct {
	ID      int64
	From    string
	Text    string
	HTML    string
	Deleted bool
}

// fakeYouGile — минимальный YouGile: отвечает на те запросы, которые шлёт
// адаптер, и запоминает их, чтобы тест проверял и исход, и форму запроса.
type fakeYouGile struct {
	t  *testing.T
	mu sync.Mutex

	projects map[string]bool
	boards   []map[string]any // {"id","title","projectId"}
	columns  []map[string]any // {"id","title","boardId"}
	tasks    map[string]*fakeTask
	order    []string // порядок создания задач
	messages map[string][]fakeMessage
	users    map[string]string // id → email
	me       string
	idem     map[string]string // idempotencyKey → id задачи
	nextID   int

	pageCap       int  // >0 — сервер режет страницу до этого размера, что бы ни просили
	endlessPaging bool // сервер всегда говорит next=true и отдаёт первый элемент

	requests  []string // "METHOD /api-v2/path?query"
	lastAuth  string
	puts      []map[string]any // тела PUT /tasks/{id}
	posts     []map[string]any // тела POST /tasks
	chatPosts []map[string]any // тела POST /chats/{id}/messages

	// afterPut вызывается после применения PUT /tasks/{id}, до ответа клиенту,
	// без удержания f.mu: так выглядит конкурент, записавший своё следом за нами.
	afterPut func(id string)
	// fail — "METHOD /api-v2/path" → код, которым ответить вместо обработки.
	fail map[string]int
}

func newFake(t *testing.T) *fakeYouGile {
	f := &fakeYouGile{
		t:        t,
		projects: map[string]bool{testProject: true},
		boards: []map[string]any{
			{"id": "board-1", "title": "Office", "projectId": testProject},
			{"id": "board-other", "title": "Other", "projectId": "proj-other"},
		},
		columns: []map[string]any{
			{"id": colReady, "title": "Ready", "boardId": "board-1"},
			{"id": colWork, "title": "In Progress", "boardId": "board-1"},
			{"id": colReview, "title": "Review", "boardId": "board-1"},
			{"id": colOutside, "title": "Ideas", "boardId": "board-1"},
			{"id": "col-foreign", "title": "Foreign", "boardId": "board-other"},
		},
		tasks:    map[string]*fakeTask{},
		messages: map[string][]fakeMessage{},
		users:    map[string]string{officeUserID: "office@example.com", humanUserID: "human@example.com"},
		me:       officeUserID,
		idem:     map[string]string{},
		fail:     map[string]int{},
	}
	f.addTask(&fakeTask{ID: testKey, Title: "First task", Description: "Do it", ColumnID: colReady,
		Timestamp: now.Add(-time.Hour).UnixMilli()})
	return f
}

func (f *fakeYouGile) addTask(task *fakeTask) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tasks[task.ID] = task
	f.order = append(f.order, task.ID)
}

// task — копия задачи для проверки в тесте.
func (f *fakeYouGile) task(id string) fakeTask {
	f.mu.Lock()
	defer f.mu.Unlock()
	return *f.tasks[id]
}

// setLease — записать аренду в apiData мимо адаптера, как это сделал бы другой прогон.
func (f *fakeYouGile) setLease(id, runID string, until time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	task := f.tasks[id]
	if task.APIData == nil {
		task.APIData = map[string]any{}
	}
	task.APIData["lease"] = map[string]any{"owner": "someone", "run_id": runID, "lease_until": until.Format(time.RFC3339Nano)}
}

// count — сколько дошло запросов с данным префиксом "METHOD /api-v2/path".
func (f *fakeYouGile) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

func number(raw string, fallback int) int {
	if n, err := strconv.Atoi(raw); err == nil {
		return n
	}
	return fallback
}

func (f *fakeYouGile) page(items []any, q url.Values) map[string]any {
	limit, offset := number(q.Get("limit"), 1000), number(q.Get("offset"), 0)
	if f.pageCap > 0 && limit > f.pageCap {
		limit = f.pageCap
	}
	if f.endlessPaging {
		return map[string]any{
			"paging":  map[string]any{"count": 1, "limit": limit, "offset": offset, "next": true},
			"content": items[:1],
		}
	}
	offset = min(offset, len(items))
	end := min(offset+limit, len(items))
	return map[string]any{
		"paging":  map[string]any{"count": end - offset, "limit": limit, "offset": offset, "next": end < len(items)},
		"content": items[offset:end],
	}
}

func (f *fakeYouGile) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
	f.lastAuth = r.Header.Get("Authorization")
	if code, ok := f.fail[r.Method+" "+r.URL.Path]; ok {
		f.mu.Unlock()
		http.Error(w, `{"error":"forced"}`, code)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, apiPrefix)
	status, body := f.route(r.Method, path, r)
	var hook func()
	if r.Method == http.MethodPut && strings.HasPrefix(path, "/tasks/") && status == http.StatusOK && f.afterPut != nil {
		id, after := strings.TrimPrefix(path, "/tasks/"), f.afterPut
		hook = func() { after(id) }
	}
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// route вызывается под f.mu.
func (f *fakeYouGile) route(method, path string, r *http.Request) (int, any) {
	q := r.URL.Query()
	notFound := map[string]any{"error": "not found"}
	decode := func() map[string]any {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			f.t.Errorf("%s %s: тело не разобрано: %v", method, path, err)
		}
		return body
	}

	switch {
	case method == http.MethodGet && path == "/users/me":
		return http.StatusOK, map[string]any{"id": f.me, "email": f.users[f.me]}

	case method == http.MethodGet && strings.HasPrefix(path, "/users/"):
		id := strings.TrimPrefix(path, "/users/")
		email, ok := f.users[id]
		if !ok {
			return http.StatusNotFound, notFound
		}
		return http.StatusOK, map[string]any{"id": id, "email": email}

	case method == http.MethodGet && strings.HasPrefix(path, "/projects/"):
		id := strings.TrimPrefix(path, "/projects/")
		if !f.projects[id] {
			return http.StatusNotFound, notFound
		}
		return http.StatusOK, map[string]any{"id": id, "title": "polygon"}

	case method == http.MethodGet && path == "/boards":
		var items []any
		for _, b := range f.boards {
			if b["projectId"] == q.Get("projectId") {
				items = append(items, b)
			}
		}
		return http.StatusOK, f.page(items, q)

	case method == http.MethodGet && path == "/columns":
		var items []any
		for _, c := range f.columns {
			if c["boardId"] == q.Get("boardId") {
				items = append(items, c)
			}
		}
		return http.StatusOK, f.page(items, q)

	case method == http.MethodGet && path == "/task-list":
		var items []any
		for _, id := range f.order {
			task := f.tasks[id]
			if task.Deleted {
				continue
			}
			if col := q.Get("columnId"); col != "" && task.ColumnID != col {
				continue
			}
			items = append(items, task.dto())
		}
		return http.StatusOK, f.page(items, q)

	case method == http.MethodGet && strings.HasPrefix(path, "/tasks/"):
		task, ok := f.tasks[strings.TrimPrefix(path, "/tasks/")]
		if !ok || task.Deleted {
			return http.StatusNotFound, notFound
		}
		return http.StatusOK, task.dto()

	case method == http.MethodPost && path == "/tasks":
		body := decode()
		f.posts = append(f.posts, body)
		key, _ := body["idempotencyKey"].(string)
		if id, ok := f.idem[key]; ok && key != "" {
			return http.StatusCreated, map[string]any{"id": id}
		}
		f.nextID++
		id := fmt.Sprintf("task-new-%d", f.nextID)
		task := &fakeTask{ID: id, Timestamp: now.UnixMilli() + int64(f.nextID)}
		task.Title, _ = body["title"].(string)
		task.Description, _ = body["description"].(string)
		task.ColumnID, _ = body["columnId"].(string)
		task.APIData, _ = body["apiData"].(map[string]any)
		f.tasks[id] = task
		f.order = append(f.order, id)
		if key != "" {
			f.idem[key] = id
		}
		return http.StatusCreated, map[string]any{"id": id}

	case method == http.MethodPut && strings.HasPrefix(path, "/tasks/"):
		id := strings.TrimPrefix(path, "/tasks/")
		task, ok := f.tasks[id]
		if !ok {
			return http.StatusNotFound, notFound
		}
		body := decode()
		f.puts = append(f.puts, body)
		if v, ok := body["columnId"].(string); ok {
			task.ColumnID = v
		}
		if v, ok := body["apiData"].(map[string]any); ok {
			task.APIData = v // замена целиком: адаптер обязан писать объект полностью
		}
		if v, ok := body["deleted"].(bool); ok {
			task.Deleted = v
		}
		return http.StatusOK, map[string]any{"id": id}

	case strings.HasPrefix(path, "/chats/") && strings.HasSuffix(path, "/messages"):
		chat := strings.TrimSuffix(strings.TrimPrefix(path, "/chats/"), "/messages")
		if _, ok := f.tasks[chat]; !ok {
			return http.StatusNotFound, notFound
		}
		if method == http.MethodPost {
			body := decode()
			f.chatPosts = append(f.chatPosts, body)
			msg := fakeMessage{ID: now.UnixMilli() + int64(len(f.messages[chat])) + 1, From: f.me}
			msg.Text, _ = body["text"].(string)
			msg.HTML, _ = body["textHtml"].(string)
			f.messages[chat] = append(f.messages[chat], msg)
			return http.StatusCreated, map[string]any{"id": msg.ID}
		}
		// Новые первыми: адаптер обязан сортировать сам, а не полагаться на порядок сервера.
		var items []any
		msgs := f.messages[chat]
		for i := len(msgs) - 1; i >= 0; i-- {
			m := msgs[i]
			items = append(items, map[string]any{
				"id": m.ID, "fromUserId": m.From, "text": m.Text, "textHtml": m.HTML,
				"label": "", "editTimestamp": 0, "reactions": map[string]any{}, "deleted": m.Deleted,
			})
		}
		return http.StatusOK, f.page(items, q)
	}
	return http.StatusNotFound, map[string]any{"error": "no route " + method + " " + path}
}

func serve(t *testing.T, fake *fakeYouGile) string {
	t.Helper()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	return server.URL
}

func testConfig(baseURL string) Config {
	return Config{
		BaseURL: baseURL, APIKey: "test-key", ProjectID: testProject,
		ColumnIDs:    map[string]string{"Ready": colReady, "InProgress": colWork, "Review": colReview},
		CreateStatus: "Ready",
	}
}

func fixture(t *testing.T) (*Tracker, *fakeYouGile) {
	t.Helper()
	fake := newFake(t)
	tr, err := Open(testConfig(serve(t, fake)))
	if err != nil {
		t.Fatalf("трекер не открыт: %v", err)
	}
	tr.Now = func() time.Time { return now }
	return tr, fake
}

func TestOpenRejectsIncompleteConfig(t *testing.T) {
	good := testConfig("https://yougile.example")
	cases := map[string]func(*Config){
		"нет base_url":          func(c *Config) { c.BaseURL = "" },
		"base_url без схемы":    func(c *Config) { c.BaseURL = "yougile.com" },
		"нет ключа API":         func(c *Config) { c.APIKey = "" },
		"нет проекта":           func(c *Config) { c.ProjectID = "" },
		"пустая карта статусов": func(c *Config) { c.ColumnIDs = nil },
	}
	for name, spoil := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := good
			spoil(&cfg)
			if _, err := Open(cfg); err == nil {
				t.Fatal("Open принял неполную конфигурацию")
			}
		})
	}
}

func TestCallSendsBearerKey(t *testing.T) {
	tr, fake := fixture(t)
	var out struct {
		ID string `json:"id"`
	}
	if err := tr.call(http.MethodGet, "/projects/"+testProject, nil, nil, &out); err != nil {
		t.Fatalf("call: %v", err)
	}
	if out.ID != testProject {
		t.Errorf("ответ не разобран: %+v", out)
	}
	if fake.lastAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q", fake.lastAuth)
	}
}

func TestCallMapsNotFound(t *testing.T) {
	tr, _ := fixture(t)
	err := tr.call(http.MethodGet, "/tasks/missing", nil, nil, nil)
	if !errors.Is(err, tracker.ErrNotFound) {
		t.Errorf("404 дал %v, ожидался ErrNotFound", err)
	}
}

func TestCallErrorNeverLeaksAPIKey(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			tr, fake := fixture(t)
			fake.fail["GET /api-v2/users/me"] = code
			err := tr.call(http.MethodGet, "/users/me", nil, nil, nil)
			if err == nil {
				t.Fatal("ошибочный ответ прошёл как успех")
			}
			if strings.Contains(err.Error(), "test-key") {
				t.Errorf("ключ API в тексте ошибки: %v", err)
			}
			if !strings.Contains(err.Error(), strconv.Itoa(code)) {
				t.Errorf("в ошибке нет кода %d: %v", code, err)
			}
		})
	}
}

func TestListAllFollowsNextPastServerCap(t *testing.T) {
	tr, fake := fixture(t)
	fake.pageCap = 2
	for i := range 5 {
		fake.addTask(&fakeTask{ID: fmt.Sprintf("extra-%d", i), ColumnID: colReady, Timestamp: now.UnixMilli() + int64(i)})
	}
	got, err := listAll[struct {
		ID string `json:"id"`
	}](tr, "/task-list", url.Values{"columnId": {colReady}})
	if err != nil {
		t.Fatalf("listAll: %v", err)
	}
	if len(got) != 6 {
		t.Errorf("получено %d задач, ожидалось 6 (страницы по 2)", len(got))
	}
}

func TestListAllGivesUpOnEndlessPaging(t *testing.T) {
	tr, fake := fixture(t)
	fake.endlessPaging = true
	_, err := listAll[struct {
		ID string `json:"id"`
	}](tr, "/task-list", url.Values{"columnId": {colReady}})
	if err == nil || !strings.Contains(err.Error(), strconv.Itoa(maxPages)) {
		t.Errorf("бесконечная пагинация дала %v, ожидался отказ с упоминанием %d страниц", err, maxPages)
	}
}

func TestCheckProjectRejectsForeignProject(t *testing.T) {
	tr, _ := fixture(t)
	if err := tr.checkProject("OTHER"); !errors.Is(err, tracker.ErrNoProject) {
		t.Errorf("чужой проект дал %v, ожидался ErrNoProject", err)
	}
	if err := tr.checkProject(testProject); err != nil {
		t.Errorf("свой проект отвергнут: %v", err)
	}
}
