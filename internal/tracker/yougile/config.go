package yougile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"os"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// TrackerFile — подключение к YouGile; живёт в ${OFFICE_HOME}, а не
// в репозитории, и открывается, только если проект назвал tracker: yougile.
// Свой файл, а не раздел tracker.yaml: тот — плоская схема JIRA, и делить
// его значило бы сломать каждую настроенную JIRA-машину.
const TrackerFile = "tracker-yougile.yaml"

// ExampleFile — образец TrackerFile с объяснениями. Лежит в репозитории
// и в поставке; `runner init` кладёт его в ${OFFICE_HOME}. Раннер его
// не читает никогда.
const ExampleFile = "tracker-yougile.example.yaml"

// refusedHost — хост, с которым вложения не скачиваются: /user-data/…
// отвечает 302 на prod-user-data.yougile.com, а файловый клиент идёт только
// на хост BaseURL и его поддомены (newFileClient).
const refusedHost = "ru.yougile.com"

// FileConfig — TrackerFile как он лежит на диске.
type FileConfig struct {
	BaseURL string `yaml:"base_url"`
	// APIKeyEnv — имя переменной окружения с ключом API; сам ключ в файл
	// не попадает никогда, как secret_env у tracker.yaml.
	APIKeyEnv string `yaml:"api_key_env"`
	// AlsoAgents — email'ы чужой автоматизации: их комментарии тоже не слова
	// человека. Учётку самого офиса сюда не пишут — раннер узнаёт её у
	// /users/me.
	AlsoAgents []string `yaml:"also_agents"`
	// Projects — по ключу из projects.local.yaml. Карта, а не одна запись,
	// чтобы второй проект однажды не менял форму файла; сегодня он ровно один.
	Projects map[string]ProjectConfig `yaml:"projects"`
}

// ProjectConfig — один проект YouGile: его id и колонка на каждый статус графа.
type ProjectConfig struct {
	ProjectID    string            `yaml:"project_id"`
	Columns      map[string]string `yaml:"columns"`
	CreateStatus string            `yaml:"create_status"`
}

// LoadConfig читает TrackerFile. Разбор строгий, как у tracker.yaml:
// неизвестное поле — ошибка, а не молча забытая настройка. Все нарушения
// идут одним отказом: починив одно, узнавать о втором следующим запуском —
// лишний круг.
func LoadConfig(path string) (FileConfig, error) {
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return FileConfig{}, fmt.Errorf("%s не заведён: подключение к YouGile — свойство инстанса, "+
			"а не офиса. Сделайте файл из образца %s: его кладёт рядом `runner init` "+
			"(в клоне он лежит в office/%s)", path, ExampleFile, ExampleFile)
	case err != nil:
		return FileConfig{}, fmt.Errorf("%s не прочитан: %w", path, err)
	}

	var fc FileConfig
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	// Пустой документ — io.EOF; о нём скажет проверка ниже, назвав недостающее.
	if err := dec.Decode(&fc); err != nil && !errors.Is(err, io.EOF) {
		return FileConfig{}, fmt.Errorf("%s не разобран: %w", path, err)
	}
	if err := errors.Join(fc.validate()...); err != nil {
		return FileConfig{}, fmt.Errorf("%s нарушает контракт: %w", path, err)
	}
	// Email нечувствителен к регистру, а сравнивают его с автором комментария
	// строкой (slices.Contains в pipeline); userEmail и Whoami приводят свою
	// сторону так же.
	for i, email := range fc.AlsoAgents {
		fc.AlsoAgents[i] = normEmail(email)
	}
	return fc, nil
}

