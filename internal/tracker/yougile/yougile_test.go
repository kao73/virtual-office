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
	"unicode/utf8"

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

	ignoreColumnFilter bool // /task-list отдаёт задачи всех колонок, как бы ни просили
	ignoreColumnOnPut  bool // PUT /tasks/{id} применяет apiData, а columnId молча пропускает
	createWithoutID    bool // POST /tasks отвечает 201 без id

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
	f.setLeaseOf(id, "someone", runID, until)
}

// setLeaseOf — то же, с владельцем: прогон той же роли пишет то же имя роли.
func (f *fakeYouGile) setLeaseOf(id, owner, runID string, until time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	task := f.tasks[id]
	if task.APIData == nil {
		task.APIData = map[string]any{}
	}
	ns, _ := task.APIData[keyNamespace].(map[string]any)
	if ns == nil {
		ns = map[string]any{}
		task.APIData[keyNamespace] = ns
	}
	ns["lease"] = map[string]any{"owner": owner, "run_id": runID, "lease_until": until.Format(time.RFC3339Nano)}
}

// officeAPIData — apiData, в котором данные офиса лежат в своём пространстве имён.
func officeAPIData(fields map[string]any) map[string]any {
	return map[string]any{keyNamespace: fields}
}

// officeOf — данные офиса в apiData задачи фейка; nil, если их там нет.
func (f *fakeYouGile) officeOf(id string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	ns, _ := f.tasks[id].APIData[keyNamespace].(map[string]any)
	return ns
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
		// Не прячем deleted (как и archived чуть выше): реальный YouGile их
		// фильтровать не обязан, отсев — забота адаптера (status.go).
		var items []any
		for _, id := range f.order {
			task := f.tasks[id]
			if col := q.Get("columnId"); col != "" && task.ColumnID != col && !f.ignoreColumnFilter {
				continue
			}
			items = append(items, task.dto())
		}
		return http.StatusOK, f.page(items, q)

	case method == http.MethodGet && strings.HasPrefix(path, "/tasks/"):
		// Удаление в YouGile мягкое: задача с deleted=true по id читается.
		task, ok := f.tasks[strings.TrimPrefix(path, "/tasks/")]
		if !ok {
			return http.StatusNotFound, notFound
		}
		return http.StatusOK, task.dto()

	case method == http.MethodPost && path == "/tasks":
		body := decode()
		f.posts = append(f.posts, body)
		if f.createWithoutID {
			return http.StatusCreated, map[string]any{}
		}
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
		if v, ok := body["columnId"].(string); ok && !f.ignoreColumnOnPut {
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

func TestWhoamiReturnsEmail(t *testing.T) {
	tr, _ := fixture(t)
	who, err := tr.Whoami()
	if err != nil || who != "office@example.com" {
		t.Errorf("Whoami = %q, %v", who, err)
	}
}

// Пустой email — не «никто», а сломанный ответ: сравнивать авторов было бы
// не с чем, и каждый комментарий офиса сошёл бы за слова человека.
func TestWhoamiRejectsEmptyEmail(t *testing.T) {
	tr, fake := fixture(t)
	fake.users[officeUserID] = ""
	if _, err := tr.Whoami(); err == nil {
		t.Error("пустой email принят")
	}
}

func openWith(t *testing.T, fake *fakeYouGile, spoil func(*Config)) (*Tracker, error) {
	t.Helper()
	cfg := testConfig(serve(t, fake))
	spoil(&cfg)
	return Open(cfg)
}

func TestOpenRejectsAmbiguousColumnMapping(t *testing.T) {
	_, err := openWith(t, newFake(t), func(c *Config) {
		c.ColumnIDs = map[string]string{"Ready": colReady, "Backlog": colReady}
	})
	if err == nil {
		t.Fatal("одна колонка на два статуса принята")
	}
	for _, want := range []string{colReady, "Ready", "Backlog"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в ошибке нет %q: %v", want, err)
		}
	}
}

func TestOpenRejectsEmptyColumnID(t *testing.T) {
	_, err := openWith(t, newFake(t), func(c *Config) { c.ColumnIDs["Ready"] = "" })
	if err == nil || !strings.Contains(err.Error(), "Ready") {
		t.Errorf("пустой id колонки дал %v", err)
	}
}

func TestOpenRejectsMissingColumn(t *testing.T) {
	_, err := openWith(t, newFake(t), func(c *Config) { c.ColumnIDs["Ready"] = "col-missing" })
	if err == nil || !strings.Contains(err.Error(), "col-missing") {
		t.Errorf("несуществующая колонка дала %v", err)
	}
}

// Колонка есть в компании, но на доске чужого проекта: для этого трекера её нет.
func TestOpenRejectsColumnOfAnotherProject(t *testing.T) {
	_, err := openWith(t, newFake(t), func(c *Config) { c.ColumnIDs["Ready"] = "col-foreign" })
	if err == nil || !strings.Contains(err.Error(), "col-foreign") {
		t.Errorf("колонка чужого проекта дала %v", err)
	}
}

func TestOpenRejectsUnknownProject(t *testing.T) {
	_, err := openWith(t, newFake(t), func(c *Config) { c.ProjectID = "nope" })
	if !errors.Is(err, tracker.ErrNoProject) {
		t.Errorf("незнакомый проект дал %v, ожидался ErrNoProject", err)
	}
}

func TestOpenRejectsCreateStatusOutsideMap(t *testing.T) {
	_, err := openWith(t, newFake(t), func(c *Config) { c.CreateStatus = "Backlog" })
	if err == nil || !strings.Contains(err.Error(), "Backlog") {
		t.Errorf("create_status вне карты дал %v", err)
	}
}

func TestOpenAllowsEmptyCreateStatus(t *testing.T) {
	if _, err := openWith(t, newFake(t), func(c *Config) { c.CreateStatus = "" }); err != nil {
		t.Errorf("пустой create_status отвергнут: %v", err)
	}
}

func TestOpenCachesProjectColumnsAndCreatesNothing(t *testing.T) {
	tr, fake := fixture(t)
	if len(tr.columns) != 4 {
		t.Errorf("кэш колонок: %+v, ожидались 4 колонки board-1", tr.columns)
	}
	for _, id := range tr.columns {
		if id == "col-foreign" {
			t.Errorf("в кэш попала колонка чужого проекта")
		}
	}
	if tr.columnStatus[colWork] != "InProgress" {
		t.Errorf("обратная карта: %+v", tr.columnStatus)
	}
	if n := fake.count("POST") + fake.count("PUT"); n != 0 {
		t.Errorf("Open что-то записал: %d запросов записи", n)
	}
}

// Пустой ответ 2xx там, где ждали тело, — не «пустой список»: иначе ListExpired
// молча не видит истёкших аренд, а FindByMarker — детей, и ensureChildren
// заводит дубли.
func TestCallRejectsEmptyBodyWhenAnswerExpected(t *testing.T) {
	tr, _ := fixture(t)
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(empty.Close)
	tr.cfg.BaseURL = empty.URL
	if refs, err := tr.ListReady(testProject, "Ready"); err == nil {
		t.Errorf("пустой ответ листинга принят как %v", refs)
	}
	if err := tr.putTask(testKey, map[string]any{"columnId": colWork}); err != nil {
		t.Errorf("пустой ответ на запись, где тело не нужно: %v", err)
	}
}

// Тело оборвалось на чтении — запрос не удался, даже если код 2xx и тело
// вызывающему не нужно: что сервер успел сделать, не известно.
func TestCallReportsBrokenBody(t *testing.T) {
	tr, _ := fixture(t)
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(broken.Close)
	tr.cfg.BaseURL = broken.URL
	if err := tr.putTask(testKey, map[string]any{"columnId": colWork}); err == nil {
		t.Error("оборванное тело ответа не дало ошибки")
	}
}

// base_url с /api-v2 на конце — частая опечатка: запросы ушли бы на
// /api-v2/api-v2/…, и 404 выдал бы себя за «нет такого проекта».
func TestOpenRejectsBaseURLWithAPIPrefix(t *testing.T) {
	fake := newFake(t)
	root := serve(t, fake)
	for _, base := range []string{root + "/api-v2", root + "/api-v2/"} {
		_, err := Open(testConfig(base))
		if err == nil || errors.Is(err, tracker.ErrNoProject) || !strings.Contains(err.Error(), "base_url") {
			t.Errorf("%s: %v, ожидалась ошибка base_url", base, err)
		}
	}
}

// Колонку из карты удалили в интерфейсе — Open обязан упасть, а не открыться
// с очередью, которая тихо пустует.
func TestOpenRejectsDeletedColumn(t *testing.T) {
	fake := newFake(t)
	fake.columns[0]["deleted"] = true // colReady
	_, err := openWith(t, fake, func(*Config) {})
	if err == nil || !strings.Contains(err.Error(), colReady) {
		t.Errorf("удалённая колонка дала %v", err)
	}
}

func TestOpenRejectsColumnOfDeletedBoard(t *testing.T) {
	fake := newFake(t)
	fake.boards[0]["deleted"] = true // board-1
	_, err := openWith(t, fake, func(*Config) {})
	if err == nil || !strings.Contains(err.Error(), colReady) {
		t.Errorf("колонки удалённой доски дали %v", err)
	}
}

// Тело ответа режется по символам: русский текст ошибки, оборванный посреди
// руны, уехал бы в лог битым UTF-8.
func TestSnippetCutsByRunes(t *testing.T) {
	// Битые байты (чужая кодировка прокси, оборванное тело) — U+FFFD, а не
	// насквозь в текст ошибки.
	for body, want := range map[string]string{"ab\xffcd": "ab\uFFFDcd", " \xd0": "\uFFFD"} {
		if got := snippet([]byte(body)); got != want {
			t.Errorf("snippet(%q) = %q, ожидалось %q", body, got, want)
		}
	}
	// 2-байтовые и 4-байтовые руны, короче и длиннее лимита в байтах.
	for _, r := range []string{"я", "🙂"} {
		for _, n := range []int{500, 5000} {
			got := snippet([]byte("  " + strings.Repeat(r, n)))
			if !utf8.ValidString(got) {
				t.Errorf("%s×%d: битый UTF-8", r, n)
			}
			if want := strings.Repeat(r, 400) + "…"; got != want {
				t.Errorf("%s×%d: обрезано до %d рун, ожидалось 400", r, n, utf8.RuneCountInString(got)-1)
			}
		}
	}
}

// Тело ответа не копируется: наружу уходит 400 символов, и память под них,
// а не под мегабайтную страницу прокси.
func TestSnippetDoesNotCopyBody(t *testing.T) {
	body := []byte(strings.Repeat("x", 1<<20))
	res := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			_ = snippet(body)
		}
	})
	if got := res.AllocedBytesPerOp(); got > 16<<10 {
		t.Errorf("snippet выделяет %d байт на мегабайтное тело", got)
	}
}
