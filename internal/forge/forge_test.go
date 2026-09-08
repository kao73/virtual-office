package forge

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Владелец и имя репозитория выводятся из repo_url, а не задаются отдельным
// ключом: два места для одного факта однажды разойдутся.
func TestParseRepo(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://github.com/kao73/expense-tracker.git", "kao73/expense-tracker"},
		{"https://github.com/kao73/expense-tracker", "kao73/expense-tracker"},
		{"git@github.com:kao73/expense-tracker.git", "kao73/expense-tracker"},
		{"ssh://git@github.com/kao73/expense-tracker.git", "kao73/expense-tracker"},
	}
	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			repo, err := ParseRepo(tc.url)
			if err != nil {
				t.Fatalf("repo_url не разобран: %v", err)
			}
			if repo.String() != tc.want {
				t.Errorf("получено %q, ожидалось %q", repo, tc.want)
			}
		})
	}

	// Локальный bare-репозиторий полигона — это не forge, и притворяться им
	// не должен: проект без forge живёт вырожденным проходом.
	for _, bad := range []string{"/Users/kao/polygons/client.git", "", "https://github.com/"} {
		if repo, err := ParseRepo(bad); err == nil {
			t.Errorf("из %q выведен репозиторий %q", bad, repo)
		}
	}
}

// Адрес pull request приходит офису не от forge, а из комментария тикета:
// его могли поправить руками или он пришёл из чужого офиса. Прежде чем слить
// такой PR, офис сверяет его репозиторий со своим repo_url — во всех трёх
// формах адреса и с локальным путём полигона заодно.
func TestSameRepo(t *testing.T) {
	const pr = "https://github.com/kao73/client/pull/42"
	same := []string{
		"https://github.com/kao73/client.git",
		"https://github.com/kao73/client",
		"git@github.com:kao73/client.git",
		"ssh://git@github.com/kao73/client.git",
		"https://github.com/KAO73/Client.git", // GitHub к регистру не чувствителен
		"/Users/kao/polygons/kao73/client.git",
	}
	for _, repoURL := range same {
		if !SameRepo(repoURL, pr) {
			t.Errorf("%q и %q сочтены разными репозиториями", repoURL, pr)
		}
	}

	other := []struct{ repoURL, prURL string }{
		{"https://github.com/kao73/client.git", "https://github.com/чужой/client/pull/42"},
		{"https://github.com/kao73/client.git", "https://github.com/kao73/другой/pull/42"},
		{"https://github.com/kao73/client.git", "не адрес"},
		{"https://github.com/kao73/client.git", ""},
		{"", pr},
		// Хвост совпадает буквой, а не сегментом: `my-client` — не `client`.
		{"https://github.com/kao73/my-client.git", pr},
	}
	for _, tc := range other {
		if SameRepo(tc.repoURL, tc.prURL) {
			t.Errorf("%q и %q сочтены одним репозиторием", tc.repoURL, tc.prURL)
		}
	}
}

func TestParsePRURL(t *testing.T) {
	repo, number, err := parsePRURL("https://github.com/kao73/client/pull/42")
	if err != nil {
		t.Fatalf("адрес не разобран: %v", err)
	}
	if repo.String() != "kao73/client" || number != 42 {
		t.Errorf("получено %s#%d, ожидалось kao73/client#42", repo, number)
	}
	for _, bad := range []string{"https://github.com/kao73/client", "не адрес", "https://github.com/kao73/client/pull/xx"} {
		if _, _, err := parsePRURL(bad); err == nil {
			t.Errorf("разобран мусор: %q", bad)
		}
	}
}

// github поднимает поддельный GitHub и forge, ходящий в него.
func github(t *testing.T, handler http.HandlerFunc) *GitHub {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &GitHub{
		api:    server.URL,
		token:  "тестовый",
		client: server.Client(),
		repos:  map[string]Repo{"EXP": {Owner: "kao73", Name: "expense-tracker"}},
	}
}