// normEmail — email в форме, в которой его сравнивают: без пробелов по краям
// и в нижнем регистре.
func normEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// validate проверяет всё, что видно из самого файла, без графа и без сети:
// doctor показывает эти отказы под config:, а не под yougile:open, где их
// приняли бы за беду сервера. Open повторяет часть проверок — для тех, кто
// собирает Config не из файла.
func (fc FileConfig) validate() []error {
	var errs []error
	if _, err := parseBaseURL(fc.BaseURL); err != nil {
		errs = append(errs, err)
	}
	if fc.APIKeyEnv == "" {
		errs = append(errs, errors.New("api_key_env не задан: в файле — имя переменной окружения с ключом API, не сам ключ"))
	}
	for i, email := range fc.AlsoAgents {
		if normEmail(email) == "" {
			errs = append(errs, fmt.Errorf("also_agents[%d] пуст: там ждут email учётки чужой автоматизации", i))
		}
	}
	switch len(fc.Projects) {
	case 0:
		errs = append(errs, errors.New("projects пуст: опишите проект YouGile под его ключом из projects.local.yaml"))
	case 1:
		for key, p := range fc.Projects {
			if strings.TrimSpace(key) == "" {
				errs = append(errs, errors.New("projects: ключ проекта пуст — нужен ключ из projects.local.yaml"))
			}
			errs = append(errs, p.validate(key)...)
		}
	default:
		errs = append(errs, fmt.Errorf("projects описывает %d проекта (%s): поддерживается один проект YouGile на раннер",
			len(fc.Projects), strings.Join(slices.Sorted(maps.Keys(fc.Projects)), ", ")))
	}
	return errs
}

// parseBaseURL разбирает base_url и требует корень хоста: схема http(s) и
// хост, и больше ничего, кроме слешей на конце, — ни пути (/api-v2 в том
// числе), ни query, ни fragment, ни логина. Адрес запроса склеивается из
// base_url строкой (send, download), и всё сверх корня ушло бы в каждый
// запрос, а ответ выдал бы себя за «нет проекта». Правило — одно сравнение
// с каноническим корнем, а не перечень синтаксиса URL: пустой «#» и «%2F»
// разбор не показывает ни во Fragment, ни в Path. ru.yougile.com тоже
// отвергается. Адрес в отказе — без пароля (Redacted): отказ печатают doctor
// и лог.
func parseBaseURL(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, errors.New("base_url не задан: без адреса YouGile идти некуда (пример: https://yougile.com)")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("base_url не разобран: %w", err)
	}
	shown := u.Redacted()
	root := u.Scheme + "://" + u.Host
	switch {
	case u.Scheme == "" || u.Host == "":
		return nil, fmt.Errorf("base_url=%q: нет схемы или хоста (пример: https://yougile.com)", shown)
	case u.Scheme != "https" && u.Scheme != "http":
		return nil, fmt.Errorf("base_url=%q: схема — http или https (пример: https://yougile.com)", shown)
	case strings.HasSuffix(strings.ToLower(strings.TrimRight(u.EscapedPath(), "/")), apiPrefix):
		return nil, fmt.Errorf("base_url=%q: %s адаптер добавляет сам, укажите корень хоста (пример: https://yougile.com)", shown, apiPrefix)
	case !strings.EqualFold(strings.TrimRight(raw, "/"), root):
		return nil, fmt.Errorf("base_url=%q: нужен корень хоста, без пути, query, fragment и логина (пример: https://yougile.com)", shown)
	// «ru.yougile.com.» с точкой на конце — тот же хост.
	case strings.EqualFold(strings.TrimSuffix(u.Hostname(), "."), refusedHost):
		return nil, fmt.Errorf("base_url=%q: с %s вложения не скачаются — /user-data/ перенаправляет "+
			"на prod-user-data.yougile.com, а это не поддомен %s; укажите https://yougile.com",
			shown, refusedHost, refusedHost)
	}
	return u, nil
}

