// Package jira — трекер поверх JIRA Server REST API v2.
//
// Версия API выбрана намеренно: v2 понимают и Server, и Cloud, а v3 с его ADF —
// только Cloud. Целевая версия у нас Server 8.13, и всё здесь проверено на ней.
//
// Клиентских библиотек нет и не будет: их API уходит вперёд Server-версий,
// а нам нужен контроль над каждым запросом — в JIRA слишком много мест, где
// ответ сервера важнее документации.
package jira

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kao73/virtual-office/internal/tracker"
)

// dateLayout — формат даты и времени, который принимает и отдаёт JIRA:
// миллисекунды обязательны, смещение без двоеточия.
const dateLayout = "2006-01-02T15:04:05.000-0700"

// apiPath — префикс REST API.
const apiPath = "/rest/api/2"

// pageSize — сколько комментариев просить за раз. Раннер режет их сам, поэтому
// нужны все: страницы крутятся до конца переписки.
const pageSize = 100

// searchPage — сколько задач просить у поиска. Из кандидатов берут первого
// годного, поэтому предел один на все очереди.
const searchPage = 50

// Config — подключение и раскладка полей. Живёт в ${OFFICE_HOME}/tracker.yaml.
type Config struct {
	BaseURL string `yaml:"base_url"`
	Auth    Auth   `yaml:"auth"`

	// Accounts — под кем офис ходит в трекер: общая учётка и, если заведены,
	// учётки ролей.
	Accounts Accounts `yaml:"accounts"`

	// AlsoAgents — чужая автоматизация: боты, чья проза тоже не человеческая.
	// Единственный список, который пишется руками, — вывести его неоткуда.
	AlsoAgents []string `yaml:"also_agents"`

	// StatusMap — статус графа → имя статуса в JIRA. Ключ — узел workflow.yaml,
	// значение — как этот узел зовут на инстансе: «In Progress» с пробелом
	// остаётся здесь. Имя ключа не `statuses`: там, где слово значит и узел графа,
	// и его перевод, одно однажды прочитают вместо другого.
	StatusMap map[string]string `yaml:"status_map"`

	Fields Fields `yaml:"fields"`

	// HumanFlagLabel — метка «ждёт человека». Метка, а не поле: её видно
	// в списке задач и она не требует настройки экранов.
	HumanFlagLabel string `yaml:"human_flag_label"`

	// IssueType — тип задачи для CreateTask. JIRA v2 требует issuetype
	// в теле POST /issue. Пусто — код берёт "Task" (issueType()): на
	// большинстве инстансов он есть из коробки, и заставлять заполнять
	// поле ради дефолтного значения незачем.
	IssueType string `yaml:"issue_type"`

	// DependsOnLink — имя типа связи "зависит от" на инстансе
	// (LinkDependsOn, POST /issueLink). Не входит в обязательные поля
	// LoadConfig: нужен он одному-единственному узкому методу, а не каждому
	// обращению к трекеру, и отказ здесь ронял бы Comment, Transition и Get
	// из-за поля, которое им не нужно. Пусто — LinkDependsOn откажет сам,
	// в момент вызова. Заводится или подбирается на полигоне — см. живую
	// проверку, Task 8 плана
	// docs/superpowers/plans/2026-09-05-split-autocreate-tickets.md.
	DependsOnLink string `yaml:"depends_on_link"`
}

// Auth — способ авторизации. Он один на все учётки: как ходить — свойство
// инстанса, а под кем — свойство роли.
type Auth struct {
	Mode string `yaml:"mode"` // только basic
}

// Accounts — учётки офиса. Общая обязательна, учётки ролей — опция: раннер
// различает роли по маркеру в первой строке комментария, а не по автору,
// и одна учётка на всех агентов ничего не ломает.
type Accounts struct {
	Default Account            `yaml:"default"`
	Roles   map[string]Account `yaml:"roles"`
}

// Account — одна учётка. В файле лежат только имена переменных: секреты живут
// в окружении, и имя пользователя — тоже, чтобы обе половины креда были рядом.
type Account struct {
	UserEnv   string `yaml:"user_env"`
	SecretEnv string `yaml:"secret_env"`
}

// For — учётка роли, если она заведена, иначе общая.
func (a Accounts) For(role string) Account {
	if account, found := a.Roles[role]; found {
		return account
	}
	return a.Default
}

