package jira

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kao73/virtual-office/internal/tracker"
)

var now = time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

// fakeJira — минимальный JIRA: столько, сколько трогает трекер. Он не изображает
// сервер вообще, а отвечает на конкретные запросы и запоминает, что ему прислали,
// — чтобы проверять не только исход, но и форму запроса.
type fakeJira struct {
	t *testing.T

	// hitPaths — путь каждого дошедшего запроса, по порядку. Нужен там, где
	// проверяется, что заслон отказал ДО отправки запроса, а не полагается
	// на то, что сервер сам ответит 404 незнакомому пути.
	hitPaths []string

	status           string
	runID            string
	owner            string
	leaseUntil       string
	attempts         float64
	labels           []string
	comments         []map[string]any
	issueAttachments []map[string]any
	// remoteIssuelinks — то, что "issuelinks" отдаёт GET/поиск задачи в
	// этом тесте: подделывает то, что реально хранит сервер, в отличие
	// от issueLinks (без круглой буквы l после "issue") выше, которое
	// ловит исходящие POST /issueLink этого же трекера.
	remoteIssuelinks []any

	// remoteFields — то, что отдаёт GET /field: список полей живого инстанса.
	// nil — сервер отвечает пустым списком, то есть ни один customfield_* не
	// существует.
	remoteFields []fakeField

	// remoteLinkTypes — имена, которые отдаёт GET /issueLinkType. nil — пустой
	// список: искомый тип связи не существует.
	remoteLinkTypes []string

	// verifyRunID подменяет run_id при перечитывании после захвата: так выглядит
	// проигранная гонка, ради которой сверка и делается.
	verifyRunID string
	claimed     bool

	// transitionsTo — статусы, в которые задаче доступен переход. По умолчанию
	// все четыре: так настроен полигон, где вход в каждый статус глобальный.
	transitionsTo []string
	noIssues      bool // поиск ничего не находит

	lastUpdate map[string]any // fields последнего PUT
	lastJQL    string
	// lastSearchFields — "fields" последнего тела POST /search: то, что
	// на самом деле запрашивает searchFields(), не то, что фейковый
	// сервер решает вернуть (он всегда отдаёт issue() целиком — см.
	// TestSearchRequestsIssuelinksField ниже, которая проверяет именно
	// запрос).
	lastSearchFields []any
	lastLimit        int      // maxResults последнего поиска
	lastStartAt      int      // startAt последнего поиска
	transitons       []string // имена статусов, в которые переводили
	commentPages     int      // сколько раз спрашивали страницу комментариев
	fakeTotal        int      // ненулевой — сервер врёт про размер переписки

	// searchIssues — если задано, /search отдаёт постранично ИМЕННО этот
	// список (по startAt/maxResults из тела запроса), а не единственный
	// f.issue(). Нужен для проверки пагинации List() (fix round 1,
	// Finding 1): по умолчанию (nil) сервер ведёт себя как раньше — одна
	// страница с единственной VO-1.
	searchIssues []map[string]any
	searchPages  int // сколько раз запрашивали страницу поиска (searchIssues != nil)
	// searchServerCap — если не 0, сервер режет страницу по этому размеру,
	// что бы клиент ни просил в maxResults, и честно эхает применённый
	// размер обратно в "maxResults" ответа — так выглядит инстанс со
	// своим потолком поиска (jira.search.views.default.max) ниже
	// searchPage. Нужен для fix round 2, Finding 8: пагинация не должна
	// принимать «страница короче ЗАПРОШЕННОГО» за «это была последняя
	// страница», если сервер сам никогда не отдаёт больше своего потолка.
	searchServerCap int

	// searchNeverEnds — если задано, /search всегда отдаёт ровно
	// lastLimit свежесгенерированных задач и врёт про total (всегда 0),
	// независимо от startAt — так выглядит сервер, который никогда не
	// подтверждает конец списка. Нужен для TestSearchAllProjectStopsAfterPageCap
	// (pr-converge round 2, Finding 5): без потолка страниц searchAllProject
	// крутил бы этот цикл вечно.
	searchNeverEnds bool

	lastUser string // учётка последнего запроса: под кем ходил трекер

	// badSearch заставляет поиск падать, а knownProject — единственный проект,
	// который сервер признаёт своим. Вместе они изображают заглушку
	// в projects.local.yaml: JQL по несуществующему проекту JIRA отвергает.
	badSearch    bool
	knownProject string

	// nextKey — ключ, который вернёт POST /issue. Пусто — по умолчанию VO-2.
	nextKey string
	created []fakeIssue

	// baseURL — адрес тестового сервера. Нужен, чтобы отдавать в метаданных
	// вложения абсолютную ссылку content, как это делает настоящая JIRA.
	baseURL     string
	attachments []fakeAttachment

	// contentHost — если задан, метаданные вложения называют content по этому
	// адресу вместо baseURL: так подделывается сервер, отдающий ссылку на чужой
	// хост (S3 и подобное) вместо себя самого.
	contentHost string

	// issueLinks — тела POST /issueLink, принятые сервером, в порядке прихода.
	issueLinks []map[string]any
}

// fakeIssue — задача, заведённая через POST /issue в этом тесте.
type fakeIssue struct {
	key    string
	fields map[string]any
}

// fakeField — одна запись ответа GET /field.
type fakeField struct {
	id     string
	schema string // schema.type; пусто — как у части системных полей без схемы
}

// fakeAttachment — вложение, принятое через POST /issue/{key}/attachments.
type fakeAttachment struct {
	id, name string
	data     []byte
}

// number — целое из строки запроса, с запасным значением на пустоту и мусор.
func number(raw string, fallback int) int {
	if n, err := strconv.Atoi(raw); err == nil {
		return n
	}
	return fallback
}

func (f *fakeJira) issue() map[string]any {
	fields := map[string]any{
		"summary":           "Добавить hello.py",
		"description":       "Создай файл и закоммить.",
		"status":            map[string]any{"name": f.status},
		"project":           map[string]any{"key": "VO"},
		"labels":            f.labels,
		"customfield_10001": f.owner,
		"customfield_10002": f.runID,
		"customfield_10003": f.leaseUntil,
		"customfield_10004": f.attempts,
		"updated":           "2026-08-17T12:00:00.000+0000",
	}
	if f.issueAttachments != nil {
		list := make([]any, len(f.issueAttachments))
		for i, a := range f.issueAttachments {
			list[i] = a
		}
		fields["attachment"] = list
	}
	if f.remoteIssuelinks != nil {
		fields["issuelinks"] = f.remoteIssuelinks
	}
	return map[string]any{"key": "VO-1", "fields": fields}
}

// pageSize — размер страницы поиска, который сервер применит: то, что
// запросил клиент (lastLimit), а без этого — 50, как отдаёт настоящий
// searchPage по умолчанию. Общая для обеих постраничных веток /search
// (pr-converge cleanup pass, simplification finding 4).
func (f *fakeJira) pageSize() int {
	if f.lastLimit <= 0 {
		return 50
	}
	return f.lastLimit
}