func TestGitHubOpensPullRequest(t *testing.T) {
	var got map[string]string
	g := github(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/kao73/expense-tracker/pulls" || r.Method != http.MethodPost {
			t.Errorf("запрос ушёл не туда: %s %s", r.Method, r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer тестовый" {
			t.Errorf("запрос не подписан токеном: %q", auth)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"html_url":"https://github.com/kao73/expense-tracker/pull/3"}`))
	})

	url, err := g.OpenPR("EXP", "agent/EXP-1", "main", "EXP-1 Заголовок", "Тело")
	if err != nil {
		t.Fatalf("pull request не открыт: %v", err)
	}
	if url != "https://github.com/kao73/expense-tracker/pull/3" {
		t.Errorf("адрес %q", url)
	}
	if got["head"] != "agent/EXP-1" || got["base"] != "main" || got["title"] != "EXP-1 Заголовок" {
		t.Errorf("запрос собран не так: %+v", got)
	}
}

// Слитый pull request у GitHub тоже closed: различает их поле merged.
func TestGitHubPRState(t *testing.T) {
	cases := []struct {
		answer string
		want   State
	}{
		{`{"state":"open","merged":false}`, Open},
		{`{"state":"closed","merged":true}`, Merged},
		{`{"state":"closed","merged":false}`, Closed},
	}
	for _, tc := range cases {
		t.Run(string(tc.want), func(t *testing.T) {
			g := github(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/repos/kao73/expense-tracker/pulls/3" {
					t.Errorf("запрос ушёл не туда: %s", r.URL.Path)
				}
				_, _ = w.Write([]byte(tc.answer))
			})
			got, err := g.PRState("https://github.com/kao73/expense-tracker/pull/3")
			if err != nil {
				t.Fatalf("состояние не выяснено: %v", err)
			}
			if got != tc.want {
				t.Errorf("состояние %q, ожидалось %q", got, tc.want)
			}
		})
	}
}

// Отказ сервера и сбой связи — разные вещи, и разводить их обязательно: за отказ
// задача уходит к человеку, а за сбой её двигать нельзя.
func TestGitHubSeparatesRefusalFromFailure(t *testing.T) {
	refusing := github(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"No commits between main and agent/EXP-1"}`))
	})
	_, err := refusing.OpenPR("EXP", "agent/EXP-1", "main", "заголовок", "тело")
	if !errors.Is(err, ErrRefused) {
		t.Errorf("отказ 422 не распознан как отказ: %v", err)
	}
	if !strings.Contains(err.Error(), "No commits") {
		t.Errorf("причина отказа потеряна: %v", err)
	}

	broken := github(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	_, err = broken.OpenPR("EXP", "agent/EXP-1", "main", "заголовок", "тело")
	if err == nil {
		t.Fatal("сбой сервера прошёл незамеченным")
	}
	if errors.Is(err, ErrRefused) {
		t.Errorf("сбой связи принят за отказ: %v", err)
	}

	// Проект, за которым нет репозитория GitHub, — тоже отказ, а не сбой:
	// ждать, что он появится, нечего.
	if _, err := refusing.OpenPR("OFF", "agent/OFF-1", "master", "з", "т"); !errors.Is(err, ErrRefused) {
		t.Errorf("неизвестный проект не распознан как отказ: %v", err)
	}
}

// Merge сливает pull request через PUT .../pulls/{number}/merge, с зашитым
// merge_method: "merge" — сохраняет историю ветки задачи как есть.
func TestGitHubMerge(t *testing.T) {
	var gotMethod, gotPath string
	var gotPayload map[string]string
	g := github(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotPayload)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"merged":true}`))
	})

	if err := g.Merge("https://github.com/kao73/expense-tracker/pull/3"); err != nil {
		t.Fatalf("слияние не выполнено: %v", err)
	}
	if gotMethod != http.MethodPut || gotPath != "/repos/kao73/expense-tracker/pulls/3/merge" {
		t.Errorf("запрос ушёл не туда: %s %s", gotMethod, gotPath)
	}
	if gotPayload["merge_method"] != "merge" {
		t.Errorf("merge_method = %q, ожидалось merge", gotPayload["merge_method"])
	}
}

// 405/409 — GitHub отказывается сливать (не мержится, не прошли required
// checks и т.п.): это окончательный ответ, а не сбой связи.
func TestGitHubMergeRefusal(t *testing.T) {
	for _, code := range []int{http.StatusMethodNotAllowed, http.StatusConflict} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			g := github(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(code)
				_, _ = w.Write([]byte(`{"message":"отказ"}`))
			})
			err := g.Merge("https://github.com/kao73/expense-tracker/pull/3")
			if !errors.Is(err, ErrRefused) {
				t.Errorf("код %d не распознан как отказ: %v", code, err)
			}
		})
	}
}

// Сбой связи/5xx — задачу за него двигать нельзя, следующий проход
// попробует снова.
func TestGitHubMergeTransportFailure(t *testing.T) {
	g := github(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	err := g.Merge("https://github.com/kao73/expense-tracker/pull/3")
	if err == nil {
		t.Fatal("сбой сервера прошёл незамеченным")
	}
	if errors.Is(err, ErrRefused) {
		t.Errorf("сбой связи принят за отказ: %v", err)
	}
}