// AgentAccounts — имена всех учёток офиса плюс чужая автоматизация. Список
// выводится, а не перечисляется: забудь однажды дописать сюда роль — и её
// собственный отчёт станет для раннера голосом человека, а задача вернётся
// в очередь по кругу.
//
// Пустое имя — ошибка, а не пустяк: именно так учётка и выпала бы из списка.
func (c Config) AgentAccounts() ([]string, error) {
	accounts := map[string]Account{"default": c.Accounts.Default}
	for role, account := range c.Accounts.Roles {
		accounts[role] = account
	}

	names := slices.Clone(c.AlsoAgents)
	var errs []error
	for _, role := range slices.Sorted(maps.Keys(accounts)) {
		name := os.Getenv(accounts[role].UserEnv)
		if name == "" {
			errs = append(errs, fmt.Errorf("accounts.%s: в %s нет имени учётки — её комментарии сойдут за слова человека",
				role, accounts[role].UserEnv))
			continue
		}
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names, errors.Join(errs...)
}

// Fields — идентификаторы кастомных полей аренды (customfield_NNNNN).
type Fields struct {
	Owner      string `yaml:"agent_owner"`
	RunID      string `yaml:"run_id"`
	LeaseUntil string `yaml:"lease_until"`
	Attempts   string `yaml:"attempts"`
}

// Tracker — трекер поверх JIRA.
type Tracker struct {
	cfg    Config
	client *http.Client
	user   string
	secret string
	graph  map[string]string // имя статуса в JIRA → статус графа
	// baseURL — cfg.BaseURL, разобранный один раз при открытии: download()
	// сверяет по нему scheme+host у ссылок, которые называет сам сервер.
	baseURL *url.URL

	// Now — часы раннера. Аренду сверяем ими, а не серверными: сервер считает
	// now() в своей зоне, и полагаться на совпадение не стоит.
	Now func() time.Time
}

var _ tracker.Tracker = (*Tracker)(nil)

// Open готовит трекер под общей учёткой офиса: под ней идут reap и разбор ответов
// человека — работа, у которой роли нет.
func Open(cfg Config) (*Tracker, error) { return OpenAs(cfg, "") }

// OpenAs готовит трекер под учёткой роли, а если своей у неё нет — под общей.
func OpenAs(cfg Config, role string) (*Tracker, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("base_url не задан")
	}
	base, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("base_url не разобран: %w", err)
	}
	// "jira.example.com" (без схемы) url.Parse разбирает без ошибки, но
	// с пустым Host — весь текст уходит в Path. Без проверки здесь download()
	// на каждой ссылке молча отвергал бы её как чужую (пустой Host никогда
	// не совпадёт с настоящим), и причина не была бы видна до первого вложения.
	if base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("base_url=%q: нет схемы или хоста (пример: https://jira.example.com)", cfg.BaseURL)
	}
	// Режим один, и это не упущение. Персональные токены появились в Jira Server
	// с 8.14, а целевая версия — 8.13: проверить их не на чем, а необъявленное
	// лучше необслуживаемого. Когда инстанс с токенами появится, добавится
	// заголовок Authorization: Bearer — см. docs/notes/jira-api.md.
	if cfg.Auth.Mode != "basic" {
		return nil, fmt.Errorf("auth.mode=%q: реализован только basic", cfg.Auth.Mode)
	}

	account := cfg.Accounts.For(role)
	secret := os.Getenv(account.SecretEnv)
	if secret == "" {
		return nil, fmt.Errorf("в %s нет креда JIRA: секреты живут только в окружении", account.SecretEnv)
	}

	// Обратная карта статусов: её строим один раз и падаем на неоднозначности
	// сразу, а не на середине первого цикла.
	graph := make(map[string]string, len(cfg.StatusMap))
	for status, name := range cfg.StatusMap {
		if before, found := graph[name]; found {
			return nil, fmt.Errorf("статус %q сопоставлен и с %q, и с %q", name, before, status)
		}
		graph[name] = status
	}

	return &Tracker{
		cfg:     cfg,
		client:  &http.Client{Timeout: 30 * time.Second},
		user:    os.Getenv(account.UserEnv),
		secret:  secret,
		graph:   graph,
		baseURL: base,
		Now:     time.Now,
	}, nil
}

// CheckAccount сверяет имя учётки из конфигурации с тем, кем нас видит сервер.
//
// Имя берётся из окружения, а кред — оттуда же, но проверяет их разное: имя идёт
// в список агентских учёток, а кредом подписывается запрос. Разойдись они —
// офис перестанет узнавать собственные комментарии, примет их за слова человека
// и начнёт возвращать в очередь задачи, которые сам же и заблокировал. Ошибка
// молчаливая и заметная не сразу, поэтому сверка делается на сборке, один раз.
func (t *Tracker) CheckAccount() error {
	server, err := t.Whoami()
	if err != nil {
		return fmt.Errorf("учётка не сверена: %w", err)
	}
	if server != t.user {
		return fmt.Errorf("учётка %q не совпадает с той, кем нас видит JIRA (%q): "+
			"комментарии офиса сойдут за слова человека", t.user, server)
	}
	return nil
}

// Whoami — учётка, под которой ходит раннер.
func (t *Tracker) Whoami() (string, error) {
	var me struct {
		Name string `json:"name"`
	}
	if err := t.call(http.MethodGet, "/myself", nil, &me); err != nil {
		return "", err
	}
	return me.Name, nil
}

// ListReady — кандидаты в статусе проекта: без живой аренды, отсортированы.
//
// JQL отбирает грубо, а решает раннер: сервер сравнивает время своими часами,
// и полагаться на совпадение с нашими нельзя.
func (t *Tracker) ListReady(project, status string) ([]tracker.TaskRef, error) {
	jql := fmt.Sprintf(`project = %q AND status = %q AND (%s IS EMPTY OR %s <= now()) ORDER BY priority DESC, created ASC`,
		project, t.jiraStatus(status), t.jqlField(t.cfg.Fields.LeaseUntil), t.jqlField(t.cfg.Fields.LeaseUntil))

	return t.searchProject(project, jql, searchPage, func(task tracker.Task) bool { return !task.LeaseAlive(t.Now()) })
}

// List — задачи проекта в названных статусах, как есть.
//
// Аренду он не отбрасывает, в отличие от ListReady: этот список показывают
// человеку, а «кто работает прямо сейчас» — первое, что он в нём ищет.
func (t *Tracker) List(project string, statuses []string) ([]tracker.TaskRef, error) {
	if len(statuses) == 0 {
		return nil, nil // спрашивать «задачи ни в одном статусе» незачем
	}

	names := make([]string, 0, len(statuses))
	for _, status := range statuses {
		names = append(names, strconv.Quote(t.jiraStatus(status)))
	}
	jql := fmt.Sprintf(`project = %q AND status IN (%s) ORDER BY created ASC`,
		project, strings.Join(names, ", "))

	return t.searchProject(project, jql, searchPage, func(tracker.Task) bool { return true })
}