func (f *fakeJira) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.hitPaths = append(f.hitPaths, r.URL.Path)
	f.lastUser, _, _ = r.BasicAuth()
	body := map[string]any{}
	if r.Body != nil {
		raw, _ := io.ReadAll(r.Body)
		// Тело возвращается на место: вложение читает его заново как multipart
		// (r.ParseMultipartForm), а не как JSON, и ниже ему нужен тот же поток
		// байт, а не уже осушенный io.ReadAll выше.
		r.Body = io.NopCloser(bytes.NewReader(raw))
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &body)
		}
	}
	write := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}

	switch {
	case r.URL.Path == "/rest/api/2/myself":
		write(map[string]any{"name": "office", "displayName": "Офис"})

	case r.URL.Path == "/rest/api/2/field" && r.Method == http.MethodGet:
		list := make([]any, 0, len(f.remoteFields))
		for _, rf := range f.remoteFields {
			list = append(list, map[string]any{"id": rf.id, "schema": map[string]any{"type": rf.schema}})
		}
		write(list)

	case r.URL.Path == "/rest/api/2/issueLinkType" && r.Method == http.MethodGet:
		types := make([]any, 0, len(f.remoteLinkTypes))
		for _, name := range f.remoteLinkTypes {
			types = append(types, map[string]any{"name": name})
		}
		write(map[string]any{"issueLinkTypes": types})

	case r.URL.Path == "/rest/api/2/search":
		f.lastJQL, _ = body["jql"].(string)
		f.lastSearchFields, _ = body["fields"].([]any)
		if limit, ok := body["maxResults"].(float64); ok {
			f.lastLimit = int(limit)
		}
		if startAt, ok := body["startAt"].(float64); ok {
			f.lastStartAt = int(startAt)
		} else {
			f.lastStartAt = 0
		}
		if f.badSearch {
			w.WriteHeader(http.StatusBadRequest)
			write(map[string]any{"errorMessages": []string{"поиск не удался"}})
			return
		}
		if f.noIssues {
			write(map[string]any{"issues": []any{}})
			return
		}
		if f.searchNeverEnds {
			f.searchPages++
			size := f.pageSize()
			page := make([]any, size)
			for i := range page {
				page[i] = fakeSearchIssue(fmt.Sprintf("VO-INF-%d-%d", f.lastStartAt, i))
			}
			write(map[string]any{"issues": page, "total": 0, "maxResults": size})
			return
		}
		if f.searchIssues != nil {
			// Страницы отдаются честно, тем же приёмом, что и переписка
			// (см. комментарии выше по startAt/maxResults): без этого
			// тест на пагинацию List() проходил бы и без пагинации.
			f.searchPages++
			size := f.pageSize()
			if f.searchServerCap > 0 && f.searchServerCap < size {
				// Свой потолок инстанса — ниже того, что просил клиент.
				// Честно эхаем применённый размер в "maxResults": ровно
				// то поле, на которое обязана опираться пагинация
				// клиента, раз "короче запрошенного" сервер отдаёт на
				// каждой странице, а не только на последней.
				size = f.searchServerCap
			}
			start := f.lastStartAt
			var page []any
			if start < len(f.searchIssues) {
				end := min(start+size, len(f.searchIssues))
				for _, iss := range f.searchIssues[start:end] {
					page = append(page, iss)
				}
			}
			write(map[string]any{"issues": page, "total": len(f.searchIssues), "maxResults": size, "startAt": start})
			return
		}
		write(map[string]any{"issues": []any{f.issue()}})

	case strings.HasPrefix(r.URL.Path, "/rest/api/2/project/"):
		if key := strings.TrimPrefix(r.URL.Path, "/rest/api/2/project/"); key == f.knownProject {
			write(map[string]any{"key": key, "name": "Virtual Office"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
		write(map[string]any{"errorMessages": []string{"No project could be found with key"}})

	case r.URL.Path == "/rest/api/2/issue" && r.Method == http.MethodPost:
		fields, _ := body["fields"].(map[string]any)
		key := f.nextKey
		if key == "" {
			key = "VO-2"
		}
		f.created = append(f.created, fakeIssue{key: key, fields: fields})
		w.WriteHeader(http.StatusCreated)
		write(map[string]any{"id": "10100", "key": key})

	case r.URL.Path == "/rest/api/2/issueLink" && r.Method == http.MethodPost:
		f.issueLinks = append(f.issueLinks, body)
		w.WriteHeader(http.StatusCreated)

	case r.URL.Path == "/rest/api/2/issue/VO-1" && r.Method == http.MethodGet:
		issue := f.issue()
		if f.claimed && f.verifyRunID != "" {
			issue["fields"].(map[string]any)["customfield_10002"] = f.verifyRunID
		}
		write(issue)

	case r.URL.Path == "/rest/api/2/issue/VO-1" && r.Method == http.MethodPut:
		if fields, ok := body["fields"].(map[string]any); ok {
			f.lastUpdate = fields
			f.apply(fields)
		}
		w.WriteHeader(http.StatusNoContent)

	case r.URL.Path == "/rest/api/2/issue/VO-1/transitions" && r.Method == http.MethodGet:
		list := make([]any, 0, len(f.transitionsTo))
		for i, name := range f.transitionsTo {
			list = append(list, map[string]any{
				"id": strconv.Itoa(11 + i*10), "to": map[string]any{"name": name},
			})
		}
		write(map[string]any{"transitions": list})

	case r.URL.Path == "/rest/api/2/issue/VO-1/transitions" && r.Method == http.MethodPost:
		id, _ := body["transition"].(map[string]any)["id"].(string)
		index := (number(id, -11) - 11) / 10
		if index < 0 || index >= len(f.transitionsTo) {
			f.t.Errorf("неизвестный переход %q", id)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.status = f.transitionsTo[index]
		f.transitons = append(f.transitons, f.status)
		f.claimed = true
		w.WriteHeader(http.StatusNoContent)

	// GET на переписку общий для VO-1 и задач, заведённых в этом тесте через
	// POST /issue (CreateTask сам перечитывает созданную задачу через Get,
	// а тот всегда тянет комментарии): у VO-1 своя история в f.comments,
	// у только что созданных задач комментариев ещё не бывает.
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/rest/api/2/issue/") &&
		strings.HasSuffix(r.URL.Path, "/comment"):
		key := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/rest/api/2/issue/"), "/comment")
		if key != "VO-1" {
			write(map[string]any{"comments": []map[string]any{}, "total": 0, "startAt": 0, "maxResults": 0})
			return
		}
		// Страницы отдаются честно: сервер режет выдачу по startAt и maxResults,
		// а полный размер сообщает в total. Отдавай подделка всё разом — тест
		// на пагинацию проходил бы и без пагинации.
		start := number(r.URL.Query().Get("startAt"), 0)
		size := number(r.URL.Query().Get("maxResults"), 50)
		f.commentPages++

		page := []map[string]any{}
		if start < len(f.comments) {
			page = f.comments[start:min(start+size, len(f.comments))]
		}
		total := len(f.comments)
		if f.fakeTotal != 0 {
			total = f.fakeTotal
		}
		write(map[string]any{
			"comments": page, "total": total,
			"startAt": start, "maxResults": size,
		})

	case r.URL.Path == "/rest/api/2/issue/VO-1/comment" && r.Method == http.MethodPost:
		f.comments = append(f.comments, map[string]any{
			"id": "1", "body": body["body"],
			"author":  map[string]any{"name": "office"},
			"created": "2026-08-17T12:00:00.000+0000",
		})
		w.WriteHeader(http.StatusCreated)
		write(map[string]any{"id": "1"})

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
		host := f.baseURL
		if f.contentHost != "" {
			host = f.contentHost
		}
		for _, a := range f.attachments {
			if a.id == id {
				write(map[string]any{"id": id, "content": host + "/secure/attachment/" + id})
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

	case strings.HasPrefix(r.URL.Path, "/rest/api/2/issue/"):
		w.WriteHeader(http.StatusNotFound)
		write(map[string]any{"errorMessages": []string{"Issue Does Not Exist"}})

	default:
		f.t.Errorf("неожиданный запрос %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func (f *fakeJira) apply(fields map[string]any) {
	if v, ok := fields["customfield_10001"]; ok {
		f.owner, _ = v.(string)
	}
	if v, ok := fields["customfield_10002"]; ok {
		f.runID, _ = v.(string)
	}
	if v, ok := fields["customfield_10003"]; ok {
		f.leaseUntil, _ = v.(string)
	}
	if v, ok := fields["customfield_10004"]; ok {
		f.attempts, _ = v.(float64)
	}
	if v, ok := fields["labels"]; ok {
		f.labels = nil
		for _, label := range v.([]any) {
			f.labels = append(f.labels, label.(string))
		}
	}
}

func fixture(t *testing.T) (*Tracker, *fakeJira) {
	t.Helper()
	fake := &fakeJira{
		t: t, status: "Ready",
		transitionsTo: []string{"In Progress", "Review", "Blocked", "Ready"},
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	fake.baseURL = server.URL

	t.Setenv("JIRA_USER", "office")
	t.Setenv("JIRA_PASSWORD", "секрет")

	tr, err := Open(Config{
		BaseURL:   server.URL,
		Auth:      Auth{Mode: "basic"},
		Accounts:  Accounts{Default: Account{UserEnv: "JIRA_USER", SecretEnv: "JIRA_PASSWORD"}},
		StatusMap: map[string]string{"Ready": "Ready", "InProgress": "In Progress", "Review": "Review", "Blocked": "Blocked"},
		Fields: Fields{
			Owner: "customfield_10001", RunID: "customfield_10002",
			LeaseUntil: "customfield_10003", Attempts: "customfield_10004",
		},
		HumanFlagLabel: "office-waits-human",
		IssueType:      "Task",
		DependsOnLink:  "Depends",
	})
	if err != nil {
		t.Fatalf("трекер не открыт: %v", err)
	}
	tr.Now = func() time.Time { return now }
	return tr, fake
}

// fixtureWithoutDependsOnLink — тот же трекер, что и fixture(), но с
// depends_on_link не заданным: валидная, поддерживаемая конфигурация
// (LoadConfig не требует поля, LinkDependsOn откажет сам при вызове —
// см. его доккомент), нужная TestGetSkipsIssuelinkWithoutConfiguredType
// (pr-converge round 2, Finding 1).
func fixtureWithoutDependsOnLink(t *testing.T) (*Tracker, *fakeJira) {
	t.Helper()
	fake := &fakeJira{
		t: t, status: "Ready",
		transitionsTo: []string{"In Progress", "Review", "Blocked", "Ready"},
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	fake.baseURL = server.URL

	t.Setenv("JIRA_USER", "office")
	t.Setenv("JIRA_PASSWORD", "секрет")

	tr, err := Open(Config{
		BaseURL:   server.URL,
		Auth:      Auth{Mode: "basic"},
		Accounts:  Accounts{Default: Account{UserEnv: "JIRA_USER", SecretEnv: "JIRA_PASSWORD"}},
		StatusMap: map[string]string{"Ready": "Ready", "InProgress": "In Progress", "Review": "Review", "Blocked": "Blocked"},
		Fields: Fields{
			Owner: "customfield_10001", RunID: "customfield_10002",
			LeaseUntil: "customfield_10003", Attempts: "customfield_10004",
		},
		HumanFlagLabel: "office-waits-human",
		IssueType:      "Task",
	})
	if err != nil {
		t.Fatalf("трекер не открыт: %v", err)
	}
	tr.Now = func() time.Time { return now }
	return tr, fake
}

func TestWhoami(t *testing.T) {
	tr, _ := fixture(t)
	who, err := tr.Whoami()
	if err != nil || who != "office" {
		t.Errorf("Whoami = %q, %v", who, err)
	}
}

// Имена статусов на инстансе переводятся в статусы графа и обратно: раннер работает
// с именами из workflow.yaml и про «In Progress» с пробелом знать не должен.
func TestGetMapsStatusToColumn(t *testing.T) {
	tr, fake := fixture(t)
	fake.status = "In Progress"
	fake.runID = "прогон-1"
	fake.owner = "implementer"
	fake.leaseUntil = "2026-08-17T12:30:00.000+0000"
	fake.attempts = 2
	fake.labels = []string{"office-waits-human", "demo"}

	task, err := tr.Get("VO-1")
	if err != nil {
		t.Fatalf("задача не прочитана: %v", err)
	}
	if task.Status != "InProgress" {
		t.Errorf("статус %q, ожидался статус графа InProgress", task.Status)
	}
	if task.RunID != "прогон-1" || task.Owner != "implementer" {
		t.Errorf("аренда прочитана как %q/%q", task.Owner, task.RunID)
	}
	if want := now.Add(30 * time.Minute); !task.LeaseUntil.Equal(want) {
		t.Errorf("срок аренды %s, ожидался %s", task.LeaseUntil, want)
	}
	if !task.LeaseAlive(now) {
		t.Error("живая аренда сочтена истёкшей")
	}
	if task.Attempts != 2 {
		t.Errorf("попыток %d, ожидалось 2", task.Attempts)
	}
	if !task.HumanFlag {
		t.Error("метка ожидания человека не распознана")
	}
	if task.Project != "VO" || task.Summary == "" {
		t.Errorf("поля задачи: %+v", task)
	}
}

// TestGetMapsAttachments доказывает, что toTask больше не выбрасывает
// fields["attachment"] молча — без этого поля агент никогда не узнал бы,
// что к тикету что-то приложено (правка "видимость вложений").
func TestGetMapsAttachments(t *testing.T) {
	tr, fake := fixture(t)
	fake.issueAttachments = []map[string]any{
		{"id": "10004", "filename": "schema.png"},
		{"id": "10005", "filename": "spec.pdf"},
	}

	task, err := tr.Get("VO-1")
	if err != nil {
		t.Fatalf("задача не прочитана: %v", err)
	}
	want := []tracker.AttachmentRef{{ID: "10004", Name: "schema.png"}, {ID: "10005", Name: "spec.pdf"}}
	if !slices.Equal(task.Attachments, want) {
		t.Errorf("вложения %+v, ожидались %+v", task.Attachments, want)
	}
}

// TestGetParsesDependsOnFromIssuelinks покрывает четыре формы записи
// issuelinks разом (tasks.md 1.4): связь с совпадающим типом, связь с
// чужим type.name (игнорируется), несколько связей сразу, запись только
// с inwardIssue (обратная сторона — не читается: DependsOn — это "от
// кого зависит эта задача", не "кто зависит от неё").
func TestGetParsesDependsOnFromIssuelinks(t *testing.T) {
	tr, fake := fixture(t) // fixture() уже задаёт DependsOnLink: "Depends"
	fake.remoteIssuelinks = []any{
		map[string]any{
			"type":         map[string]any{"name": "Depends"},
			"outwardIssue": map[string]any{"key": "VO-5"},
		},
		map[string]any{
			// чужой тип связи — не должен попасть в DependsOn
			"type":         map[string]any{"name": "Blocks"},
			"outwardIssue": map[string]any{"key": "VO-9"},
		},
		map[string]any{
			// обратная сторона — не читается
			"type":        map[string]any{"name": "Depends"},
			"inwardIssue": map[string]any{"key": "VO-3"},
		},
		map[string]any{
			"type":         map[string]any{"name": "Depends"},
			"outwardIssue": map[string]any{"key": "VO-6"},
		},
	}

	task, err := tr.Get("VO-1")
	if err != nil {
		t.Fatalf("задача не прочитана: %v", err)
	}
	want := []string{"VO-5", "VO-6"}
	if !slices.Equal(task.DependsOn, want) {
		t.Errorf("DependsOn = %v, ожидалось %v", task.DependsOn, want)
	}
}

// TestGetSkipsIssuelinkWithMissingOutwardKey доказывает, что запись
// issuelinks с совпадающим типом, но без строкового "key" у outwardIssue
// (JIRA отдаёт такую урезанную заглушку вместо полной связанной задачи,
// когда у учётки нет прав видеть её — permission-restricted issue), не
// превращается в пустую строку в DependsOn. Пустой ключ никогда не
// найдётся в byKey гейта и печатался бы как "не найдена в статусах
// графа ()" — тот же текст, что у настоящей пропавшей зависимости,
// хотя причина другая (сбой разбора, не отсутствие в графе) — pr-converge
// round 1, Finding 9.
func TestGetSkipsIssuelinkWithMissingOutwardKey(t *testing.T) {
	tr, fake := fixture(t)
	fake.remoteIssuelinks = []any{
		map[string]any{
			"type":         map[string]any{"name": "Depends"},
			"outwardIssue": map[string]any{}, // "key" отсутствует
		},
		map[string]any{
			"type":         map[string]any{"name": "Depends"},
			"outwardIssue": map[string]any{"key": "VO-6"},
		},
	}

	task, err := tr.Get("VO-1")
	if err != nil {
		t.Fatalf("задача не прочитана: %v", err)
	}
	want := []string{"VO-6"}
	if !slices.Equal(task.DependsOn, want) {
		t.Errorf("DependsOn = %v, ожидалось %v (пустой ключ не должен попасть в список)", task.DependsOn, want)
	}
}

// TestGetSkipsIssuelinkWithoutConfiguredType доказывает pr-converge round 2,
// Finding 1: на инстансе без настроенного depends_on_link (валидная
// конфигурация — LinkDependsOn откажет сам при вызове, LoadConfig поле не
// требует) запись issuelinks без объекта "type" не должна становиться
// зависимостью. text(typ["name"]) на нулевой мапе — пустая строка, и до
// фикса она совпадала с пустым t.cfg.DependsOnLink, так что запись проходила
// фильтр типа связи, а её outwardIssue.key утекал в DependsOn — зеркально
// находке про пустой outwardIssue.key (TestGetSkipsIssuelinkWithMissingOutwardKey),
// только дыра на соседней стороне условия.
func TestGetSkipsIssuelinkWithoutConfiguredType(t *testing.T) {
	tr, fake := fixtureWithoutDependsOnLink(t)
	fake.remoteIssuelinks = []any{
		map[string]any{
			// нет "type" вовсе — typ станет нулевой мапой
			"outwardIssue": map[string]any{"key": "VO-5"},
		},
	}

	task, err := tr.Get("VO-1")
	if err != nil {
		t.Fatalf("задача не прочитана: %v", err)
	}
	if len(task.DependsOn) != 0 {
		t.Errorf("DependsOn = %v, ожидался пустой список (depends_on_link не настроен)", task.DependsOn)
	}
}

// TestSearchRequestsIssuelinksField доказывает, что searchFields()
// просит issuelinks у сервера — без этого поля ListReady/List на живом
// JIRA отдавали бы пустой DependsOn у каждого кандидата даже при верном
// toTask (design doc §1).
func TestSearchRequestsIssuelinksField(t *testing.T) {
	tr, fake := fixture(t)
	if _, err := tr.ListReady("VO", "Ready"); err != nil {
		t.Fatalf("список не прочитан: %v", err)
	}

	found := false
	for _, raw := range fake.lastSearchFields {
		if s, _ := raw.(string); s == "issuelinks" {
			found = true
		}
	}
	if !found {
		t.Errorf("запрошенные поля поиска не включают issuelinks: %v", fake.lastSearchFields)
	}
}

// TestSearchOmitsIssuelinksFieldWithoutConfiguredType — зеркало
// TestSearchRequestsIssuelinksField: на инстансе без depends_on_link
// searchFields() не должен просить issuelinks вовсе — toTask их всё
// равно выбросит целиком (её доккомент), так что поле было бы лишним
// весом каждой страницы поиска без единого потребителя (pr-converge
// round 3, Finding 3).
func TestSearchOmitsIssuelinksFieldWithoutConfiguredType(t *testing.T) {
	tr, fake := fixtureWithoutDependsOnLink(t)
	if _, err := tr.ListReady("VO", "Ready"); err != nil {
		t.Fatalf("список не прочитан: %v", err)
	}

	for _, raw := range fake.lastSearchFields {
		if s, _ := raw.(string); s == "issuelinks" {
			t.Errorf("запрошенные поля поиска включают issuelinks без настроенного depends_on_link: %v", fake.lastSearchFields)
		}
	}
}

// TestFindByMarkerOmitsIssuelinksFieldEvenWhenConfigured доказывает, что
// issuelinks просят не любой поиск с настроенным depends_on_link, а
// только List/ListReady (searchAllProject) — их результат читает гейт
// зависимостей. FindByMarker (как и ListExpired, CheckWorkflow — общий
// путь через searchProject) DependsOn у своих задач никогда не смотрит,
// так что поле было бы лишним весом каждой страницы поиска без единого
// потребителя, даже на инстансе, где depends_on_link задан (pr-converge
// cleanup pass, efficiency finding 2 — отдельно от Finding 5/round 3
// Finding 3 выше, которые закрыли только случай ненастроенного
// depends_on_link).
func TestFindByMarkerOmitsIssuelinksFieldEvenWhenConfigured(t *testing.T) {
	tr, fake := fixture(t) // fixture() задаёт DependsOnLink: "Depends"
	fake.labels = []string{"split-child:VO-1:category-crud"}

	if _, err := tr.FindByMarker("VO", "split-child:VO-1:category-crud"); err != nil {
		t.Fatalf("поиск не удался: %v", err)
	}

	for _, raw := range fake.lastSearchFields {
		if s, _ := raw.(string); s == "issuelinks" {
			t.Errorf("FindByMarker просит issuelinks, хотя никогда не читает DependsOn: %v", fake.lastSearchFields)
		}
	}
}

// TestListCandidateCarriesDependsOnLikeGet доказывает, что кандидат из
// List() несёт ту же зависимость, что и Get() той же задачи — не только
// форма запроса верна, но и итоговое значение совпадает (tasks.md 1.4,
// последний пункт).
func TestListCandidateCarriesDependsOnLikeGet(t *testing.T) {
	tr, fake := fixture(t)
	fake.remoteIssuelinks = []any{
		map[string]any{"type": map[string]any{"name": "Depends"}, "outwardIssue": map[string]any{"key": "VO-5"}},
	}

	refs, err := tr.List("VO", []string{"Ready"})
	if err != nil {
		t.Fatalf("список не прочитан: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("кандидатов %d, ожидался 1", len(refs))
	}

	task, err := tr.Get("VO-1")
	if err != nil {
		t.Fatalf("задача не прочитана: %v", err)
	}
	if !slices.Equal(refs[0].DependsOn, task.DependsOn) {
		t.Errorf("List().DependsOn = %v, Get().DependsOn = %v — разошлись", refs[0].DependsOn, task.DependsOn)
	}
	if !slices.Equal(refs[0].DependsOn, []string{"VO-5"}) {
		t.Errorf("DependsOn = %v, ожидалось [VO-5]", refs[0].DependsOn)
	}
}

// TestGetDependsOnEmptyWithoutIssuelinksField — задача без issuelinks
// вовсе (обычный случай для большинства тикетов) не должна давать сбой
// разбора и не должна давать ложных зависимостей.
func TestGetDependsOnEmptyWithoutIssuelinksField(t *testing.T) {
	tr, _ := fixture(t)
	task, err := tr.Get("VO-1")
	if err != nil {
		t.Fatalf("задача не прочитана: %v", err)
	}
	if len(task.DependsOn) != 0 {
		t.Errorf("DependsOn = %v, ожидался пустой список", task.DependsOn)
	}
}

func TestGetUnknownIssue(t *testing.T) {
	tr, _ := fixture(t)
	if _, err := tr.Get("VO-404"); !errors.Is(err, tracker.ErrNotFound) {
		t.Errorf("отсутствующая задача дала %v, ожидалось ErrNotFound", err)
	}
}

// Захват: запись полей, перевод в рабочий статус и обязательная сверка после.
// Атомарности в JIRA нет, поэтому сверка — единственное, что отличает захват
// от надежды на него.
func TestClaimWritesFieldsAndVerifies(t *testing.T) {
	tr, fake := fixture(t)

	err := tr.Claim(tracker.ClaimRequest{
		Key: "VO-1", RunID: "прогон-1", Owner: "implementer",
		LeaseUntil: now.Add(30 * time.Minute), ExpectStatus: "Ready", WorkingStatus: "InProgress",
	})
	if err != nil {
		t.Fatalf("захват не удался: %v", err)
	}

	if fake.lastUpdate["customfield_10002"] != "прогон-1" {
		t.Errorf("run_id не записан: %+v", fake.lastUpdate)
	}
	if fake.lastUpdate["customfield_10001"] != "implementer" {
		t.Errorf("владелец не записан: %+v", fake.lastUpdate)
	}
	// Формат даты — тот, который принимает JIRA: миллисекунды и смещение без двоеточия.
	lease, _ := fake.lastUpdate["customfield_10003"].(string)
	if _, err := time.Parse(dateLayout, lease); err != nil {
		t.Errorf("срок аренды записан как %q: %v", lease, err)
	}
	if len(fake.transitons) != 1 || fake.transitons[0] != "In Progress" {
		t.Errorf("переходы: %v, ожидался один в In Progress", fake.transitons)
	}
}

// Рабочий статус — опция роли. Без него захват записывает аренду и не трогает
// статус: «в работе» означает живую аренду в том же статусе, из которого роль
// читает. Перевод в пустой статус был бы бедой, а перевод «в тот же самый» —
// запросом, которого workflow может и не разрешить: переход Review → Review
// в JIRA существует не всегда.
func TestClaimWithoutWorkingStatusKeepsColumn(t *testing.T) {
	cases := map[string]string{
		"рабочего статуса у роли нет":               "",
		"рабочий статус тот же, из которого читаем": "Review",
	}

	for name, working := range cases {
		t.Run(name, func(t *testing.T) {
			tr, fake := fixture(t)
			fake.status = "Review"

			err := tr.Claim(tracker.ClaimRequest{
				Key: "VO-1", RunID: "прогон-1", Owner: "reviewer",
				LeaseUntil: now.Add(30 * time.Minute), ExpectStatus: "Review", WorkingStatus: working,
			})
			if err != nil {
				t.Fatalf("захват не удался: %v", err)
			}
			if fake.lastUpdate["customfield_10002"] != "прогон-1" {
				t.Errorf("аренда не записана: %+v", fake.lastUpdate)
			}
			if len(fake.transitons) != 0 {
				t.Errorf("переходы: %v, ожидалось ни одного", fake.transitons)
			}
			if fake.status != "Review" {
				t.Errorf("статус стал %q, ожидался прежний Review", fake.status)
			}
		})
	}
}

func TestClaimLostWhenAnotherRunWins(t *testing.T) {
	tr, fake := fixture(t)
	fake.verifyRunID = "чужой" // перечитывание покажет другого владельца

	err := tr.Claim(tracker.ClaimRequest{
		Key: "VO-1", RunID: "прогон-1", Owner: "implementer",
		LeaseUntil: now.Add(30 * time.Minute), ExpectStatus: "Ready", WorkingStatus: "InProgress",
	})
	if !errors.Is(err, tracker.ErrClaimLost) {
		t.Errorf("проигранный захват дал %v, ожидалось ErrClaimLost", err)
	}
}

func TestClaimChecksExpectedStatus(t *testing.T) {
	tr, fake := fixture(t)
	fake.status = "Review"

	err := tr.Claim(tracker.ClaimRequest{
		Key: "VO-1", RunID: "прогон-1", Owner: "implementer",
		LeaseUntil: now.Add(time.Minute), ExpectStatus: "Ready", WorkingStatus: "InProgress",
	})
	if !errors.Is(err, tracker.ErrClaimLost) {
		t.Errorf("захват из чужого статуса дал %v, ожидалось ErrClaimLost", err)
	}
}

// Правило владения общее для всех трекеров, и JIRA не исключение: проверка
// делается тем же CheckOwner, что у файлового трекера.
func TestMutationsFollowOwnership(t *testing.T) {
	tr, fake := fixture(t)
	fake.status = "In Progress"
	fake.runID = "прогон-1"
	fake.leaseUntil = "2026-08-17T12:30:00.000+0000"

	if err := tr.Transition("VO-1", tracker.ByRun("чужой"), "Review"); !errors.Is(err, tracker.ErrNotOwner) {
		t.Errorf("чужой прогон подвинул задачу: %v", err)
	}
	if err := tr.Comment("VO-1", tracker.BySystem(), "текст"); !errors.Is(err, tracker.ErrNotOwner) {
		t.Errorf("системная операция при живой аренде прошла: %v", err)
	}
	if err := tr.Transition("VO-1", tracker.ByRun("прогон-1"), "Review"); err != nil {
		t.Errorf("владелец не смог подвинуть задачу: %v", err)
	}
	if fake.status != "Review" {
		t.Errorf("статус %q, ожидался Review", fake.status)
	}
}

func TestCommentAndRead(t *testing.T) {
	tr, fake := fixture(t)
	fake.status = "In Progress"
	fake.runID = "прогон-1"
	fake.leaseUntil = "2026-08-17T12:30:00.000+0000"

	if err := tr.Comment("VO-1", tracker.ByRun("прогон-1"), "[office run:1 role:implementer outcome:done config:abc]\nготово"); err != nil {
		t.Fatalf("комментарий не записан: %v", err)
	}
	task, err := tr.Get("VO-1")
	if err != nil {
		t.Fatalf("задача не прочитана: %v", err)
	}
	if len(task.Comments) != 1 {
		t.Fatalf("комментариев %d, ожидался 1", len(task.Comments))
	}
	if task.Comments[0].Author != "office" || !strings.Contains(task.Comments[0].Body, "готово") {
		t.Errorf("комментарий прочитан как %+v", task.Comments[0])
	}
	if task.Comments[0].Created.IsZero() {
		t.Error("время комментария не разобрано")
	}
}

func TestReleaseClearsLease(t *testing.T) {
	tr, fake := fixture(t)
	fake.status = "In Progress"
	fake.runID = "прогон-1"
	fake.owner = "implementer"
	fake.leaseUntil = "2026-08-17T12:30:00.000+0000"

	if err := tr.Release("VO-1", tracker.ByRun("прогон-1")); err != nil {
		t.Fatalf("аренда не снята: %v", err)
	}
	for _, field := range []string{"customfield_10001", "customfield_10002", "customfield_10003"} {
		if value, ok := fake.lastUpdate[field]; !ok || value != nil {
			t.Errorf("поле %s после снятия аренды: %v (ok=%v)", field, value, ok)
		}
	}
	if fake.status != "In Progress" {
		t.Errorf("Release тронул статус: %q", fake.status)
	}
}

func TestSetHumanFlagUsesLabel(t *testing.T) {
	tr, fake := fixture(t)
	fake.status = "Blocked"
	fake.labels = []string{"demo"}

	if err := tr.SetHumanFlag("VO-1", tracker.BySystem(), true); err != nil {
		t.Fatalf("метка не выставлена: %v", err)
	}
	if !strings.Contains(strings.Join(fake.labels, ","), "office-waits-human") {
		t.Errorf("метки: %v", fake.labels)
	}
	if !strings.Contains(strings.Join(fake.labels, ","), "demo") {
		t.Errorf("чужая метка потеряна: %v", fake.labels)
	}

	if err := tr.SetHumanFlag("VO-1", tracker.BySystem(), false); err != nil {
		t.Fatalf("метка не снята: %v", err)
	}
	if strings.Contains(strings.Join(fake.labels, ","), "office-waits-human") {
		t.Errorf("метка не снята: %v", fake.labels)
	}
}

// JQL — это то, чем раннер отбирает кандидатов, и ошибка в нём тихо приводит
// к «работы нет». Проверяем форму запроса, а не только ответ.
func TestListReadyBuildsJQL(t *testing.T) {
	tr, fake := fixture(t)

	refs, err := tr.ListReady("VO", "Ready")
	if err != nil {
		t.Fatalf("список не прочитан: %v", err)
	}
	if len(refs) != 1 || refs[0].Key != "VO-1" {
		t.Errorf("кандидаты: %+v", refs)
	}
	for _, want := range []string{`project = "VO"`, `status = "Ready"`, "cf[10003]", "IS EMPTY", "now()"} {
		if !strings.Contains(fake.lastJQL, want) {
			t.Errorf("в JQL нет %q: %s", want, fake.lastJQL)
		}
	}

	if _, err := tr.ListExpired("VO", now); err != nil {
		t.Fatalf("список истёкших не прочитан: %v", err)
	}
	for _, want := range []string{"cf[10002] IS NOT EMPTY", "cf[10003]"} {
		if !strings.Contains(fake.lastJQL, want) {
			t.Errorf("в JQL истёкших нет %q: %s", want, fake.lastJQL)
		}
	}
}

// Живая аренда не должна попадать в кандидаты, даже если JQL по какой-то причине
// её отдал: сервер сравнивает время своими часами, а решает раннер своими.
func TestListReadyDropsLiveLease(t *testing.T) {
	tr, fake := fixture(t)
	fake.runID = "прогон-1"
	fake.leaseUntil = "2026-08-17T12:30:00.000+0000"

	refs, err := tr.ListReady("VO", "Ready")
	if err != nil {
		t.Fatalf("список не прочитан: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("задача с живой арендой попала в кандидаты: %+v", refs)
	}
}

// accountsConfig — конфигурация с учёткой роли сверх общей.
func accountsConfig(baseURL string) Config {
	return Config{
		BaseURL: baseURL,
		Auth:    Auth{Mode: "basic"},
		Accounts: Accounts{
			Default: Account{UserEnv: "JIRA_USER", SecretEnv: "JIRA_PASSWORD"},
			Roles: map[string]Account{
				"reviewer": {UserEnv: "JIRA_REVIEWER_USER", SecretEnv: "JIRA_REVIEWER_PASSWORD"},
			},
		},
		AlsoAgents:     []string{"renovate-bot"},
		StatusMap:      map[string]string{"Ready": "Ready", "Review": "Review"},
		Fields:         Fields{Owner: "customfield_10001", RunID: "customfield_10002", LeaseUntil: "customfield_10003", Attempts: "customfield_10004"},
		HumanFlagLabel: "office-waits-human",
	}
}

func setAccountsEnv(t *testing.T) {
	t.Helper()
	t.Setenv("JIRA_USER", "office")
	t.Setenv("JIRA_PASSWORD", "секрет")
	t.Setenv("JIRA_REVIEWER_USER", "office-reviewer")
	t.Setenv("JIRA_REVIEWER_PASSWORD", "секрет-ревьюера")
}

// Роль ходит под своей учёткой: история тикета читается людьми, а права в JIRA
// разводятся. Проверяем не поле в структуре, а то, чем подписан запрос.
func TestOpenAsUsesRoleAccount(t *testing.T) {
	setAccountsEnv(t)
	fake := &fakeJira{t: t, status: "Ready"}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	tr, err := OpenAs(accountsConfig(server.URL), "reviewer")
	if err != nil {
		t.Fatalf("трекер роли не открыт: %v", err)
	}
	if _, err := tr.Whoami(); err != nil {
		t.Fatalf("запрос не прошёл: %v", err)
	}
	if fake.lastUser != "office-reviewer" {
		t.Errorf("запрос ушёл под %q, ожидалась учётка роли", fake.lastUser)
	}
}

// Своей учётки у роли может не быть, и это штатный режим: одна учётка на всех
// агентов ничего не ломает — роли различаются маркером, а не автором.
func TestOpenAsFallsBackToDefault(t *testing.T) {
	setAccountsEnv(t)
	fake := &fakeJira{t: t, status: "Ready"}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	tr, err := OpenAs(accountsConfig(server.URL), "implementer")
	if err != nil {
		t.Fatalf("трекер роли не открыт: %v", err)
	}
	if _, err := tr.Whoami(); err != nil {
		t.Fatalf("запрос не прошёл: %v", err)
	}
	if fake.lastUser != "office" {
		t.Errorf("запрос ушёл под %q, ожидалась общая учётка", fake.lastUser)
	}
}

// Список агентских учёток выводится из конфигурации, а не пишется руками:
// забытая в списке роль стала бы «голосом человека», и её собственный отчёт
// вернул бы задачу в очередь.
func TestAgentAccountsAreDerived(t *testing.T) {
	setAccountsEnv(t)

	names, err := accountsConfig("http://localhost").AgentAccounts()
	if err != nil {
		t.Fatalf("учётки не выведены: %v", err)
	}
	for _, want := range []string{"office", "office-reviewer", "renovate-bot"} {
		if !slices.Contains(names, want) {
			t.Errorf("в списке агентов нет %q: %q", want, names)
		}
	}
}

// Имя учётки живёт в окружении, и пустая переменная — не пустяк: комментарии этой
// роли перестали бы отличаться от слов человека, и офис начал бы отвечать сам себе.
func TestAgentAccountsRejectEmptyName(t *testing.T) {
	setAccountsEnv(t)
	t.Setenv("JIRA_REVIEWER_USER", "")

	if _, err := accountsConfig("http://localhost").AgentAccounts(); err == nil {
		t.Fatal("учётка без имени принята")
	}
}

// Имя учётки берётся из окружения, а сервер видит того, кому принадлежит кред.
// Разойдись они — офис перестал бы узнавать собственные комментарии и принялся
// бы отвечать сам себе: свой отчёт разблокировал бы задачу, которую сам и закрыл.
func TestCheckAccountCatchesNameMismatch(t *testing.T) {
	setAccountsEnv(t)
	t.Setenv("JIRA_USER", "не-тот-кто-в-креде")
	fake := &fakeJira{t: t, status: "Ready"} // сервер отвечает: ты office
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	tr, err := Open(accountsConfig(server.URL))
	if err != nil {
		t.Fatalf("трекер не открыт: %v", err)
	}

	err = tr.CheckAccount()
	if err == nil {
		t.Fatal("расхождение имени учётки не замечено")
	}
	if !strings.Contains(err.Error(), "office") {
		t.Errorf("ошибка не называет того, кем нас видит сервер: %v", err)
	}
}

// Совпадение — тишина: сверка не должна мешать нормальному запуску.
func TestCheckAccountPassesWhenNamesMatch(t *testing.T) {
	tr, _ := fixture(t)
	if err := tr.CheckAccount(); err != nil {
		t.Errorf("сверка не прошла при совпадении имён: %v", err)
	}
}

// `ls` показывает всё, а не очередь: задачи в названных статусах как есть,
// вместе с живой арендой. Один запрос на проект, переписка не тянется.
func TestListBuildsJQLForStatuses(t *testing.T) {
	tr, fake := fixture(t)
	fake.owner, fake.runID = "implementer", "прогон-1"
	fake.leaseUntil = now.Add(time.Hour).Format(dateLayout)

	refs, err := tr.List("VO", []string{"Ready", "InProgress"})
	if err != nil {
		t.Fatalf("доска не прочитана: %v", err)
	}

	for _, want := range []string{`project = "VO"`, `"Ready"`, `"In Progress"`} {
		if !strings.Contains(fake.lastJQL, want) {
			t.Errorf("в JQL нет %s: %s", want, fake.lastJQL)
		}
	}
	// Аренду отбрасывает ListReady, а не этот метод: `ls` — как раз про то,
	// кто над задачей работает.
	if len(refs) != 1 {
		t.Fatalf("задач %d, ожидалась 1: %+v", len(refs), refs)
	}
	if refs[0].Owner != "implementer" || !refs[0].LeaseAlive(now) {
		t.Errorf("аренда не доехала до списка: %+v", refs[0])
	}
	if refs[0].Updated.IsZero() {
		t.Errorf("возраст задачи неизвестен: %+v", refs[0])
	}
	if fake.commentPages != 0 {
		t.Errorf("список задач полез за переписью: страниц %d", fake.commentPages)
	}
}

// fakeSearchIssue — минимальная задача для страницы /search: только то,
// что нужно List(), чтобы дойти до конца и не упасть на разборе полей.
func fakeSearchIssue(key string) map[string]any {
	return map[string]any{
		"key": key,
		"fields": map[string]any{
			"summary": "задача " + key,
			"status":  map[string]any{"name": "Ready"},
			"project": map[string]any{"key": "VO"},
			"updated": "2026-08-17T12:00:00.000+0000",
		},
	}
}

// TestListPaginatesBeyondFirstPage доказывает fix round 1, Finding 1:
// List() раньше отдавал только первую страницу поиска (searchPage=50)
// от старых задач по created ASC — дети сплита, будучи самыми новыми,
// молча выпадали из среза, на котором строится гейт зависимостей
// (Office.projectByKey → UnmetDependencies), и заблокированная задача
// стояла бы вечно, приняв настоящую-но-невидимую зависимость за
// отсутствующую. Тест даёт фейковому серверу заведомо больше одной
// страницы фикстур и проверяет, что List() вернул их все — с
// пагинацией по startAt, тем же приёмом, что comments() уже делает
// для переписки.
func TestListPaginatesBeyondFirstPage(t *testing.T) {
	tr, fake := fixture(t)
	const total = searchPage + 7 // заведомо больше одной страницы
	fake.searchIssues = make([]map[string]any, total)
	want := make(map[string]bool, total)
	for i := range total {
		key := fmt.Sprintf("VO-%d", i+1)
		fake.searchIssues[i] = fakeSearchIssue(key)
		want[key] = true
	}

	refs, err := tr.List("VO", []string{"Ready"})
	if err != nil {
		t.Fatalf("список не прочитан: %v", err)
	}
	if len(refs) != total {
		t.Fatalf("получено %d задач из %d: список обрублен первой страницей поиска", len(refs), total)
	}
	for _, ref := range refs {
		if !want[ref.Key] {
			t.Errorf("неожиданный ключ %s", ref.Key)
		}
		delete(want, ref.Key)
	}
	if len(want) != 0 {
		t.Errorf("не вернулись ключи: %v", want)
	}
	if fake.searchPages < 2 {
		t.Errorf("страниц запрошено %d: пагинации не было, тест бы прошёл и без фикса", fake.searchPages)
	}
}

// TestListSurvivesLowerServerSideMaxResultsCap доказывает fix round 2,
// Finding 8: старая проверка «страница короче ЗАПРОШЕННОГО — значит,
// последняя» ломается об инстанс со своим потолком страницы поиска
// (jira.search.views.default.max) ниже searchPage — там КАЖДАЯ страница
// короче запрошенного, и старый код обрывал бы пагинацию после первой
// же страницы, теряя весь хвост списка, на котором строится ground
// truth гейта зависимостей (Office.projectByKey → UnmetDependencies).
// Новая логика опирается на total и на maxResults, которые сервер
// сообщает в самом ответе, а не на то, что было запрошено.
func TestListSurvivesLowerServerSideMaxResultsCap(t *testing.T) {
	tr, fake := fixture(t)
	const total = searchPage + 7 // больше одной страницы даже без потолка сервера
	fake.searchIssues = make([]map[string]any, total)
	want := make(map[string]bool, total)
	for i := range total {
		key := fmt.Sprintf("VO-%d", i+1)
		fake.searchIssues[i] = fakeSearchIssue(key)
		want[key] = true
	}
	// Сервер не отдаёт больше 10 штук за раз, что бы клиент ни просил
	// (searchPage=50) — заведомо ниже searchPage и не делитель total, чтобы
	// короткая последняя страница осталась короткой и по старому критерию
	// тоже (иначе тест давал бы ложный зелёный на старом коде).
	fake.searchServerCap = 10

	refs, err := tr.List("VO", []string{"Ready"})
	if err != nil {
		t.Fatalf("список не прочитан: %v", err)
	}
	if len(refs) != total {
		t.Fatalf("получено %d задач из %d: пагинация приняла серверный потолок страницы за конец списка", len(refs), total)
	}
	for _, ref := range refs {
		if !want[ref.Key] {
			t.Errorf("неожиданный ключ %s", ref.Key)
		}
		delete(want, ref.Key)
	}
	if len(want) != 0 {
		t.Errorf("не вернулись ключи: %v", want)
	}
}

// TestListReadyPaginatesBeyondFirstPage доказывает pr-converge round 1,
// Finding 7: ListReady, в отличие от List() (fix round 1, Finding 1),
// оставался одностраничным (searchProject, потолок searchPage) даже
// после того, как эта волна впервые сделала пропуск кандидата
// потенциально вечным — заблокированная зависимостью задача, в отличие
// от прежних причин пропуска (истёкшая аренда, исчерпанные попытки),
// никуда не уходит и занимает место на странице сколько угодно долго.
// При ≥searchPage задач в Ready и заблокированных в начале списка
// свободные кандидаты за первой страницей были бы не видны claim()
// вовсе. Тест даёт фейковому серверу заведомо больше одной страницы
// кандидатов Ready и проверяет, что ListReady вернул их все.
func TestListReadyPaginatesBeyondFirstPage(t *testing.T) {
	tr, fake := fixture(t)
	const total = searchPage + 7 // заведомо больше одной страницы
	fake.searchIssues = make([]map[string]any, total)
	want := make(map[string]bool, total)
	for i := range total {
		key := fmt.Sprintf("VO-%d", i+1)
		fake.searchIssues[i] = fakeSearchIssue(key)
		want[key] = true
	}

	refs, err := tr.ListReady("VO", "Ready")
	if err != nil {
		t.Fatalf("список не прочитан: %v", err)
	}
	if len(refs) != total {
		t.Fatalf("получено %d задач из %d: ListReady обрублен первой страницей поиска", len(refs), total)
	}
	for _, ref := range refs {
		if !want[ref.Key] {
			t.Errorf("неожиданный ключ %s", ref.Key)
		}
		delete(want, ref.Key)
	}
	if len(want) != 0 {
		t.Errorf("не вернулись ключи: %v", want)
	}
}

// TestSearchAllProjectStopsAfterTaskCap доказывает pr-converge round 1,
// Finding 5 (bot rebuttal на F11): без потолка searchAllProject крутился
// бы вечно на сервере, который никогда не подтверждает конец списка
// (total всегда 0, страница всегда полная) — в отличие от comments(),
// эта функция теперь ходит по целому проекту (List/ListReady), а не по
// переписке одного тикета, так что цена такого зависания выше.
//
// Потолок мерян в задачах (startAt), не в страницах (pr-converge round 3,
// Finding 2) — тест задаёт его как 5×searchPage, так что фейковый сервер
// (страница ровно searchPage, ничего не режущий) даёт ровно 5 запросов,
// но сама проверка срабатывания не зависит от размера страницы. Потолок
// временно снижен, чтобы тест не гонял 10000 настоящих задач — пакетная
// переменная, не параметр: безопасно постольку, поскольку в пакете нет
// t.Parallel().
//
// Проверка — точное число запросов, не "хотя бы потолок": "< 5" прошёл
// бы и при потолке в 6 запросов — смещении на единицу в
// startAt >= searchAllProjectTaskCap, которое тест на границу и должен
// ловить.
func TestSearchAllProjectStopsAfterTaskCap(t *testing.T) {
	tr, fake := fixture(t)
	orig := searchAllProjectTaskCap
	searchAllProjectTaskCap = 5 * searchPage
	t.Cleanup(func() { searchAllProjectTaskCap = orig })
	fake.searchNeverEnds = true

	_, err := tr.List("VO", []string{"Ready"})
	if err == nil {
		t.Fatal("ожидалась ошибка: сервер никогда не подтверждает конец списка")
	}
	if fake.searchPages != 5 {
		t.Errorf("запросов сделано %d, ожидалось ровно 5 (потолок 5×searchPage задач)", fake.searchPages)
	}
}

// tracker.example.yaml — то, из чего собирают конфигурацию нового инстанса,
// и битый образец обнаружился бы первым же циклом против JIRA, то есть на живой
// доске. Сам tracker.yaml проверить нечем: он машинный и в репозитории его нет.
//
// Образец копируется дословно и правится в пяти значениях (base_url и четыре
// поля), поэтому всё необязательное в нём выключено: одна учётка на все роли,
// ни одного чужого бота, тип задачи по умолчанию. Тип связи, напротив,
// включён — его заводит scripts/jira-setup.sh.
func TestShippedTrackerConfigIsValid(t *testing.T) {
	root := filepath.Join("..", "..", "..", "office")

	cfg, err := LoadConfig(filepath.Join(root, ExampleFile))
	if err != nil {
		t.Fatalf("%s не загружен: %v", ExampleFile, err)
	}
	if len(cfg.Accounts.Roles) != 0 {
		t.Errorf("accounts.roles в образце активен (%v): копия заставила бы заводить учётку роли", cfg.Accounts.Roles)
	}
	if len(cfg.AlsoAgents) != 0 {
		t.Errorf("also_agents в образце не пуст: %v", cfg.AlsoAgents)
	}
	if cfg.IssueType != "" {
		t.Errorf("issue_type в образце задан (%q): умолчание кода — Task, поле незачем включать", cfg.IssueType)
	}
	if cfg.DependsOnLink != "Depends" {
		t.Errorf("depends_on_link = %q, ожидался Depends — его заводит jira-setup.sh", cfg.DependsOnLink)
	}

	// Заголовок написан для рабочего файла: после копии в ${OFFICE_HOME} он
	// не должен называть себя образцом, который раннер не читает.
	raw, err := os.ReadFile(filepath.Join(root, ExampleFile))
	if err != nil {
		t.Fatalf("%s не прочитан: %v", ExampleFile, err)
	}
	if strings.Contains(string(raw), "не читает") {
		t.Errorf("%s всё ещё описывает себя как образец, который раннер не читает", ExampleFile)
	}
}

// Учётки ролей — опция, общая — нет: без неё офису нечем ходить в трекер вовсе.
func TestLoadConfigRequiresDefaultAccount(t *testing.T) {
	body := `base_url: http://localhost
auth: { mode: basic }
accounts:
  roles:
    reviewer: { user_env: JIRA_REVIEWER_USER, secret_env: JIRA_REVIEWER_PASSWORD }
status_map: { Ready: Ready }
fields:
  agent_owner: customfield_10001
  run_id: customfield_10002
  lease_until: customfield_10003
  attempts: customfield_10004
human_flag_label: office-waits-human
`
	path := filepath.Join(t.TempDir(), TrackerFile)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("конфигурация не записана: %v", err)
	}

	if _, err := LoadConfig(path); err == nil {
		t.Fatal("конфигурация без общей учётки принята")
	}
}

// depends_on_link нужен только LinkDependsOn — узкой опциональной операции,
// а не каждому вызову трекера. Отказ на его отсутствие здесь означал бы, что
// Comment, Transition и Get ломаются из-за поля, которое им не нужно:
// проверка обязана жить в LinkDependsOn, а не в LoadConfig.
func TestLoadConfigSucceedsWithoutDependsOnLink(t *testing.T) {
	body := `base_url: http://localhost
auth: { mode: basic }
accounts:
  default: { user_env: JIRA_USER, secret_env: JIRA_PASSWORD }
status_map: { Ready: Ready }
fields:
  agent_owner: customfield_10001
  run_id: customfield_10002
  lease_until: customfield_10003
  attempts: customfield_10004
human_flag_label: office-waits-human
`
	path := filepath.Join(t.TempDir(), TrackerFile)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("конфигурация не записана: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("конфигурация без depends_on_link не загружена: %v", err)
	}
	if cfg.DependsOnLink != "" {
		t.Errorf("DependsOnLink = %q, ожидалась пустая строка", cfg.DependsOnLink)
	}
}

func TestConfigRequiresCredential(t *testing.T) {
	t.Setenv("JIRA_USER", "office")
	t.Setenv("JIRA_PASSWORD", "")

	_, err := Open(Config{
		BaseURL:  "http://localhost",
		Auth:     Auth{Mode: "basic"},
		Accounts: Accounts{Default: Account{UserEnv: "JIRA_USER", SecretEnv: "JIRA_PASSWORD"}},
	})
	if err == nil {
		t.Error("трекер открылся без креда")
	}
}

// Раннер режет историю с конца — по последнему маркеру своей роли, — поэтому
// ему нужен именно хвост. Сортировка по возрастанию отдаёт первую сотню, то есть
// начало: на длинной переписке ответ человека потерялся бы молча.
func TestGetReadsAllCommentPages(t *testing.T) {
	tr, fake := fixture(t)
	const total = 250
	for i := 1; i <= total; i++ {
		fake.comments = append(fake.comments, map[string]any{
			"id": fmt.Sprint(i), "body": fmt.Sprintf("комментарий %d", i),
			"author":  map[string]any{"name": "office"},
			"created": "2026-08-17T12:00:00.000+0000",
		})
	}

	task, err := tr.Get("VO-1")
	if err != nil {
		t.Fatalf("задача не прочитана: %v", err)
	}

	if len(task.Comments) != total {
		t.Fatalf("получено %d комментариев из %d: хвост переписки потерян", len(task.Comments), total)
	}
	if fake.commentPages < 2 {
		t.Errorf("страниц запрошено %d: пагинации нет, а тест её якобы проверил", fake.commentPages)
	}
	// Порядок обязан уцелеть: по нему раннер находит последний маркер.
	if got := task.Comments[total-1].Body; got != "комментарий 250" {
		t.Errorf("последний комментарий %q, ожидался «комментарий 250»", got)
	}
	if got := task.Comments[0].Body; got != "комментарий 1" {
		t.Errorf("первый комментарий %q, ожидался «комментарий 1»", got)
	}
}

// Сервер вправе соврать про размер переписки, и цикл по страницам не должен
// становиться вечным: пустая страница означает, что читать больше нечего.
// Без этой оговорки раннер молотил бы JIRA запросами до скончания века.
func TestGetStopsWhenServerLiesAboutTotal(t *testing.T) {
	tr, fake := fixture(t)
	for i := 1; i <= 100; i++ {
		fake.comments = append(fake.comments, map[string]any{
			"id": fmt.Sprint(i), "body": fmt.Sprintf("комментарий %d", i),
			"author":  map[string]any{"name": "office"},
			"created": "2026-08-17T12:00:00.000+0000",
		})
	}
	fake.fakeTotal = 250 // а на деле их сто

	task, err := tr.Get("VO-1")
	if err != nil {
		t.Fatalf("задача не прочитана: %v", err)
	}
	if len(task.Comments) != 100 {
		t.Errorf("получено %d комментариев, а лежит 100", len(task.Comments))
	}
}

// Режим pat конфигурация объявляла, а код не реализовывал: запрос уходил
// с basic-авторизацией независимо от него. Обещание, которого никто не держит,
// хуже отсутствия обещания — и Open теперь отказывается его давать.
// TestOpenRejectsBaseURLWithoutSchemeOrHost — "jira.example.com" (без схемы)
// разбирается url.Parse без ошибки, но даёт пустой Host: без проверки на
// открытии всё ломалось бы позже и молча, на каждом download() (внешнее
// ревью, pr-converge раунд 1).
func TestOpenRejectsBaseURLWithoutSchemeOrHost(t *testing.T) {
	t.Setenv("JIRA_USER", "office")
	t.Setenv("JIRA_PASSWORD", "секрет")

	_, err := Open(Config{
		BaseURL:  "jira.example.com",
		Auth:     Auth{Mode: "basic"},
		Accounts: Accounts{Default: Account{UserEnv: "JIRA_USER", SecretEnv: "JIRA_PASSWORD"}},
	})
	if err == nil {
		t.Fatal("base_url без схемы принят — download() будет молча ломаться на пустом хосте")
	}
}

func TestOpenRejectsUnimplementedAuthMode(t *testing.T) {
	t.Setenv("JIRA_USER", "office")
	t.Setenv("JIRA_PASSWORD", "секрет")

	_, err := Open(Config{
		BaseURL:  "http://localhost",
		Auth:     Auth{Mode: "pat"},
		Accounts: Accounts{Default: Account{UserEnv: "JIRA_USER", SecretEnv: "JIRA_PASSWORD"}},
	})
	if err == nil {
		t.Fatal("трекер открылся в режиме, которого нет: запросы пошли бы как basic")
	}
	if !strings.Contains(err.Error(), "basic") {
		t.Errorf("отказ не подсказывает рабочий режим: %v", err)
	}
}

// Проект, описанный в projects.local.yaml, но неизвестный трекеру, роняет весь цикл:
// раннер обходит проекты по порядку и на первом же отказе бросает остальные.
// Поймано живой проверкой — заглушка OFFICE остановила reap до настоящего VO.
func TestListReadyTellsUnknownProjectApart(t *testing.T) {
	tr, fake := fixture(t)
	fake.badSearch = true
	fake.knownProject = "VO"

	_, err := tr.ListReady("OFFICE", "Ready")
	if !errors.Is(err, tracker.ErrNoProject) {
		t.Fatalf("незнакомый проект не распознан: %v", err)
	}
	if !strings.Contains(err.Error(), "OFFICE") {
		t.Errorf("в ошибке нет имени проекта, чинить придётся вслепую: %v", err)
	}
}

// Глобальный вход в рабочий статус — та самая лазейка, из-за которой захват
// не становится CAS: задача, уже находящаяся в работе, видит переход в работу,
// то есть второй прогон исполнит его без всякой помехи. Раннер обязан заметить
// это сам, а не тогда, когда двое возьмут одну задачу.
func TestCheckWorkflowFindsSelfEntry(t *testing.T) {
	tr, fake := fixture(t)
	fake.status = "In Progress"

	check, err := tr.CheckWorkflow("VO", "InProgress")
	if err != nil {
		t.Fatalf("проверка не выполнена: %v", err)
	}
	if check.Sample != "VO-1" {
		t.Errorf("образец %q, ожидалась VO-1", check.Sample)
	}
	if !check.SelfEntry {
		t.Error("глобальный вход в рабочий статус не замечен")
	}
	if !strings.Contains(fake.lastJQL, `status = "In Progress"`) {
		t.Errorf("искали не в рабочем статусе: %s", fake.lastJQL)
	}
	// Нужна одна задача, а не очередь: это проверка настройки, а не поиск работы.
	if fake.lastLimit != 1 {
		t.Errorf("поиск просил %d задач, хватает одной", fake.lastLimit)
	}
}

// Workflow, настроенный под несколько раннеров: у задачи в работе перехода
// в работу нет, и захват переходом станет настоящим CAS.
func TestCheckWorkflowPassesWithoutSelfEntry(t *testing.T) {
	tr, fake := fixture(t)
	fake.status = "In Progress"
	fake.transitionsTo = []string{"Review", "Blocked", "Ready"}

	check, err := tr.CheckWorkflow("VO", "InProgress")
	if err != nil {
		t.Fatalf("проверка не выполнена: %v", err)
	}
	if check.Sample == "" {
		t.Fatal("образец не найден, хотя задача в работе есть")
	}
	if check.SelfEntry {
		t.Error("вход в рабочий статус из него самого померещился")
	}
}

// Переходы JIRA показывает только у конкретной задачи, поэтому без задачи
// в рабочем статусе проверять не на чем. Пустой ответ обязан отличаться
// от «всё хорошо»: иначе раннер молчал бы так, будто проверил.
func TestCheckWorkflowWithoutSampleTask(t *testing.T) {
	tr, fake := fixture(t)
	fake.noIssues = true

	check, err := tr.CheckWorkflow("VO", "InProgress")
	if err != nil {
		t.Fatalf("проверка не выполнена: %v", err)
	}
	if check.Sample != "" || check.SelfEntry {
		t.Errorf("проверка отчиталась, не найдя задачи: %+v", check)
	}
}

func TestCheckFieldsAllPresentWithRightType(t *testing.T) {
	tr, fake := fixture(t)
	fake.remoteFields = []fakeField{
		{id: "customfield_10001", schema: "string"},
		{id: "customfield_10002", schema: "string"},
		{id: "customfield_10003", schema: "datetime"},
		{id: "customfield_10004", schema: "number"},
	}

	checks, err := tr.CheckFields()
	if err != nil {
		t.Fatalf("проверка полей не выполнена: %v", err)
	}
	if len(checks) != 4 {
		t.Fatalf("проверок %d, ожидалось 4: %+v", len(checks), checks)
	}
	for _, c := range checks {
		if !c.Present || c.ActualType != c.ExpectedType {
			t.Errorf("поле %s не совпало: %+v", c.Config, c)
		}
	}
}

func TestCheckFieldsWrongType(t *testing.T) {
	tr, fake := fixture(t)
	fake.remoteFields = []fakeField{
		{id: "customfield_10001", schema: "string"},
		{id: "customfield_10002", schema: "string"},
		{id: "customfield_10003", schema: "string"}, // ожидался datetime
		{id: "customfield_10004", schema: "number"},
	}

	checks, err := tr.CheckFields()
	if err != nil {
		t.Fatalf("проверка полей не выполнена: %v", err)
	}
	var lease FieldCheck
	for _, c := range checks {
		if c.Config == "lease_until" {
			lease = c
		}
	}
	if !lease.Present || lease.ActualType != "string" || lease.ExpectedType != "datetime" {
		t.Errorf("несовпадение типа не замечено: %+v", lease)
	}
}

func TestCheckFieldsAbsent(t *testing.T) {
	tr, fake := fixture(t)
	fake.remoteFields = []fakeField{
		{id: "customfield_10001", schema: "string"},
		{id: "customfield_10002", schema: "string"},
		// customfield_10003 (lease_until) отсутствует на инстансе
		{id: "customfield_10004", schema: "number"},
	}

	checks, err := tr.CheckFields()
	if err != nil {
		t.Fatalf("проверка полей не выполнена: %v", err)
	}
	var lease FieldCheck
	for _, c := range checks {
		if c.Config == "lease_until" {
			lease = c
		}
	}
	if lease.Present || lease.ActualType != "" {
		t.Errorf("отсутствующее поле не замечено: %+v", lease)
	}
	if lease.ID != "customfield_10003" {
		t.Errorf("id настроенного поля не сохранён: %+v", lease)
	}
}

// fixture() задаёт DependsOnLink: "Depends" (см. её тело).
func TestCheckLinkTypePresent(t *testing.T) {
	tr, fake := fixture(t)
	fake.remoteLinkTypes = []string{"Blocks", "Depends"}

	if err := tr.CheckLinkType(); err != nil {
		t.Errorf("существующий тип связи не принят: %v", err)
	}
}

func TestCheckLinkTypeAbsent(t *testing.T) {
	tr, fake := fixture(t)
	fake.remoteLinkTypes = []string{"Blocks"}

	err := tr.CheckLinkType()
	if err == nil || !strings.Contains(err.Error(), "Depends") {
		t.Errorf("отсутствующий тип связи не назван: %v", err)
	}
}

func TestCheckLinkTypeSkippedWhenEmpty(t *testing.T) {
	tr, fake := fixtureWithoutDependsOnLink(t)

	if err := tr.CheckLinkType(); err != nil {
		t.Errorf("пустой depends_on_link должен молча пропускаться: %v", err)
	}
	if slices.Contains(fake.hitPaths, "/rest/api/2/issueLinkType") {
		t.Error("пустой depends_on_link не должен звать инстанс")
	}
}

// Отличать надо именно незнакомый проект: упавший поиск по любой другой причине
// обязан оставаться бедой, а не поводом молча пропустить проект целиком.
func TestListReadyKeepsOtherSearchFailures(t *testing.T) {
	tr, fake := fixture(t)
	fake.badSearch = true
	fake.knownProject = "VO"

	_, err := tr.ListReady("VO", "Ready")
	if err == nil {
		t.Fatal("отказ поиска потерян")
	}
	if errors.Is(err, tracker.ErrNoProject) {
		t.Errorf("обычный отказ поиска выдан за незнакомый проект: %v", err)
	}
}

// Отсутствие tracker.yaml — самый частый отказ на новой машине: подключение
// к JIRA свойство инстанса, и в репозитории его нет. Отказ обязан назвать
// и путь, которого не хватает, и образец, из которого файл делают, — иначе
// человек пойдёт искать его в репозитории и не найдёт.
func TestLoadConfigNamesExampleWhenMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), TrackerFile)

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("отсутствие файла принято за годную конфигурацию")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("отказ не назвал путь: %v", err)
	}
	if !strings.Contains(err.Error(), ExampleFile) {
		t.Errorf("отказ не назвал образец: %v", err)
	}
}

// Пустой файл на новой машине — обычный шаг («завёл, ещё не заполнил»), и отказ
// обязан назвать недостающее, а не сказать «не разобран: EOF». Адрес инстанса
// среди недостающего называется первым: без него идти некуда.
func TestLoadConfigNamesWhatIsMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), TrackerFile)
	if err := os.WriteFile(path, []byte("# сюда допишу позже\n"), 0o644); err != nil {
		t.Fatalf("файл не записан: %v", err)
	}

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("пустая конфигурация принята за годную")
	}
	if strings.Contains(err.Error(), "EOF") {
		t.Errorf("отказ говорит про EOF вместо причины: %v", err)
	}
	for _, want := range []string{"base_url", "accounts.default", "status_map", "human_flag_label"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("отказ не назвал %s: %v", want, err)
		}
	}
}

// CreateTask заводит задачу через POST /issue: тип задачи, метки и переписанное
// в wiki-разметку описание обязаны доехать до тела запроса, а ключ созданной
// задачи — вернуться в TaskRef тем же путём, что и обычное чтение (Get).
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
	// Тело запроса едет через настоящий HTTP: сервер разбирает JSON в map[string]any,
	// и массив приходит как []any, а не []string — так же, как в apply() выше.
	labels, _ := fake.created[0].fields["labels"].([]any)
	if len(labels) != 1 || labels[0] != "split-child:VO-1:category-crud" {
		t.Errorf("labels %v", fake.created[0].fields["labels"])
	}
}