// validate — правила проекта, которые видны без графа: id проекта, колонки
// с непустыми и несовпадающими id и create_status среди них. create_status
// обязателен: без него офис не заведёт детей разбиения, и узнать об этом
// посреди цикла, после двух подтверждений человека, — поздно.
func (p ProjectConfig) validate(key string) []error {
	var errs []error
	if p.ProjectID == "" {
		errs = append(errs, fmt.Errorf("projects.%s.project_id не задан", key))
	}
	if len(p.Columns) == 0 {
		errs = append(errs, fmt.Errorf("projects.%s.columns пуст: статус графа — это колонка, "+
			"раннеру нужна карта статус → id колонки", key))
	}
	owner := map[string]string{}
	for _, status := range slices.Sorted(maps.Keys(p.Columns)) {
		id := p.Columns[status]
		switch before, taken := owner[id]; {
		case id == "":
			errs = append(errs, fmt.Errorf("projects.%s.columns.%s: пустой id колонки", key, status))
		case taken:
			errs = append(errs, fmt.Errorf("projects.%s.columns: колонка %q сопоставлена и с %s, и с %s — "+
				"статус задачи в YouGile это её колонка, одна колонка на два статуса не годится", key, id, before, status))
		default:
			owner[id] = status
		}
	}
	switch _, ok := p.Columns[p.CreateStatus]; {
	case p.CreateStatus == "":
		errs = append(errs, fmt.Errorf("projects.%s.create_status не задан: в колонку этого статуса офис "+
			"кладёт детей разбиения (в образце — Backlog)", key))
	case !ok:
		errs = append(errs, fmt.Errorf("projects.%s.create_status=%q нет среди projects.%s.columns", key, p.CreateStatus, key))
	}
	return errs
}

// CheckGraph сверяет колонки проекта key с графом в обе стороны. У каждого
// статуса графа есть колонка: статус задачи в YouGile — её колонка, и без
// колонки для Backlog или Done задачу в таком статусе не прочтёт ни List, ни
// доска. И каждый ключ columns — статус графа: колонка под лишним ключом
// (опечатка, Todo) читалась бы со статусом, которого ни одна роль не берёт, и
// задачи в ней — а при create_status на ней и дети разбиения — молча стояли
// бы. Колонки вне графа в columns просто не пишут. Сверка локальная: сами
// колонки на сервере проверяет Open.
func (fc FileConfig) CheckGraph(key string, statuses []string) error {
	project, ok := fc.Projects[key]
	if !ok {
		return fmt.Errorf("%s не описывает проект %q", TrackerFile, key)
	}
	columns := project.Columns
	var missing, unknown []string
	for _, status := range statuses {
		if columns[status] == "" {
			missing = append(missing, status)
		}
	}
	for _, status := range slices.Sorted(maps.Keys(columns)) {
		if !slices.Contains(statuses, status) {
			unknown = append(unknown, status)
		}
	}
	var errs []error
	if len(missing) > 0 {
		errs = append(errs, fmt.Errorf("у статусов графа %s нет колонки в projects.%s.columns",
			strings.Join(missing, ", "), key))
	}
	if len(unknown) > 0 {
		errs = append(errs, fmt.Errorf("projects.%s.columns называет %s, а таких статусов в графе нет "+
			"(статусы графа: %s); колонки вне графа в columns не пишут",
			key, strings.Join(unknown, ", "), strings.Join(statuses, ", ")))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("%s: %w", TrackerFile, err)
	}
	return nil
}

// ProjectKey — ключ единственного проекта. Осмыслен после LoadConfig: тот
// не пропускает файл, где проектов не ровно один.
func (fc FileConfig) ProjectKey() string {
	for key := range fc.Projects {
		return key
	}
	return ""
}

// Tracker собирает Config адаптера для проекта key; ключ API берётся из
// переменной api_key_env. Пустая переменная — отказ с её именем: Open сказал
// бы только «ключ API не задан», не сказав, где его ждали.
func (fc FileConfig) Tracker(key string) (Config, error) {
	p, ok := fc.Projects[key]
	if !ok {
		return Config{}, fmt.Errorf("%s не описывает проект %q", TrackerFile, key)
	}
	apiKey := os.Getenv(fc.APIKeyEnv)
	if apiKey == "" {
		return Config{}, fmt.Errorf("переменная %s пуста: в ней ждут ключ API YouGile (api_key_env в %s)",
			fc.APIKeyEnv, TrackerFile)
	}
	return Config{
		Key:          key,
		BaseURL:      fc.BaseURL,
		APIKey:       apiKey,
		ProjectID:    p.ProjectID,
		ColumnIDs:    maps.Clone(p.Columns),
		CreateStatus: p.CreateStatus,
	}, nil
}