// ListExpired — задачи с истёкшей арендой: сырьё для reaper.
func (t *Tracker) ListExpired(project string, now time.Time) ([]tracker.TaskRef, error) {
	jql := fmt.Sprintf(`project = %q AND %s IS NOT EMPTY AND %s <= now() ORDER BY created ASC`,
		project, t.jqlField(t.cfg.Fields.RunID), t.jqlField(t.cfg.Fields.LeaseUntil))

	return t.searchProject(project, jql, searchPage, func(task tracker.Task) bool {
		return task.RunID != "" && !task.LeaseAlive(now)
	})
}

// CheckWorkflow выясняет, допускает ли workflow проекта вход в рабочий статус
// из него самого. Такой вход — глобальный переход «в этот статус из любого» —
// и есть лазейка, из-за которой захват не становится CAS даже с перечитыванием:
// см. Claim и «Сколько раннеров на проект» в контракте трекера.
//
// Переходы JIRA показывает только у конкретной задачи и только из её текущего
// статуса — спросить workflow целиком нельзя. Поэтому нужен образец: любая
// задача, уже находящаяся в рабочем статусе. Её отсутствие — не «всё хорошо»,
// а «проверить было не на чем», и отличать одно от другого обязан вызывающий.
func (t *Tracker) CheckWorkflow(project, workingStatus string) (tracker.WorkflowCheck, error) {
	working := t.jiraStatus(workingStatus)
	jql := fmt.Sprintf(`project = %q AND status = %q`, project, working)

	// Одна задача, а не очередь: это проверка настройки, а не поиск работы.
	refs, err := t.searchProject(project, jql, 1, func(tracker.Task) bool { return true })
	if err != nil || len(refs) == 0 {
		return tracker.WorkflowCheck{}, err
	}

	check := tracker.WorkflowCheck{Sample: refs[0].Key}
	options, err := t.transitions(check.Sample)
	if err != nil {
		return tracker.WorkflowCheck{}, err
	}
	for _, option := range options {
		if option.to == working {
			check.SelfEntry = true
			break
		}
	}
	return check, nil
}

// searchProject — поиск по проекту, отличающий незнакомый проект от прочих бед.
//
// JQL по несуществующему проекту JIRA отвергает четырёхсоткой, и без разбора
// такой отказ роняет весь цикл: раннер обходит проекты по порядку и на первом же
// отказе бросает остальные. Одна протухшая строка в projects.yaml останавливала бы
// работу по всем проектам сразу — поймано живой проверкой.
//
// Разбираем не по тексту ошибки: он зависит от версии сервера и однажды сменится
// молча. Вместо этого спрашиваем сам проект — и только когда поиск уже упал,
// так что в счастливом пути лишнего запроса не появляется.
func (t *Tracker) searchProject(project, jql string, limit int, keep func(tracker.Task) bool) ([]tracker.TaskRef, error) {
	refs, err := t.search(jql, limit, keep)
	if err == nil {
		return refs, nil
	}
	if known, checkErr := t.projectExists(project); checkErr == nil && !known {
		return nil, fmt.Errorf("%w: %s", tracker.ErrNoProject, project)
	}
	return nil, err
}

// projectExists спрашивает у сервера, знает ли он такой проект.
func (t *Tracker) projectExists(project string) (bool, error) {
	err := t.call(http.MethodGet, "/project/"+project, nil, nil)
	switch {
	case errors.Is(err, tracker.ErrNotFound):
		return false, nil
	case err != nil:
		return false, err
	default:
		return true, nil
	}
}

// Get — задача целиком, включая все комментарии.
func (t *Tracker) Get(key string) (tracker.Task, error) {
	var raw issue
	if err := t.call(http.MethodGet, "/issue/"+key, nil, &raw); err != nil {
		return tracker.Task{}, err
	}
	task := t.toTask(raw)

	comments, err := t.comments(key)
	if err != nil {
		return tracker.Task{}, err
	}
	task.Comments = comments
	return task, nil
}

// Claim — захват задачи.
//
// Атомарности в JIRA нет: записать поля и перевести статус — два запроса, между
// которыми может вклиниться конкурент. Поэтому после записи задача перечитывается,
// и владельцем считается тот, чей run_id остался в поле.
//
// Сверка закрывает половину гонки — ту, где нашу запись затёрли: мы честно
// проигрываем. Зеркальную не закрывает. Конкурент, прочитавший задачу свободной
// до нашей записи, пишет свои поля уже после нашей сверки; его собственная сверка
// видит его же run_id — и захват удаётся обоим.
//
// Спотыкаться второму негде: переходы заводятся глобальными («Allow all statuses
// to transition to this one», docs/notes/jira-setup.md), а значит переход в рабочий
// статус доступен и из него самого — проверено на полигоне, задаче в Review виден
// переход в Review. Двое уходят работать, а рабочая папка и ветка ключуются
// по ключу задачи, не по run_id (workspace.Ensure), — один каталог на двоих.
// Renew одного из них скоро вернёт ErrNotOwner, но правят они одни файлы уже
// сейчас, и выглядит это не гонкой захвата, а испорченным worktree.
//
// Нужны для этого два процесса: tick последователен и сам с собой не соревнуется,
// так что дотянуться до гонки может разве что ручной tick поверх работающего loop.
// Настоящий CAS в JIRA есть только в workflow — переход, доступный ровно из одного
// статуса, исполняется один раз, — но он требует снять глобальный вход у рабочего
// статуса, то есть предъявить требования к workflow проекта-клиента.
func (t *Tracker) Claim(req tracker.ClaimRequest) error {
	task, err := t.Get(req.Key)
	if err != nil {
		return err
	}
	switch {
	case task.Status != req.ExpectStatus:
		return fmt.Errorf("%w: %s в статусе %q, а захват шёл из %q",
			tracker.ErrClaimLost, req.Key, task.Status, req.ExpectStatus)
	case task.LeaseAlive(t.Now()):
		return fmt.Errorf("%w: %s арендована прогоном %s до %s",
			tracker.ErrClaimLost, req.Key, task.RunID, task.LeaseUntil.Format(time.RFC3339))
	}

	if err := t.update(req.Key, map[string]any{
		t.cfg.Fields.Owner:      req.Owner,
		t.cfg.Fields.RunID:      req.RunID,
		t.cfg.Fields.LeaseUntil: req.LeaseUntil.Format(dateLayout),
	}); err != nil {
		return err
	}
	// Статус меняется, только если роли есть куда переводить задачу. Рабочий
	// статус необязателен: без него «в работе» означает живую аренду в том же
	// статусе, из которого роль читает. Перевод «в тот же самый статус» вдобавок
	// не всегда существует — перехода Review → Review в workflow может не быть
	// вовсе, и захват падал бы на ровном месте.
	if req.WorkingStatus != "" && req.WorkingStatus != task.Status {
		if err := t.transition(req.Key, req.WorkingStatus); err != nil {
			return err
		}
	}

	fresh, err := t.Get(req.Key)
	if err != nil {
		return err
	}
	if fresh.RunID != req.RunID {
		return fmt.Errorf("%w: после захвата %s владеет %s", tracker.ErrClaimLost, req.Key, fresh.RunID)
	}
	return nil
}