// TestCreateTaskDoesNotReconvertDescriptionAppend доказывает, что
// DescriptionAppend едет в description как есть, а не через wiki(): текст
// в нём читан из другой задачи и уже в её собственной разметке. Если бы
// он полз через общий конвертер вместе с Description, wiki-ссылка
// "[текст|https://example.com]" (легальная, но не markdown-форма)
// экранировалась бы escapeBrackets так же, как случайная квадратная
// скобка в прозе, — и превращалась в нечитаемый текст на JIRA.
func TestCreateTaskDoesNotReconvertDescriptionAppend(t *testing.T) {
	tr, fake := fixture(t)
	fake.nextKey = "VO-2"

	const wikiLink = "[инстанс|https://jira.corp.com]"
	if _, err := tr.CreateTask("VO", tracker.TaskInput{
		Summary: "Category CRUD", Description: "Новый текст ребёнка.",
		DescriptionAppend: "## Исходная постановка\n\n" + wikiLink,
	}); err != nil {
		t.Fatalf("задача не создана: %v", err)
	}
	if len(fake.created) != 1 {
		t.Fatalf("создание не отправлено: %+v", fake.created)
	}
	desc, _ := fake.created[0].fields["description"].(string)
	if !strings.Contains(desc, wikiLink) {
		t.Errorf("DescriptionAppend изменён конвертером, ожидалась дословная подстрока %q в %q", wikiLink, desc)
	}
}

