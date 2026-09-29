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
	return fc, nil
}

func (fc FileConfig) validate() []error {
	var errs []error
	if fc.BaseURL == "" {
		errs = append(errs, errors.New("base_url не задан: без адреса YouGile идти некуда (пример: https://yougile.com)"))
	} else if u, err := url.Parse(fc.BaseURL); err == nil && strings.EqualFold(u.Hostname(), refusedHost) {
		errs = append(errs, fmt.Errorf("base_url=%q: с %s вложения не скачаются — /user-data/ перенаправляет "+
			"на prod-user-data.yougile.com, а это не поддомен %s; укажите https://yougile.com",
			fc.BaseURL, refusedHost, refusedHost))
	}
	if fc.APIKeyEnv == "" {
		errs = append(errs, errors.New("api_key_env не задан: в файле — имя переменной окружения с ключом API, не сам ключ"))
	}
	switch len(fc.Projects) {
	case 0:
		errs = append(errs, errors.New("projects пуст: опишите проект YouGile под его ключом из projects.local.yaml"))
	case 1:
		for key, p := range fc.Projects {
			if p.ProjectID == "" {
				errs = append(errs, fmt.Errorf("projects.%s.project_id не задан", key))
			}
			if len(p.Columns) == 0 {
				errs = append(errs, fmt.Errorf("projects.%s.columns пуст: статус графа — это колонка, "+
					"раннеру нужна карта статус → id колонки", key))
			}
		}
	default:
		errs = append(errs, fmt.Errorf("projects описывает %d проекта (%s): поддерживается один проект YouGile на раннер",
			len(fc.Projects), strings.Join(slices.Sorted(maps.Keys(fc.Projects)), ", ")))
	}
	return errs
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