// Renew продлевает свою живую аренду.
func (t *Tracker) Renew(key, runID string, leaseUntil time.Time) error {
	if _, err := t.owned(key, tracker.ByRun(runID)); err != nil {
		return err
	}
	return t.update(key, map[string]any{t.cfg.Fields.LeaseUntil: leaseUntil.Format(dateLayout)})
}

// Release снимает аренду, не трогая статус.
func (t *Tracker) Release(key string, by tracker.Actor) error {
	if _, err := t.owned(key, by); err != nil {
		return err
	}
	// Поля чистятся значением null. Пустая строка под `IS EMPTY` в JQL тоже
	// подходит — проверено на 8.13, — но в интерфейсе оставляет поле «заполненным
	// пустотой», и разбирать такое глазами неприятно.
	return t.update(key, map[string]any{
		t.cfg.Fields.Owner:      nil,
		t.cfg.Fields.RunID:      nil,
		t.cfg.Fields.LeaseUntil: nil,
	})
}

// Transition двигает задачу по графу.
func (t *Tracker) Transition(key string, by tracker.Actor, toStatus string) error {
	if _, err := t.owned(key, by); err != nil {
		return err
	}
	return t.transition(key, toStatus)
}

// Comment пишет комментарий обычным текстом: v2 не знает ADF, и это к лучшему —
// тело едет строкой и той же строкой читается обратно.
//
// Офис пишет markdown, а Server понимает wiki-разметку, поэтому тело переводится
// здесь, на записи (wiki.go). Машинные куски записи перевод обходит: строку-маркер
// и **тело** раздела «Вопросы» читает раннер, и они обязаны доехать буква в букву.
// Заголовок раздела — исключение и переводится; почему — в wiki.go.
func (t *Tracker) Comment(key string, by tracker.Actor, body string) error {
	if _, err := t.owned(key, by); err != nil {
		return err
	}
	return t.call(http.MethodPost, "/issue/"+key+"/comment", map[string]any{"body": wiki(body)}, nil)
}

// SetHumanFlag выставляет или снимает метку ожидания человека.
func (t *Tracker) SetHumanFlag(key string, by tracker.Actor, on bool) error {
	task, err := t.owned(key, by)
	if err != nil {
		return err
	}

	labels := make([]string, 0, len(task.Labels)+1)
	for _, label := range task.Labels {
		if label != t.cfg.HumanFlagLabel {
			labels = append(labels, label)
		}
	}
	if on {
		labels = append(labels, t.cfg.HumanFlagLabel)
	}
	return t.update(key, map[string]any{"labels": labels})
}

// SetAttempts записывает счётчик попыток.
func (t *Tracker) SetAttempts(key string, by tracker.Actor, n int) error {
	if _, err := t.owned(key, by); err != nil {
		return err
	}
	return t.update(key, map[string]any{t.cfg.Fields.Attempts: n})
}

// issueType — тип задачи для CreateTask, с дефолтом.
func (t *Tracker) issueType() string {
	if t.cfg.IssueType != "" {
		return t.cfg.IssueType
	}
	return "Task"
}

// CreateTask заводит новую задачу. Статус создания решает workflow проекта
// на инстансе — POST /issue не умеет задать статус, и эта реализация не
// пытается: см. живую проверку (Task 8 плана
// docs/superpowers/plans/2026-09-05-split-autocreate-tickets.md).
func (t *Tracker) CreateTask(project string, input tracker.TaskInput) (tracker.TaskRef, error) {
	var created struct {
		Key string `json:"key"`
	}
	description := wiki(input.Description)
	if input.DescriptionAppend != "" {
		// Без wiki(): DescriptionAppend уже в чужой разметке (см. доккомент
		// TaskInput.DescriptionAppend), повторный прогон исказил бы её.
		// Разделитель — только когда есть что разделять: пустой Description
		// с непустым DescriptionAppend не должен оставлять висячий отступ.
		if description == "" {
			description = input.DescriptionAppend
		} else {
			description += "\n\n" + input.DescriptionAppend
		}
	}
	fields := map[string]any{
		"project":     map[string]any{"key": project},
		"summary":     input.Summary,
		"description": description,
		"issuetype":   map[string]any{"name": t.issueType()},
	}
	if len(input.Labels) > 0 {
		fields["labels"] = input.Labels
	}
	if err := t.call(http.MethodPost, "/issue", map[string]any{"fields": fields}, &created); err != nil {
		return tracker.TaskRef{}, err
	}

	task, err := t.Get(created.Key)
	if err != nil {
		return tracker.TaskRef{}, err
	}
	return task.Ref(), nil
}

