package jira

import (
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

	"github.com/kao73/virtual-office/tracker"
)

var now = time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

// fakeJira — минимальный JIRA: столько, сколько трогает трекер. Он не изображает
// сервер вообще, а отвечает на конкретные запросы и запоминает, что ему прислали,
// — чтобы проверять не только исход, но и форму запроса.
type fakeJira struct {
	t *testing.T

	status     string
	runID      string
	owner      string
	leaseUntil string
	attempts   float64
	labels     []string
	comments   []map[string]any

	// verifyRunID подменяет run_id при перечитывании после захвата: так выглядит
	// проигранная гонка, ради которой сверка и делается.
	verifyRunID string
	claimed     bool

	// transitionsTo — статусы, в которые задаче доступен переход. По умолчанию
	// все четыре: так настроен полигон, где вход в каждый статус глобальный.
	transitionsTo []string
	noIssues      bool // поиск ничего не находит

	lastUpdate   map[string]any // fields последнего PUT
	lastJQL      string
	lastLimit    int      // maxResults последнего поиска
	transitons   []string // имена статусов, в которые переводили
	commentPages int      // сколько раз спрашивали страницу комментариев
	fakeTotal    int      // ненулевой — сервер врёт про размер переписки

	lastUser string // учётка последнего запроса: под кем ходил трекер

	// badSearch заставляет поиск падать, а knownProject — единственный проект,
	// который сервер признаёт своим. Вместе они изображают заглушку
	// в projects.yaml: JQL по несуществующему проекту JIRA отвергает.
	badSearch    bool
	knownProject string
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
	return map[string]any{"key": "VO-1", "fields": fields}
}

func (f *fakeJira) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.lastUser, _, _ = r.BasicAuth()
	body := map[string]any{}
	if r.Body != nil {
		raw, _ := io.ReadAll(r.Body)
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

	case r.URL.Path == "/rest/api/2/search":
		f.lastJQL, _ = body["jql"].(string)
		if limit, ok := body["maxResults"].(float64); ok {
			f.lastLimit = int(limit)
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
		write(map[string]any{"issues": []any{f.issue()}})

	case strings.HasPrefix(r.URL.Path, "/rest/api/2/project/"):
		if key := strings.TrimPrefix(r.URL.Path, "/rest/api/2/project/"); key == f.knownProject {
			write(map[string]any{"key": key, "name": "Virtual Office"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
		write(map[string]any{"errorMessages": []string{"No project could be found with key"}})

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

	case r.URL.Path == "/rest/api/2/issue/VO-1/comment" && r.Method == http.MethodGet:
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

	t.Setenv("JIRA_USER", "office")
	t.Setenv("JIRA_PASSWORD", "секрет")

	tr, err := Open(Config{
		BaseURL:  server.URL,
		Auth:     Auth{Mode: "basic"},
		Accounts: Accounts{Default: Account{UserEnv: "JIRA_USER", SecretEnv: "JIRA_PASSWORD"}},
		Statuses: map[string]string{"Ready": "Ready", "InProgress": "In Progress", "Review": "Review", "Blocked": "Blocked"},
		Fields: Fields{
			Owner: "customfield_10001", RunID: "customfield_10002",
			LeaseUntil: "customfield_10003", Attempts: "customfield_10004",
		},
		HumanFlagLabel: "office-waits-human",
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

// Статусы JIRA переводятся в колонки графа и обратно: раннер работает
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
		t.Errorf("статус %q, ожидалась колонка InProgress", task.Status)
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

// Рабочая колонка — опция роли. Без неё захват записывает аренду и не трогает
// статус: «в работе» означает живую аренду в той же колонке, из которой роль
// читает. Перевод в пустой статус был бы бедой, а перевод «в тот же самый» —
// запросом, которого workflow может и не разрешить: переход Review → Review
// в JIRA существует не всегда.
func TestClaimWithoutWorkingStatusKeepsColumn(t *testing.T) {
	cases := map[string]string{
		"рабочей колонки у роли нет":                         "",
		"рабочая колонка та же, что и та, из которой читаем": "Review",
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
		Statuses:       map[string]string{"Ready": "Ready", "Review": "Review"},
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

// `ls` показывает доску, а не очередь: задачи в названных колонках как есть,
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

// tracker.yaml едет в прод как есть, и битый обнаружился бы первым же циклом
// против JIRA — то есть на живой доске.
func TestShippedTrackerConfigIsValid(t *testing.T) {
	root := filepath.Join("..", "..")

	cfg, err := LoadConfig(filepath.Join(root, TrackerFile))
	if err != nil {
		t.Fatalf("%s не загружен: %v", TrackerFile, err)
	}

	// Учётка роли, которой нет в графе, — опечатка: ходить под ней некому,
	// а в список агентов её имя попадёт и молча ничего не изменит.
	wf, err := tracker.LoadWorkflow(filepath.Join(root, tracker.WorkflowFile))
	if err != nil {
		t.Fatalf("граф не загружен: %v", err)
	}
	for role := range cfg.Accounts.Roles {
		if _, err := wf.Role(role); err != nil {
			t.Errorf("accounts.roles.%s: такой роли в графе нет", role)
		}
	}
}

// Учётки ролей — опция, общая — нет: без неё офису нечем ходить в трекер вовсе.
func TestLoadConfigRequiresDefaultAccount(t *testing.T) {
	body := `base_url: http://localhost
auth: { mode: basic }
accounts:
  roles:
    reviewer: { user_env: JIRA_REVIEWER_USER, secret_env: JIRA_REVIEWER_PASSWORD }
statuses: { Ready: Ready }
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

// Проект, описанный в projects.yaml, но неизвестный трекеру, роняет весь цикл:
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
