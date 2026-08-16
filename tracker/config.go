package tracker

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kao73/virtual-office/runner"
)

// Файлы конфигурации в корне конфиг-репозитория.
const (
	// WorkflowFile — граф переходов. Его читает раннер; агент его не видит.
	WorkflowFile = "workflow.yaml"
	// ProjectsFile — проекты-клиенты: где их репозитории и как звать ветки.
	ProjectsFile = "projects.yaml"
)

// outcomes — исходы, для которых граф обязан задать переход. Список берётся
// из контракта «раннер ↔ агент»: собственный разъехался бы с ним.
var outcomes = []string{
	string(runner.OutcomeDone),
	string(runner.OutcomeNeedsHuman),
	string(runner.OutcomeBlocked),
	string(runner.OutcomeFailed),
}

// Workflow — граф состояний: колонки, роли и то, куда роль двигает задачу
// по каждому исходу. Всё, что раннер «решает», решается отсюда, а не эвристикой.
type Workflow struct {
	Columns    []string            `yaml:"columns"`
	Roles      map[string]RoleFlow `yaml:"roles"`
	Limits     Limits              `yaml:"limits"`
	HumanReply HumanReplyRule      `yaml:"human_reply"`
}

// RoleFlow — место роли в графе.
type RoleFlow struct {
	ReadsFrom string             `yaml:"reads_from"`
	Working   string             `yaml:"working"`
	Outcomes  map[string]Outcome `yaml:"outcomes"`
}

// Blocked — колонка, в которой задача ждёт человека. Отдельной настройки для неё
// нет намеренно: это ровно то место, куда роль отправляет задачу с вопросом,
// и раздваивать его — значит однажды развести их по разным колонкам.
func (r RoleFlow) Blocked() string {
	return r.Outcomes[string(runner.OutcomeNeedsHuman)].To
}

// Outcome — что делать с задачей при таком исходе.
type Outcome struct {
	To    string `yaml:"to"`
	Human bool   `yaml:"human"`
	// Attempts — дельта счётчика попыток: `attempts: +1` означает единицу.
	Attempts int `yaml:"attempts"`
}

// Limits — общие пределы конвейера.
type Limits struct {
	MaxAttempts    int `yaml:"max_attempts"`
	LeaseMarginSec int `yaml:"lease_margin_sec"`
}

// HumanReplyRule — что делает раннер, увидев ответ человека на заблокированную задачу.
type HumanReplyRule struct {
	To            string `yaml:"to"`
	ResetAttempts bool   `yaml:"reset_attempts"`
}

// LoadWorkflow читает и проверяет граф. Разбор строгий: неизвестное поле — ошибка,
// а не молча забытая настройка.
func LoadWorkflow(path string) (Workflow, error) {
	var w Workflow
	if err := decodeStrict(path, &w); err != nil {
		return Workflow{}, err
	}
	if err := w.validate(); err != nil {
		return Workflow{}, fmt.Errorf("%s нарушает контракт графа: %w", path, err)
	}
	return w, nil
}

// LeaseMargin — запас, который раннер добавляет к таймауту роли, назначая аренду.
func (w Workflow) LeaseMargin() time.Duration {
	return time.Duration(w.Limits.LeaseMarginSec) * time.Second
}

// Role — описание роли в графе.
func (w Workflow) Role(name string) (RoleFlow, error) {
	role, found := w.Roles[name]
	if !found {
		return RoleFlow{}, fmt.Errorf("роль %q не описана в %s", name, WorkflowFile)
	}
	return role, nil
}

func (w Workflow) validate() error {
	var errs []error

	if len(w.Columns) == 0 {
		errs = append(errs, errors.New("columns пуст: графа нет"))
	}
	known := func(field, column string) {
		if column == "" {
			errs = append(errs, fmt.Errorf("%s не задан", field))
			return
		}
		if !slices.Contains(w.Columns, column) {
			errs = append(errs, fmt.Errorf("%s=%q: такой колонки нет в columns", field, column))
		}
	}

	if len(w.Roles) == 0 {
		errs = append(errs, errors.New("roles пуст: работать некому"))
	}
	for name, role := range w.Roles {
		known(name+".reads_from", role.ReadsFrom)
		known(name+".working", role.Working)

		// Исход без перехода — это задача, застрявшая в рабочей колонке
		// без объяснения. Лучше не запуститься.
		for _, outcome := range outcomes {
			transition, found := role.Outcomes[outcome]
			if !found {
				errs = append(errs, fmt.Errorf("%s: для исхода %s не задан переход", name, outcome))
				continue
			}
			known(fmt.Sprintf("%s.outcomes.%s.to", name, outcome), transition.To)
		}
		for outcome := range role.Outcomes {
			if !slices.Contains(outcomes, outcome) {
				errs = append(errs, fmt.Errorf("%s: неизвестный исход %q", name, outcome))
			}
		}
	}

	if w.Limits.MaxAttempts <= 0 {
		errs = append(errs, fmt.Errorf("limits.max_attempts=%d: ожидается положительное число", w.Limits.MaxAttempts))
	}
	if w.Limits.LeaseMarginSec < 0 {
		errs = append(errs, fmt.Errorf("limits.lease_margin_sec=%d: ожидается неотрицательное число", w.Limits.LeaseMarginSec))
	}
	known("human_reply.to", w.HumanReply.To)

	return errors.Join(errs...)
}

// Project — проект-клиент: где его репозиторий и как раннер зовёт ветки задач.
type Project struct {
	RepoURL       string `yaml:"repo_url"`
	DefaultBranch string `yaml:"default_branch"`
	BranchPrefix  string `yaml:"branch_prefix"`
	// WorktreeRoot необязателен: пусто — значит ${OFFICE_HOME}/worktrees/<project>.
	WorktreeRoot string `yaml:"worktree_root"`
}

// Projects — проекты по ключу трекера.
type Projects map[string]Project

// Branch — ветка задачи.
func (p Project) Branch(key string) string { return p.BranchPrefix + key }

// Get отдаёт проект по ключу. Задачу неизвестного проекта раннер брать не вправе:
// ему негде взять репозиторий и некуда пушить.
func (p Projects) Get(key string) (Project, error) {
	project, found := p[key]
	if !found {
		return Project{}, fmt.Errorf("проект %q не описан в %s: задача не может быть взята в работу", key, ProjectsFile)
	}
	return project, nil
}

// LoadProjects читает и проверяет список проектов.
func LoadProjects(path string) (Projects, error) {
	var p Projects
	if err := decodeStrict(path, &p); err != nil {
		return nil, err
	}

	var errs []error
	for key, project := range p {
		if project.RepoURL == "" {
			errs = append(errs, fmt.Errorf("%s: repo_url не задан", key))
		}
		if project.DefaultBranch == "" {
			errs = append(errs, fmt.Errorf("%s: default_branch не задан", key))
		}
		if project.BranchPrefix == "" {
			errs = append(errs, fmt.Errorf("%s: branch_prefix не задан", key))
		}
		if root := project.WorktreeRoot; root != "" && !filepath.IsAbs(root) {
			errs = append(errs, fmt.Errorf("%s: worktree_root=%q должен быть абсолютным", key, root))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("%s нарушает контракт: %w", path, err)
	}
	return p, nil
}

// decodeStrict читает YAML, отвергая неизвестные поля.
func decodeStrict(path string, into any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%s не прочитан: %w", filepath.Base(path), err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(into); err != nil {
		return fmt.Errorf("%s не разобран: %w", path, err)
	}
	return nil
}