// FindByMarker — задачи проекта с данной меткой, тем же JQL-поиском, что
// ListReady/List.
func (t *Tracker) FindByMarker(project, marker string) ([]tracker.TaskRef, error) {
	jql := fmt.Sprintf(`project = %q AND labels = %q`, project, marker)
	return t.searchProject(project, jql, searchPage, func(tracker.Task) bool { return true })
}

// upload выполняет multipart-запрос: вложения не JSON, и t.call им не
// годится. X-Atlassian-Token обязателен — без него JIRA отклонит запись
// вложения так же, как отклоняет её без basic-авторизации (см. call).
func (t *Tracker) upload(path, filename string, data []byte) ([]byte, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		return nil, fmt.Errorf("вложение не собрано: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return nil, fmt.Errorf("вложение не собрано: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("вложение не собрано: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, t.cfg.BaseURL+apiPath+path, &body)
	if err != nil {
		return nil, fmt.Errorf("запрос не собран: %w", err)
	}
	req.SetBasicAuth(t.user, t.secret)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("X-Atlassian-Token", "no-check")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", http.MethodPost, path, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, statusError(http.MethodPost, path, resp.StatusCode, raw)
	}
	return raw, nil
}

// download читает вложение по прямой ссылке из ответа GET /attachment/{id}:
// она не под /rest/api/2 и не отдаёт JSON, поэтому не годится t.call.
//
// Ссылку называет сам сервер, и доверять ей безоговорочно нельзя: если он
// когда-нибудь отдаст адрес внешнего хранилища (S3 и подобное) вместо себя
// самого, безусловный SetBasicAuth ниже отправил бы туда креды инстанса.
// Редирект с другого хоста Go сам обрежет Authorization начиная с 1.8 —
// это защита от прямо названного чужого адреса, не от редиректа.
//
// Сравнение — по разобранным scheme+host, не по префиксу строки: голый
// strings.HasPrefix пропустил бы "<BaseURL>@чужой-хост/…" (до "@" — не хост,
// а userinfo) или "<BaseURL>.чужой-хост/…" (другой домен с тем же началом) —
// в обоих случаях итоговый хост запроса не совпадает с инстансом, хотя
// строка с ним совпадает.
//
// Путь сверяется отдельно, когда у BaseURL он не пустой и не корень: инстанс
// за контекстным путём (base_url вида "https://host/jira" — call()/upload()
// уже строят из него BaseURL+apiPath+path) делит хост с чем угодно ещё на
// этом же сервере, и голого совпадения scheme+host было бы мало — оно
// пропустило бы "https://host/другое-приложение" как "свой" адрес. Обе части
// сравниваются через path.Clean, чтобы ".." в ссылке не обошёл проверку.
func (t *Tracker) download(dl string) ([]byte, error) {
	u, err := url.Parse(dl)
	if err != nil {
		return nil, fmt.Errorf("вложение по ссылке %s: не разобрано: %w", dl, err)
	}
	foreign := fmt.Errorf("вложение по ссылке %s: сервер назвал адрес не своего инстанса (%s), запрос не отправлен",
		dl, t.cfg.BaseURL)
	if u.Scheme != t.baseURL.Scheme || u.Host != t.baseURL.Host {
		return nil, foreign
	}
	if t.baseURL.Path != "" && t.baseURL.Path != "/" {
		base := path.Clean(t.baseURL.Path)
		got := path.Clean(u.Path)
		if got != base && !strings.HasPrefix(got, base+"/") {
			return nil, foreign
		}
	}

	req, err := http.NewRequest(http.MethodGet, dl, nil)
	if err != nil {
		return nil, fmt.Errorf("запрос вложения не собран: %w", err)
	}
	req.SetBasicAuth(t.user, t.secret)
	req.Header.Set("X-Atlassian-Token", "no-check")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", dl, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("вложение не прочитано: %w", err)
	}
	if resp.StatusCode >= 300 {
		return nil, statusError(http.MethodGet, dl, resp.StatusCode, raw)
	}
	return raw, nil
}

// AddAttachment сохраняет сырые данные вложением. Ответ JIRA на создание —
// массив из одного элемента; возвращается его id.
func (t *Tracker) AddAttachment(key string, by tracker.Actor, name string, data []byte) (string, error) {
	if _, err := t.owned(key, by); err != nil {
		return "", err
	}

	raw, err := t.upload("/issue/"+key+"/attachments", name, data)
	if err != nil {
		return "", err
	}

	var created []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		return "", fmt.Errorf("вложение %s: ответ не разобран: %w\n%s", key, err, snippet(raw))
	}
	if len(created) == 0 {
		return "", fmt.Errorf("вложение %s: сервер не назвал идентификатор", key)
	}
	return created[0].ID, nil
}

// GetAttachment читает вложение обратно. key не используется: идентификаторы
// вложений в JIRA глобальны — параметр входит в контракт ради файлового
// трекера, которому путь по ключу задачи и нужен.
func (t *Tracker) GetAttachment(_, id string) ([]byte, error) {
	// Второй заслон, не только у ParseMarker (единственного сегодняшнего
	// источника id): id склеивается прямо в REST-путь, и без проверки
	// здесь значение вроде "../issue/VO-1" ушло бы на сервер как есть —
	// а если тот сам нормализует ".." при маршрутизации (многие веб-
	// фреймворки так делают), запрос попал бы на совсем другой эндпойнт
	// (внешнее ревью, pr-converge раунд 3).
	if !tracker.ValidAttachmentID(id) {
		return nil, fmt.Errorf("%w: вложение %s", tracker.ErrNotFound, id)
	}
	var meta struct {
		Content string `json:"content"`
	}
	if err := t.call(http.MethodGet, "/attachment/"+id, nil, &meta); err != nil {
		return nil, err
	}
	return t.download(meta.Content)
}