// TestCreateTaskJoinsEmptyDescriptionWithAppendCleanly — TaskInput допускает
// пустой Description с непустым DescriptionAppend (childDescription сегодня
// такую пару не производит, но контракт TaskInput её не запрещает), и голая
// конкатенация через "\n\n" оставляла бы висячий пустой отступ перед текстом.
func TestCreateTaskJoinsEmptyDescriptionWithAppendCleanly(t *testing.T) {
	tr, fake := fixture(t)
	fake.nextKey = "VO-2"

	if _, err := tr.CreateTask("VO", tracker.TaskInput{
		Summary: "Category CRUD", DescriptionAppend: "исходный текст",
	}); err != nil {
		t.Fatalf("задача не создана: %v", err)
	}
	desc, _ := fake.created[0].fields["description"].(string)
	if strings.HasPrefix(desc, "\n") || strings.HasPrefix(desc, " ") {
		t.Errorf("description начинается с висячего отступа: %q", desc)
	}
}

// FindByMarker ищет тем же JQL-поиском, что ListReady/List, но фильтрует
// по метке, а не по статусу.
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

// AddAttachment шлёт вложение multipart-запросом на .../attachments — REST v2
// не принимает вложения как JSON — и обязан приложить X-Atlassian-Token
// (проверяется в fakeJira.ServeHTTP), иначе настоящая JIRA отклонит запись.
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

