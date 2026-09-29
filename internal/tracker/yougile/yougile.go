// Package yougile — трекер поверх YouGile REST API v2 (https://yougile.com/api-v2,
// спека — https://yougile.com/api-json).
//
// Ходим в настоящий REST, а не через yougile-mcp: обёртка не пробрасывает ни
// apiData, ни фильтры листинга. Клиентской библиотеки нет по той же причине,
// что у jira: нужен контроль над каждым запросом.
//
// Статус задачи — её колонка на доске (Config.ColumnIDs): в YouGile задача
// живёт ровно на одной доске и в одной колонке, и принцип «статус — не колонка»
// из docs/DESIGN.md, написанный для JIRA с её многими досками, здесь опоры не
// имеет (docs/openspec/changes/archive/2026-09-28-yougile-adapter-core/design.md,
// Decisions; дальше просто «design.md»).
//
// Аренда, счётчик попыток, флаг «ждёт человека» и метки живут в apiData
// задачи — свободном JSON-поле (lease.go).
//
// Пакет пока не реализует tracker.Tracker целиком: LinkDependsOn и вложения
// добавит yougile-dependencies-attachments, и только тогда здесь появится
// проверка `var _ tracker.Tracker = (*Tracker)(nil)`.
package yougile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/kao73/virtual-office/internal/tracker"
)

// apiPrefix — префикс REST API; BaseURL — корень хоста.
const apiPrefix = "/api-v2"

// pageLimit — сколько элементов просить за страницу: максимум, который
// принимает API.
const pageLimit = 1000

// maxPages — потолок страниц на один листинг: сервер, который никогда не
// говорит «дальше пусто», не должен крутить цикл вечно.
const maxPages = 100

// Config — подключение и раскладка статусов по колонкам. Загрузчика из файла
// пока нет — его добавит yougile-wiring-and-docs; ключ API приходит из
// окружения на стороне вызывающего и сюда попадает уже значением.
type Config struct {
	// BaseURL — корень хоста, https://yougile.com. Поле, а не константа, —
	// чтобы тесты направляли трекер на httptest.
	BaseURL string
	// APIKey — ключ API. Не пишется ни в ошибки, ни в лог.
	APIKey string
	// ProjectID — id проекта YouGile. Один Tracker — один проект.
	ProjectID string
	// ColumnIDs — статус графа → id колонки. Колонки заводит человек; адаптер
	// их не создаёт, а на Open сверяет, что они есть.
	ColumnIDs map[string]string
	// CreateStatus — статус графа, в колонку которого CreateTask кладёт новую
	// задачу. Пусто — CreateTask откажет при вызове.
	CreateStatus string
}

// Tracker — трекер поверх YouGile.
type Tracker struct {
	cfg    Config
	client *http.Client

	// columnStatus — обратная карта ColumnIDs: id колонки → статус графа.
	columnStatus map[string]string
	// columns — id всех колонок всех досок проекта, прочитанные один раз на Open.
	columns []string

	mu    sync.Mutex
	users map[string]string // id пользователя → email, кэш авторов комментариев

	// Now — часы раннера: аренду сверяем ими, а не серверными.
	Now func() time.Time
	// Logf — куда адаптер сообщает о том, что стерпел, а не вернул ошибкой:
	// листинги пропускают карточку с нечитаемыми данными офиса (status.go,
	// collect). По умолчанию log.Printf; yougile-wiring-and-docs направит
	// его в лог раннера.
	Logf func(format string, args ...any)
}

// Open готовит трекер: проверяет конфигурацию и ничего на сервере не создаёт.
func Open(cfg Config) (*Tracker, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("yougile: base_url не задан")
	}
	base, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("yougile: base_url не разобран: %w", err)
	}
	if base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("yougile: base_url=%q: нет схемы или хоста (пример: https://yougile.com)", cfg.BaseURL)
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	// Адрес документации API кончается на /api-v2, и его легко вставить как есть.
	// Запросы ушли бы на /api-v2/api-v2/…, а 404 выдал бы себя за «нет проекта».
	if strings.HasSuffix(cfg.BaseURL, apiPrefix) {
		return nil, fmt.Errorf("yougile: base_url=%q: %s адаптер добавляет сам, укажи корень хоста (пример: https://yougile.com)",
			cfg.BaseURL, apiPrefix)
	}
	if cfg.APIKey == "" {
		return nil, errors.New("yougile: ключ API не задан")
	}
	if cfg.ProjectID == "" {
		return nil, errors.New("yougile: id проекта не задан")
	}
	if len(cfg.ColumnIDs) == 0 {
		return nil, errors.New("yougile: карта статус → колонка пуста")
	}

	// Обратная карта: строим один раз и падаем на неоднозначности сразу, а не
	// посреди первого цикла. Обход в порядке ключей — ошибка одна и та же при
	// каждом запуске.
	columnStatus := make(map[string]string, len(cfg.ColumnIDs))
	for _, status := range slices.Sorted(maps.Keys(cfg.ColumnIDs)) {
		id := cfg.ColumnIDs[status]
		if id == "" {
			return nil, fmt.Errorf("yougile: у статуса %q пустой id колонки", status)
		}
		if before, found := columnStatus[id]; found {
			return nil, fmt.Errorf("yougile: колонка %q сопоставлена и с %q, и с %q", id, before, status)
		}
		columnStatus[id] = status
	}
	if cfg.CreateStatus != "" {
		if _, ok := cfg.ColumnIDs[cfg.CreateStatus]; !ok {
			return nil, fmt.Errorf("yougile: create_status %q не входит в карту статус → колонка", cfg.CreateStatus)
		}
	}

	t := &Tracker{
		cfg:          cfg,
		client:       &http.Client{Timeout: 30 * time.Second},
		columnStatus: columnStatus,
		users:        map[string]string{},
		Now:          time.Now,
		Logf:         log.Printf,
	}
	if err := t.loadColumns(); err != nil {
		return nil, err
	}
	return t, nil
}