// LinkDependsOn связывает key с dependsOnKey типом связи из конфигурации.
//
// dependsOnKey — исходящая (outward) сторона запроса, key — входящая
// (inward): эмпирически проверено на живом JIRA Server 8.13
// (2026-09-06, throwaway-тикеты на полигоне, тип Blocks для независимой
// сверки, отчёт — docs/notes/analyst-task-splitting.md, «Живой прогон,
// нашедший разворот direction») — сервер описывает связь через ТУ сторону,
// которая передана как inwardIssue, используя outward-текст типа, а не
// наоборот. Иными словами: результат POST {outwardIssue: O, inwardIssue: I}
// читается как «I <outward-текст> O», не «O <outward-текст> I». Прежняя
// версия (outwardIssue: key, inwardIssue: dependsOnKey) была развёрнута —
// прошла собственные юнит-тесты (они проверяли только форму запроса, не
// его смысл на реальном сервере) и не была поймана живой проверкой Task 8,
// которая тоже сверяла только факт создания связи, не её видимое
// направление на обеих карточках.
//
// Не идемпотентна на стороне клиента (в отличие от mock.LinkDependsOn,
// которая проверяет slices.Contains перед записью) — повтор после сбоя
// (completeSplit ретраит весь путь целиком) полагается на то, что сам
// JIRA Server дедуплицирует одинаковый POST /issueLink; проверено
// эмпирически на паре одноразовых тикетов (docs/notes/analyst-task-
// splitting.md), не гарантировано контрактом REST API.
func (t *Tracker) LinkDependsOn(key, dependsOnKey string, by tracker.Actor) error {
	if t.cfg.DependsOnLink == "" {
		return fmt.Errorf("depends_on_link не задан в tracker.yaml: связь %s → %s не создана", key, dependsOnKey)
	}
	if _, err := t.owned(key, by); err != nil {
		return err
	}
	return t.call(http.MethodPost, "/issueLink", map[string]any{
		"type":         map[string]any{"name": t.cfg.DependsOnLink},
		"outwardIssue": map[string]any{"key": dependsOnKey},
		"inwardIssue":  map[string]any{"key": key},
	}, nil)
}

// owned читает задачу и проверяет право актора её менять. Правило общее для всех
// трекеров и живёт в пакете tracker: разъехавшись, реализации дали бы гонку.
func (t *Tracker) owned(key string, by tracker.Actor) (tracker.Task, error) {
	task, err := t.Get(key)
	if err != nil {
		return tracker.Task{}, err
	}
	if err := tracker.CheckOwner(task, by, t.Now()); err != nil {
		return tracker.Task{}, err
	}
	return task, nil
}

// transition находит переход по имени целевого статуса и исполняет его.
func (t *Tracker) transition(key, toStatus string) error {
	want := t.jiraStatus(toStatus)

	options, err := t.transitions(key)
	if err != nil {
		return err
	}

	available := make([]string, 0, len(options))
	for _, option := range options {
		if option.to == want {
			return t.call(http.MethodPost, "/issue/"+key+"/transitions",
				map[string]any{"transition": map[string]any{"id": option.id}}, nil)
		}
		available = append(available, option.to)
	}
	return fmt.Errorf("из текущего статуса %s нет перехода в %q: доступны %s — проверь workflow (docs/notes/jira-setup.md)",
		key, want, strings.Join(available, ", "))
}

// transitionOption — доступный задаче переход: идентификатор и куда он ведёт.
type transitionOption struct {
	id string
	to string
}

// transitions — переходы, доступные задаче из её текущего статуса.
//
// Идентификаторы переходов не хранятся в конфигурации намеренно: они зависят
// от workflow и от текущего статуса задачи, а спрашивать их у сервера — один
// лишний запрос, зато конфигурация не врёт после правки workflow.
func (t *Tracker) transitions(key string) ([]transitionOption, error) {
	var list struct {
		Transitions []struct {
			ID string `json:"id"`
			To struct {
				Name string `json:"name"`
			} `json:"to"`
		} `json:"transitions"`
	}
	if err := t.call(http.MethodGet, "/issue/"+key+"/transitions", nil, &list); err != nil {
		return nil, err
	}

	options := make([]transitionOption, 0, len(list.Transitions))
	for _, raw := range list.Transitions {
		options = append(options, transitionOption{id: raw.ID, to: raw.To.Name})
	}
	return options, nil
}

