package forge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Kind — имя реализации в projects.local.yaml: `forge: github`.
const Kind = "github"

// TokenEnv — переменная с токеном. Тот же, которым раннер пушит: право писать
// в репозиторий и право открыть в нём PR — одно право.
//
// Секретов в репозитории нет и не будет: токен живёт только в окружении
// и в командную строку не попадает (её видно в `ps`).
const TokenEnv = "GITHUB_TOKEN"

// API — корень REST API по умолчанию. Сам адрес хранится полем структуры:
// тесты подставляют свой сервер, а GitHub Enterprise живёт по другому адресу.
const API = "https://api.github.com"

// mergeMethod — merge commit, не squash и не rebase: сохраняет всю историю
// ветки задачи как есть, ничего не сжимает. Не вынесено в конфиг — YAGNI,
// пока не спросили.
const mergeMethod = "merge"

// GitHub — pull request через REST API v3.
type GitHub struct {
	api    string
	token  string
	client *http.Client
	repos  map[string]Repo // ключ проекта → репозиторий
}

var _ Forge = (*GitHub)(nil)

// NewGitHub готовит forge для названных проектов.
//
// Репозитории разбираются здесь и разом: узнать о неразобранном repo_url
// в середине прохода, уже пообещав задаче PR, было бы поздно.
func NewGitHub(repoURLs map[string]string) (*GitHub, error) {
	token := os.Getenv(TokenEnv)
	if token == "" {
		return nil, fmt.Errorf("%s не задан: без него офис не откроет pull request", TokenEnv)
	}

	repos := make(map[string]Repo, len(repoURLs))
	for project, url := range repoURLs {
		repo, err := ParseRepo(url)
		if err != nil {
			return nil, fmt.Errorf("проект %s: %w", project, err)
		}
		repos[project] = repo
	}
	return &GitHub{
		api:    API,
		token:  token,
		client: &http.Client{Timeout: 30 * time.Second},
		repos:  repos,
	}, nil
}

// OpenPR открывает pull request ветки задачи в названную базовую ветку.
func (g *GitHub) OpenPR(project, branch, base, title, body string) (string, error) {
	repo, known := g.repos[project]
	if !known {
		return "", fmt.Errorf("%w: проект %s не описан как репозиторий GitHub", ErrRefused, project)
	}

	payload, err := json.Marshal(map[string]string{
		"title": title, "head": branch, "base": base, "body": body,
	})
	if err != nil {
		return "", err
	}

	var answer struct {
		URL string `json:"html_url"`
	}
	if err := g.do(http.MethodPost, "/repos/"+repo.String()+"/pulls", payload, &answer); err != nil {
		return "", err
	}
	if answer.URL == "" {
		return "", fmt.Errorf("GitHub открыл pull request в %s, но не назвал его адреса", repo)
	}
	return answer.URL, nil
}

// PRState отвечает, что стало с pull request.
//
// Состояний у GitHub два — open и closed, — а нужных офису три: слитый PR
// тоже closed. Различает их поле merged.
func (g *GitHub) PRState(url string) (State, error) {
	repo, number, err := parsePRURL(url)
	if err != nil {
		return "", err
	}

	var answer struct {
		State  string `json:"state"`
		Merged bool   `json:"merged"`
	}
	path := fmt.Sprintf("/repos/%s/pulls/%d", repo, number)
	if err := g.do(http.MethodGet, path, nil, &answer); err != nil {
		return "", err
	}
	switch {
	case answer.Merged:
		return Merged, nil
	case answer.State == "open":
		return Open, nil
	default:
		return Closed, nil
	}
}

// pendingMergeStates — mergeable_state, о которых GitHub ещё не решил или
// которые может снять сам факт, что время прошло: обязательные проверки
// досчитаются, необязательные тоже, база подтянется. Это не отказ — счётчик
// limits.max_merge_refusals тратить нельзя.
var pendingMergeStates = map[string]bool{
	"unknown":  true, // GitHub ещё считает mergeable — тот же смысл, что null у mergeable
	"blocked":  true, // обязательные проверки или ревью не завершены
	"unstable": true, // необязательные проверки ещё идут
	"behind":   true, // база в понимании GitHub ушла вперёд
	"":         true, // поле не пришло вовсе (старый GitHub Enterprise, урезанный прокси)
	// — не считать это отказом: пустое неотличимо от «ещё не посчитано».
}