// GetAttachment читает вложение по ссылке из метаданных GET /attachment/{id}:
// ссылка не под /rest/api/2 и не отдаёт JSON, поэтому нужен свой HTTP-вызов.
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

// TestGetAttachmentRejectsIDOutsideMarkerAlphabet — второй заслон, не
// только у ParseMarker (единственного сегодняшнего источника id): id
// склеивается прямо в REST-путь ("/attachment/"+id), и без проверки здесь
// значение вроде "../issue/VO-1" увело бы запрос на другой эндпойнт REST
// API, а не отказало бы явно (внешнее ревью, pr-converge раунд 3).
func TestGetAttachmentRejectsIDOutsideMarkerAlphabet(t *testing.T) {
	tr, fake := fixture(t)
	fake.hitPaths = nil

	if _, err := tr.GetAttachment("VO-1", "../issue/VO-1"); err == nil {
		t.Error("id с разделителями пути должен быть отвергнут заслоном, а не уйти в запрос")
	}
	if len(fake.hitPaths) != 0 {
		t.Errorf("запрос всё же ушёл на сервер: %v — заслон обязан отказать раньше, а не полагаться на 404 сервера", fake.hitPaths)
	}
}

// GetAttachment не должен слать базовую авторизацию инстанса на URL, который
// сервер назвал в content, но который не начинается с адреса самого инстанса.
// Сегодня content всегда свой (проверено выше), но если сервер когда-нибудь
// отдаст ссылку на внешнее хранилище (S3 и подобное), креды офиса туда
// утекать не должны.
func TestGetAttachmentDoesNotLeakCredentialsToForeignHost(t *testing.T) {
	tr, fake := fixture(t)

	var hit, gotAuth bool
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		if _, _, ok := r.BasicAuth(); ok {
			gotAuth = true
		}
		w.Write([]byte("нельзя"))
	}))
	defer evil.Close()

	id, err := tr.AddAttachment("VO-1", tracker.BySystem(), "split.json", []byte(`{"children":[]}`))
	if err != nil {
		t.Fatalf("вложение не отправлено: %v", err)
	}
	fake.contentHost = evil.URL

	if _, err := tr.GetAttachment("VO-1", id); err == nil {
		t.Error("ссылка на чужой хост должна быть отвергнута, а не прочитана молча")
	}
	if hit {
		t.Error("запрос ушёл на чужой хост вовсе — а не должен был уйти")
	}
	if gotAuth {
		t.Error("креды инстанса ушли на чужой хост")
	}
}