// search выполняет JQL и отбирает то, что прошло проверку раннера.
//
// Страница одна, и это предел на будущее, а не насовсем. ListReady от него не
// страдает: из кандидатов берут первого годного, а не весь список. Reap разберёт
// остаток следующим заходом. Когда очередь одного статуса перестанет влезать
// в пятьдесят, страницы крутятся по startAt — как в comments.
func (t *Tracker) search(jql string, limit int, keep func(tracker.Task) bool) ([]tracker.TaskRef, error) {
	var result struct {
		Issues []issue `json:"issues"`
	}
	body := map[string]any{"jql": jql, "maxResults": limit, "fields": t.searchFields()}
	if err := t.call(http.MethodPost, "/search", body, &result); err != nil {
		return nil, fmt.Errorf("поиск задач не удался (%s): %w", jql, err)
	}

	var refs []tracker.TaskRef
	for _, raw := range result.Issues {
		task := t.toTask(raw)
		if !keep(task) {
			continue
		}
		ref := task.Ref()
		// Время последней правки нужно только спискам, поэтому оно не в Task:
		// решению раннера оно не помогает, а человеку показывает возраст задачи.
		if updated, err := time.Parse(dateLayout, text(raw.Fields["updated"])); err == nil {
			ref.Updated = updated
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

func (t *Tracker) searchFields() []string {
	return []string{
		"summary", "description", "status", "project", "labels", "updated", "issuelinks",
		t.cfg.Fields.Owner, t.cfg.Fields.RunID, t.cfg.Fields.LeaseUntil, t.cfg.Fields.Attempts,
	}
}

// comments тянет всю переписку страницами: раннер режет её сам по маркеру.
//
// Нужны именно все. Раннер отсчитывает хвост от последнего маркера своей роли,
// то есть от конца истории, а сортировка по возрастанию отдаёт её начало: одной
// страницей он на длинном тикете получил бы ровно не то, ради чего его будили,
// и ответ человека пропал бы молча.
//
// Шаг цикла считается по числу пришедших комментариев, а не по запрошенному
// размеру страницы: JIRA вправе отдать меньше, чем просили, — предел выдачи
// на сервере свой. Пустая страница обрывает цикл: соври сервер про total,
// круг иначе стал бы вечным.
func (t *Tracker) comments(key string) ([]tracker.Comment, error) {
	var comments []tracker.Comment

	for start := 0; ; {
		var page struct {
			Comments []struct {
				ID     string `json:"id"`
				Body   string `json:"body"`
				Author struct {
					Name string `json:"name"`
				} `json:"author"`
				Created string `json:"created"`
			} `json:"comments"`
			Total int `json:"total"`
		}

		path := fmt.Sprintf("/issue/%s/comment?startAt=%d&maxResults=%d&orderBy=created", key, start, pageSize)
		if err := t.call(http.MethodGet, path, nil, &page); err != nil {
			return nil, err
		}

		for _, raw := range page.Comments {
			created, _ := time.Parse(dateLayout, raw.Created)
			comments = append(comments, tracker.Comment{
				ID: raw.ID, Author: raw.Author.Name, Created: created, Body: raw.Body,
			})
		}

		start += len(page.Comments)
		if len(page.Comments) == 0 || start >= page.Total {
			return comments, nil
		}
	}
}

// issue — ответ JIRA о задаче. Кастомные поля лежат в общей карте: их имена
// приходят из конфигурации, и структурой их не описать.
type issue struct {
	Key    string         `json:"key"`
	Fields map[string]any `json:"fields"`
}

func (t *Tracker) toTask(raw issue) tracker.Task {
	fields := raw.Fields
	task := tracker.Task{
		Key:         raw.Key,
		Summary:     text(fields["summary"]),
		Description: text(fields["description"]),
		Owner:       text(fields[t.cfg.Fields.Owner]),
		RunID:       text(fields[t.cfg.Fields.RunID]),
	}

	if status, ok := fields["status"].(map[string]any); ok {
		task.Status = t.graphStatus(text(status["name"]))
	}
	if project, ok := fields["project"].(map[string]any); ok {
		task.Project = text(project["key"])
	}
	if lease, err := time.Parse(dateLayout, text(fields[t.cfg.Fields.LeaseUntil])); err == nil {
		task.LeaseUntil = lease
	}
	if attempts, ok := fields[t.cfg.Fields.Attempts].(float64); ok {
		task.Attempts = int(attempts)
	}
	if labels, ok := fields["labels"].([]any); ok {
		for _, label := range labels {
			name := text(label)
			if name == t.cfg.HumanFlagLabel {
				task.HumanFlag = true
				continue
			}
			task.Labels = append(task.Labels, name)
		}
	}
	if attachments, ok := fields["attachment"].([]any); ok {
		for _, raw := range attachments {
			meta, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			task.Attachments = append(task.Attachments, tracker.AttachmentRef{
				ID: text(meta["id"]), Name: text(meta["filename"]),
			})
		}
	}
	if links, ok := fields["issuelinks"].([]any); ok {
		for _, raw := range links {
			link, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			typ, _ := link["type"].(map[string]any)
			if text(typ["name"]) != t.cfg.DependsOnLink {
				continue
			}
			// outwardIssue заполнен только у той стороны связи, что была
			// записана как outwardIssue при POST /issueLink — а
			// LinkDependsOn пишет туда dependsOnKey (см. его доккомент
			// про развёрнутое направление). inwardIssue здесь —
			// обратная связь ("кто зависит от меня"), её не читаем:
			// DependsOn — это "от кого зависит эта задача", не "кто
			// зависит от неё".
			if out, ok := link["outwardIssue"].(map[string]any); ok {
				task.DependsOn = append(task.DependsOn, text(out["key"]))
			}
		}
	}
	return task
}

// jiraStatus — имя статуса на инстансе по статусу графа.
func (t *Tracker) jiraStatus(status string) string {
	if name, found := t.cfg.StatusMap[status]; found {
		return name
	}
	return status
}

// graphStatus — статус графа по имени статуса на инстансе.
func (t *Tracker) graphStatus(name string) string {
	if status, found := t.graph[name]; found {
		return status
	}
	return name
}

// jqlField переводит customfield_10003 в форму cf[10003]: по имени поля JQL тоже
// умеет, но имена бывают неоднозначными, а идентификатор — нет.
func (t *Tracker) jqlField(field string) string {
	return "cf[" + strings.TrimPrefix(field, "customfield_") + "]"
}

func (t *Tracker) update(key string, fields map[string]any) error {
	return t.call(http.MethodPut, "/issue/"+key, map[string]any{"fields": fields}, nil)
}

// call выполняет запрос к API. Тело ответа при ошибке возвращается целиком:
// JIRA объясняет отказ в нём, и терять это объяснение — значит гадать.
func (t *Tracker) call(method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("запрос не сериализован: %w", err)
		}
		body = bytes.NewReader(raw)
	}

	req, err := http.NewRequest(method, t.cfg.BaseURL+apiPath+path, body)
	if err != nil {
		return fmt.Errorf("запрос не собран: %w", err)
	}
	req.SetBasicAuth(t.user, t.secret)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// Заголовок отключает форму входа: без него JIRA на неверный кред отвечает
	// 200 со страницей логина, и разбор превращается в гадание.
	req.Header.Set("X-Atlassian-Token", "no-check")

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return statusError(method, path, resp.StatusCode, raw)
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s %s: ответ не разобран: %w\n%s", method, path, err, snippet(raw))
	}
	return nil
}

