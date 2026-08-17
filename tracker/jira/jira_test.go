package jira

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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

	lastUpdate   map[string]any // fields последнего PUT
	lastJQL      string
	transitons   []string // имена статусов, в которые переводили
	commentPages int      // сколько раз спрашивали страницу комментариев
	fakeTotal    int      // ненулевой — сервер врёт про размер переписки

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
	}
	return map[string]any{"key": "VO-1", "fields": fields}
}

func (f *fakeJira) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
		if f.badSearch {
			w.WriteHeader(http.StatusBadRequest)
			write(map[string]any{"errorMessages": []string{"поиск не удался"}})
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
		write(map[string]any{"transitions": []any{
			map[string]any{"id": "11", "to": map[string]any{"name": "In Progress"}},
			map[string]any{"id": "21", "to": map[string]any{"name": "Review"}},
			map[string]any{"id": "31", "to": map[string]any{"name": "Blocked"}},
			map[string]any{"id": "41", "to": map[string]any{"name": "Ready"}},
		}})

	case r.URL.Path == "/rest/api/2/issue/VO-1/transitions" && r.Method == http.MethodPost:
		id, _ := body["transition"].(map[string]any)["id"].(string)
		switch id {
		case "11":
			f.status = "In Progress"
		case "21":
			f.status = "Review"
		case "31":
			f.status = "Blocked"
		case "41":
			f.status = "Ready"
		default:
			f.t.Errorf("неизвестный переход %q", id)
		}
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
	fake := &fakeJira{t: t, status: "Ready"}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	t.Setenv("JIRA_USER", "office")
	t.Setenv("JIRA_PASSWORD", "секрет")

	tr, err := Open(Config{
		BaseURL:  server.URL,
		Auth:     Auth{Mode: "basic", UserEnv: "JIRA_USER", SecretEnv: "JIRA_PASSWORD"},
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

func TestConfigRequiresCredential(t *testing.T) {
	t.Setenv("JIRA_USER", "office")
	t.Setenv("JIRA_PASSWORD", "")

	_, err := Open(Config{
		BaseURL: "http://localhost",
		Auth:    Auth{Mode: "basic", UserEnv: "JIRA_USER", SecretEnv: "JIRA_PASSWORD"},
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
		BaseURL: "http://localhost",
		Auth:    Auth{Mode: "pat", UserEnv: "JIRA_USER", SecretEnv: "JIRA_PASSWORD"},
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