// TestEventCategoriesSurviveWikiRoundTrip — сбойная запись
// (internal/pipeline/splits.go, splitFailed) кладёт стабильную категорию
// первой строкой текста, а следом — свободный текст причины, который может
// нести тело ответа JIRA (квадратные скобки, как в типичном
// {"errorMessages":["..."]}) . Comment() прогоняет весь текст через wiki()
// на записи (jira.go:489), а обратного перевода нет — читается ровно то,
// что уехало. Если бы категория тоже несла спецсимволы wiki, дедупликация
// по первой строке (tracker.EventCategories) сравнивала бы разное на
// каждом проходе. Категория — простая русская проза без wiki-разметки,
// и обязана пережить круг без изменений; свободный текст причины со
// скобками — нет, и не должен участвовать в сравнении.
func TestEventCategoriesSurviveWikiRoundTrip(t *testing.T) {
	tr, _ := fixture(t)

	const category = "вложения родителя не скопированы"
	marker := tracker.Marker{RunID: "abcdef12", Role: "analyst", Event: tracker.EventSplitCreateFailed, ConfigSHA: "5bc6a3b0"}
	detail := `запрос отклонён: {"errorMessages":["вложение [10042] не найдено"],"errors":{}}`
	body := tracker.NoticeBody(marker, category+"\n"+detail)

	if err := tr.Comment("VO-1", tracker.BySystem(), body); err != nil {
		t.Fatalf("запись не отправлена: %v", err)
	}
	task, err := tr.Get("VO-1")
	if err != nil {
		t.Fatalf("задача не прочитана: %v", err)
	}

	categories := tracker.EventCategories(task.Comments, tracker.EventSplitCreateFailed)
	if !categories[category] {
		t.Errorf("категория после круга через wiki() не найдена среди %+v, ожидалась %q — дедупликация сравнивала бы разное на каждом проходе", categories, category)
	}
}