// statusError различает беды, которые лечатся по-разному: 404 — задачи нет,
// 401 и 403 — кред или права, 409 — конкурент успел первым.
func statusError(method, path string, code int, body []byte) error {
	switch code {
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s", tracker.ErrNotFound, path)
	case http.StatusConflict:
		return fmt.Errorf("%w: %s %s: %s", tracker.ErrClaimLost, method, path, snippet(body))
	case http.StatusUnauthorized:
		return fmt.Errorf("%s %s: кред не принят (401), проверь auth в ${OFFICE_HOME}/%s: %s",
			method, path, TrackerFile, snippet(body))
	case http.StatusForbidden:
		return fmt.Errorf("%s %s: доступ запрещён (403), учётке не хватает прав: %s", method, path, snippet(body))
	default:
		return fmt.Errorf("%s %s: %d: %s", method, path, code, snippet(body))
	}
}

// snippet обрезает тело ответа: JIRA бывает многословна, а в лог нужен смысл.
func snippet(body []byte) string {
	const limit = 400
	text := strings.TrimSpace(string(body))
	if len(text) > limit {
		return text[:limit] + "…"
	}
	return text
}

func text(v any) string {
	s, _ := v.(string)
	return s
}

// TrackerFile — подключение к инстансу; живёт в ${OFFICE_HOME}, а не в репозитории.
//
// Инстансных значений в поставляемом образце пять: base_url и четыре customfield_*
// (also_agents тоже свойство инстанса, но на полигоне он пуст). Прочее —
// режим авторизации (у 8.13 он один), имена переменных с учётками, метка ожидания
// человека и почти вся карта статусов — совпадёт у любых двух инстансов;
// нетождественна в ней одна запись, InProgress: In Progress, и та стандартна. Файл уносится целиком
// не потому, что каждая строка своя, а потому, что делить тридцать строк ради пяти
// значений значит завести вторую склейку там, где хватает копирования образца.
const TrackerFile = "tracker.yaml"

// ExampleFile — тот же файл с полигонными значениями и объяснениями, лежащий
// в репозитории. Из него делают tracker.yaml нового инстанса; раннер его
// не читает никогда.
const ExampleFile = "tracker.example.yaml"

// LoadConfig читает tracker.yaml. Разбор строгий, как у роли и графа:
// неизвестное поле — ошибка, а не молча забытая настройка.
func LoadConfig(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	switch {
	// Файла нет — самый частый отказ на новой машине, и он обязан сказать
	// не только чего не хватает, но и откуда это берут: сам по себе tracker.yaml
	// не пишут, его копируют из образца.
	case errors.Is(err, os.ErrNotExist):
		return Config{}, fmt.Errorf("%s не заведён: подключение к JIRA — свойство инстанса, "+
			"а не офиса. Сделайте файл из образца %s, лежащего в конфиг-репозитории", path, ExampleFile)
	case err != nil:
		return Config{}, fmt.Errorf("%s не прочитан: %w", path, err)
	}

	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	// Пустой документ — это io.EOF, и жаловаться на него нечем: «не разобран: EOF»
	// человеку не говорит ничего. Пусть объяснит проверка ниже — она назовёт, чего
	// не хватает. «Завёл файл, ещё не заполнил» на новой машине — обычный шаг.
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("%s не разобран: %w", path, err)
	}

	var errs []error
	// Без адреса инстанса конфигурация не конфигурация, а отказ без него называл бы
	// всё недостающее, кроме самого главного.
	if cfg.BaseURL == "" {
		errs = append(errs, errors.New("base_url не задан: без адреса инстанса идти некуда"))
	}
	if cfg.Accounts.Default.UserEnv == "" || cfg.Accounts.Default.SecretEnv == "" {
		errs = append(errs, errors.New("accounts.default не задана: под ней офис ходит в трекер по умолчанию, учётки ролей — опция поверх неё"))
	}
	if len(cfg.StatusMap) == 0 {
		errs = append(errs, errors.New("status_map пуст: раннер не поймёт, какому статусу графа какой статус инстанса соответствует"))
	}
	for name, field := range map[string]string{
		"agent_owner": cfg.Fields.Owner, "run_id": cfg.Fields.RunID,
		"lease_until": cfg.Fields.LeaseUntil, "attempts": cfg.Fields.Attempts,
	} {
		if !strings.HasPrefix(field, "customfield_") {
			errs = append(errs, fmt.Errorf("fields.%s=%q: ожидается идентификатор вида customfield_10001", name, field))
		}
	}
	if cfg.HumanFlagLabel == "" {
		errs = append(errs, errors.New("human_flag_label не задан: атрибутом ожидания человека служит метка"))
	}
	if err := errors.Join(errs...); err != nil {
		return Config{}, fmt.Errorf("%s нарушает контракт: %w", path, err)
	}
	return cfg, nil
}