// Merge сливает pull request.
//
// Перед самим слиянием — отдельный GET за mergeable_state: у merge-эндпоинта
// нет способа сказать, окончателен 405 или обязательные проверки просто ещё
// не завершились (REST API этого не документирует), а GitHub считает
// mergeable_state асинхронно и отдаёт его отдельным полем PR. Не разведя эти
// два случая, "проверки идут" тратило бы max_merge_refusals наравне
// с настоящим отказом — а на дефолтном тике в 2 минуты (max_merge_refusals: 3)
// это исчерпало бы предел за 4-6 минут, раньше, чем успевает пройти обычный CI:
// заявленная в design.md выгода branch protection не пережила бы собственный
// предел. Найдено внешним ревью PR, добавившего auto_merge.
func (g *GitHub) Merge(url string) error {
	repo, number, err := parsePRURL(url)
	if err != nil {
		return err
	}

	state, err := g.mergeableState(repo, number)
	if err != nil {
		return err
	}
	if pendingMergeStates[state] {
		// ErrNotReady, не голая ошибка: значение неотличимо от «застряло
		// навсегда» (упавшая обязательная проверка, недостающее обязательное
		// ревью) — задача не должна виснуть без счёта и без следа в тикете,
		// просто терпимее, чем к настоящему отказу (limits.max_merge_pending,
		// internal/pipeline/prpass.go). Найдено внешним ревью, round 2:
		// первая версия этой ветки просто логировала и не считала вовсе.
		return fmt.Errorf("%w: pull request %s пока не готов к слиянию (mergeable_state=%q): "+
			"похоже, обязательные проверки или ревью ещё не завершены — проверим на следующем тике",
			ErrNotReady, url, state)
	}
	if state != "clean" && state != "has_hooks" {
		// dirty (настоящий конфликт), draft (черновик — GitHub не мержит его
		// через API) или незнакомое значение: не глотать — раннер обязан
		// показать человеку то, чего не понимает, а не решать за него
		// (тот же принцип, что у Limit.State в бюджете, DESIGN.md §2.7).
		return fmt.Errorf("%w: pull request %s не готов к слиянию (mergeable_state=%s)",
			ErrRefused, url, state)
	}

	payload, err := json.Marshal(map[string]string{"merge_method": mergeMethod})
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/repos/%s/pulls/%d/merge", repo, number)
	// 409 здесь — "Head branch was modified": гонка между GET-проверкой выше
	// и этим PUT (кто-то успел передвинуть голову или базу за миллисекунды
	// между ними), а не окончательный отказ — retryable отдаёт его как сбой
	// связи, а не ErrRefused. 405 остаётся отказом: он же придёт, если
	// mergeable_state успел стать dirty/blocked за то же окно, и это тот же
	// смысл, что и выше.
	return g.do(http.MethodPut, path, payload, nil, http.StatusConflict)
}

// mergeableState — что GitHub думает о слиянии PR (значения — см. Merge
// выше). Отдельный запрос: сам merge-эндпоинт этого не сообщает.
func (g *GitHub) mergeableState(repo Repo, number int) (string, error) {
	var answer struct {
		MergeableState string `json:"mergeable_state"`
	}
	path := fmt.Sprintf("/repos/%s/pulls/%d", repo, number)
	if err := g.do(http.MethodGet, path, nil, &answer); err != nil {
		return "", err
	}
	return answer.MergeableState, nil
}

// do выполняет запрос и разбирает ответ.
//
// Отказ сервера (4xx) отделяется от сбоя связи: первый — окончательный ответ
// о задаче, второй — беда обвязки, и задачу за него двигать нельзя.
//
// retryable называет коды из тех же 4xx, которые здесь всё равно не отказ:
// у Merge 409 значит гонку между собственной GET-проверкой и этим же
// запросом, а не решение forge. Список пуст у OpenPR/PRState/mergeableState —
// им это не грозило ни разу, и раздувать их отказы не для чего.
func (g *GitHub) do(method, path string, payload []byte, into any, retryable ...int) error {
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequest(method, g.api+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("GitHub %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("GitHub %s %s: ответ не прочитан: %w", method, path, err)
	}
	switch {
	case slices.Contains(retryable, resp.StatusCode):
		return fmt.Errorf("GitHub %s %s → %d (переходное состояние, не отказ): %s",
			method, path, resp.StatusCode, snippet(body))
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return fmt.Errorf("%w: GitHub %s %s → %d: %s", ErrRefused, method, path, resp.StatusCode, snippet(body))
	case resp.StatusCode >= 300:
		return fmt.Errorf("GitHub %s %s → %d: %s", method, path, resp.StatusCode, snippet(body))
	}
	if into == nil {
		return nil
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("GitHub %s %s: ответ не разобран: %w", method, path, err)
	}
	return nil
}

// parsePRURL разбирает адрес вида https://github.com/owner/name/pull/12.
func parsePRURL(url string) (Repo, int, error) {
	parts := urlParts(url)
	// host / owner / name / pull / number
	if len(parts) < 5 || parts[3] != "pull" {
		return Repo{}, 0, fmt.Errorf("адрес pull request не разобран: %q", url)
	}
	number, err := strconv.Atoi(parts[4])
	if err != nil {
		return Repo{}, 0, fmt.Errorf("адрес pull request не разобран: %q", url)
	}
	return Repo{Owner: parts[1], Name: parts[2]}, number, nil
}

func snippet(body []byte) string {
	const limit = 400
	text := strings.TrimSpace(string(body))
	if len(text) > limit {
		return text[:limit] + "…"
	}
	return text
}