// TestGetAttachmentDoesNotLeakCredentialsViaUserinfoBypass — та же угроза,
// что и TestGetAttachmentDoesNotLeakCredentialsToForeignHost, но обходом
// через userinfo: strings.HasPrefix(url, BaseURL) считает совпадением
// строку "<BaseURL>@<чужой-хост>/…" — она и правда начинается с BaseURL
// как текст, но при разборе URL всё до "@" читается как userinfo, а
// настоящий хост — то, что после. Раздельный host/scheme нужен именно
// затем, чтобы отличать это от него.
func TestGetAttachmentDoesNotLeakCredentialsViaUserinfoBypass(t *testing.T) {
	tr, fake := fixture(t)

	var hit, gotAuth bool
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		if _, _, ok := r.BasicAuth(); ok {
			gotAuth = true
		}
		w.Write([]byte("нельзя"))
	}))
	defer evil.Close()

	id, err := tr.AddAttachment("VO-1", tracker.BySystem(), "split.json", []byte(`{"children":[]}`))
	if err != nil {
		t.Fatalf("вложение не отправлено: %v", err)
	}
	fake.contentHost = fake.baseURL + "@" + strings.TrimPrefix(evil.URL, "http://")

	if _, err := tr.GetAttachment("VO-1", id); err == nil {
		t.Error("ссылка с userinfo-обходом должна быть отвергнута, а не прочитана молча")
	}
	if hit {
		t.Error("запрос ушёл на чужой хост вовсе — а не должен был уйти")
	}
	if gotAuth {
		t.Error("креды инстанса ушли на чужой хост через userinfo-обход")
	}
}