// checkProject — Tracker обслуживает ровно один проект YouGile.
func (t *Tracker) checkProject(project string) error {
	if project != t.cfg.ProjectID {
		return fmt.Errorf("%w: %q (этот трекер обслуживает проект YouGile %q)",
			tracker.ErrNoProject, project, t.cfg.ProjectID)
	}
	return nil
}

// call выполняет один запрос к API — без повторов: повтор делает следующий
// тик раннера (design.md, Decisions). Тело ответа при ошибке попадает в текст
// ошибки: YouGile объясняет отказ в нём.
func (t *Tracker) call(method, path string, query url.Values, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("запрос не сериализован: %w", err)
		}
		body = bytes.NewReader(raw)
	}

	target := t.cfg.BaseURL + apiPrefix + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequest(method, target, body)
	if err != nil {
		return fmt.Errorf("запрос не собран: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+t.cfg.APIKey)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	raw, readErr := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return statusError(method, path, resp.StatusCode, raw)
	}
	// Оборванное тело — запрос не удался: что сервер успел сделать, не знаем,
	// и повтор — дело следующего тика.
	if readErr != nil {
		return fmt.Errorf("%s %s: ответ не дочитан: %w", method, path, readErr)
	}
	if out == nil {
		return nil
	}
	// Пустой ответ там, где ждали тело, — не пустой список: иначе листинг
	// молча не увидел бы ни задач, ни истёкших аренд.
	if len(bytes.TrimSpace(raw)) == 0 {
		return fmt.Errorf("%s %s: пустой ответ (%d), а ожидалось тело", method, path, resp.StatusCode)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s %s: ответ не разобран: %w\n%s", method, path, err, snippet(raw))
	}
	return nil
}

// statusError различает беды, которые лечатся по-разному. Ключ API сюда
// не попадает никогда: в текст идут только метод, путь, код и тело ответа.
func statusError(method, path string, code int, body []byte) error {
	switch code {
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s %s (404)", tracker.ErrNotFound, method, path)
	case http.StatusUnauthorized:
		return fmt.Errorf("%s %s: ключ API не принят (401): %s", method, path, snippet(body))
	case http.StatusForbidden:
		return fmt.Errorf("%s %s: доступ запрещён (403), ключу не хватает прав: %s", method, path, snippet(body))
	case http.StatusTooManyRequests:
		return fmt.Errorf("%s %s: превышен rate limit YouGile (429, не более 50 запросов в минуту на компанию): %s",
			method, path, snippet(body))
	default:
		return fmt.Errorf("%s %s: %d: %s", method, path, code, snippet(body))
	}
}

// snippet обрезает тело ответа до смысла — по символам, а не по байтам:
// русский текст ошибки, оборванный посреди руны, стал бы битым UTF-8.
func snippet(body []byte) string {
	const limit = 400
	// Идём по рунам прямо по байтам: HTML-страница прокси бывает мегабайтами,
	// а наружу уйдёт limit символов (range по string(body) скопировал бы тело
	// целиком). Битый байт DecodeRune отдаёт как U+FFFD — его и пишем, чтобы
	// чужая кодировка не протекла в текст ошибки.
	body = bytes.TrimSpace(body)
	var out strings.Builder
	for n := 0; len(body) > 0; n++ {
		if n == limit {
			out.WriteString("…")
			break
		}
		r, size := utf8.DecodeRune(body)
		out.WriteRune(r)
		body = body[size:]
	}
	return out.String()
}

// page — страница любого листинга API v2: paging + content.
type page[T any] struct {
	Paging struct {
		Next bool `json:"next"`
	} `json:"paging"`
	Content []T `json:"content"`
}

// listAll проходит листинг до конца по paging.next. Короткая страница конца
// не означает: у сервера бывает свой потолок ниже запрошенного limit.
func listAll[T any](t *Tracker, path string, query url.Values) ([]T, error) {
	q := url.Values{}
	maps.Copy(q, query)
	var all []T
	offset := 0
	for range maxPages {
		q.Set("limit", strconv.Itoa(pageLimit))
		q.Set("offset", strconv.Itoa(offset))
		var p page[T]
		if err := t.call(http.MethodGet, path, q, nil, &p); err != nil {
			return nil, err
		}
		all = append(all, p.Content...)
		if !p.Paging.Next || len(p.Content) == 0 {
			return all, nil
		}
		offset += len(p.Content)
	}
	return nil, fmt.Errorf("GET %s: больше %d страниц, сервер не подтверждает конец списка", path, maxPages)
}

// userDTO — пользователь YouGile; нужны id и email.
type userDTO struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

// Whoami — email пользователя, чей ключ API у трекера. Авторов комментариев
// Get отдаёт тоже email'ами (comment.go), так что сравнение идёт по учётке,
// а не по тексту.
func (t *Tracker) Whoami() (string, error) {
	var me userDTO
	if err := t.call(http.MethodGet, "/users/me", nil, nil, &me); err != nil {
		return "", err
	}
	if me.Email == "" {
		return "", errors.New("yougile: /users/me не назвал email — сравнивать авторов комментариев не с чем")
	}
	// Свои комментарии в переписке есть почти всегда — автора уже знаем.
	if me.ID != "" {
		t.mu.Lock()
		t.users[me.ID] = me.Email
		t.mu.Unlock()
	}
	return me.Email, nil
}