// TestDownloadRejectsSameHostOutsideContextPath — инстанс за контекстным путём
// (base_url вида "https://host/jira", поддержано call()/upload() через
// BaseURL+apiPath+path) не должен доверять ссылке на тот же хост, но вне
// этого пути: внешнее ревью (pr-converge, раунд 1) нашло, что сведение
// проверки к голым scheme+host потеряло ограничение по пути, которое раньше
// давал strings.HasPrefix(url, BaseURL) целиком, и открыло SSRF на соседнее
// приложение того же хоста.
func TestDownloadRejectsSameHostOutsideContextPath(t *testing.T) {
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("не должно быть скачано"))
	}))
	defer evil.Close()

	t.Setenv("JIRA_USER", "office")
	t.Setenv("JIRA_PASSWORD", "секрет")
	tr, err := Open(Config{
		BaseURL:  evil.URL + "/jira",
		Auth:     Auth{Mode: "basic"},
		Accounts: Accounts{Default: Account{UserEnv: "JIRA_USER", SecretEnv: "JIRA_PASSWORD"}},
	})
	if err != nil {
		t.Fatalf("трекер не открыт: %v", err)
	}

	if _, err := tr.download(evil.URL + "/other-app/secure/attachment/1"); err == nil {
		t.Error("ссылка на тот же хост вне контекстного пути инстанса должна быть отвергнута")
	}
	// Ссылка внутри контекстного пути — по-прежнему легальна.
	if _, err := tr.download(evil.URL + "/jira/secure/attachment/1"); err != nil {
		t.Errorf("ссылка внутри контекстного пути отвергнута напрасно: %v", err)
	}
}

// LinkDependsOn шлёт POST /issueLink с типом связи из конфигурации: имя типа
// на реальном инстансе неизвестно заранее (Task 8 плана подтвердил его живьём),
// и здесь только форма и направление запроса. VO-1 «зависит от» VO-2 —
// эмпирически (живой JIRA Server 8.13, 2026-09-06) сервер читает связь через
// inward-сторону запроса, значит VO-1 обязан быть inwardIssue, а VO-2 —
// outwardIssue: см. развёрнутый комментарий у LinkDependsOn.
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
	if outward["key"] != "VO-2" || inward["key"] != "VO-1" {
		t.Errorf("направление связи %v/%v: VO-1 «зависит от» VO-2, значит VO-1 — inward, VO-2 — outward", outward, inward)
	}
}

// LinkDependsOn — мутация, и владение проверяется так же, как у Comment/AddAttachment.
func TestLinkDependsOnRequiresOwnership(t *testing.T) {
	tr, fake := fixture(t)
	fake.status = "In Progress"
	fake.runID = "прогон-1"
	fake.leaseUntil = "2026-08-17T12:30:00.000+0000"

	if err := tr.LinkDependsOn("VO-1", "VO-2", tracker.ByRun("чужой")); !errors.Is(err, tracker.ErrNotOwner) {
		t.Errorf("ошибка %v, ожидался ErrNotOwner", err)
	}
}

// Без depends_on_link в tracker.yaml собрать тип связи нечем, и LinkDependsOn
// обязан отказать сам, ясно и до сети: сервер здесь нарочно недостижим (порт
// закрыт сразу после старта) — если бы проверка не сработала раньше запроса,
// тест увидел бы отказ соединения, а не эту ошибку.
func TestLinkDependsOnFailsFastWithoutConfiguredLinkType(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	server.Close()

	t.Setenv("JIRA_USER", "office")
	t.Setenv("JIRA_PASSWORD", "секрет")

	tr, err := Open(Config{
		BaseURL:   server.URL,
		Auth:      Auth{Mode: "basic"},
		Accounts:  Accounts{Default: Account{UserEnv: "JIRA_USER", SecretEnv: "JIRA_PASSWORD"}},
		StatusMap: map[string]string{"Ready": "Ready"},
		Fields: Fields{
			Owner: "customfield_10001", RunID: "customfield_10002",
			LeaseUntil: "customfield_10003", Attempts: "customfield_10004",
		},
		HumanFlagLabel: "office-waits-human",
		// DependsOnLink нарочно не задан.
	})
	if err != nil {
		t.Fatalf("трекер не открыт: %v", err)
	}

	err = tr.LinkDependsOn("VO-1", "VO-2", tracker.BySystem())
	if err == nil {
		t.Fatal("связь создана без настроенного depends_on_link")
	}
	if !strings.Contains(err.Error(), "depends_on_link") || !strings.Contains(err.Error(), "tracker.yaml") {
		t.Errorf("ошибка не похожа на проверку depends_on_link (при недостижимом сервере реальная ошибка была бы про сеть): %v", err)
	}
}
