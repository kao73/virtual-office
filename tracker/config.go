package tracker

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
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

// Workflow — граф состояний: статусы, роли и то, куда роль двигает задачу
// по каждому исходу. Всё, что раннер «решает», решается отсюда, а не эвристикой.
//
// Статусы — узлы графа, то есть узлы workflow самого трекера. Колонок здесь нет
// и быть не может: колонка — свойство доски, представление, и досок с разной
// раскладкой одних и тех же статусов бывает сколько угодно. Раннер о досках
// не знает ничего (DESIGN §1).
type Workflow struct {
	Statuses []string `yaml:"statuses"`
	// Terminal — статусы, в которых жизнь задачи кончается: работа опубликована,
	// рабочая папка больше не нужна.
	Terminal []string `yaml:"terminal"`
	// TickOrder — порядок обхода ролей. Задаётся явно: порядок YAML-карты
	// не сохраняется, и «по алфавиту» вышло бы совпадением, а не правилом.
	TickOrder  []string            `yaml:"tick_order"`
	Roles      map[string]RoleFlow `yaml:"roles"`
	Limits     Limits              `yaml:"limits"`
	HumanReply HumanReplyRule      `yaml:"human_reply"`
}

// RoleFlow — место роли в графе.
type RoleFlow struct {
	ReadsFrom string `yaml:"reads_from"`
	// Working — рабочий статус, в который роль переводит захваченную задачу.
	// Необязателен: без него «в работе» означает живую аренду в том же статусе,
	// из которого роль читает. Статус видят люди на доске, и отдельный статус
	// под проверку, длящуюся минуты, был бы на ней шумом.
	Working  string             `yaml:"working"`
	Outcomes map[string]Outcome `yaml:"outcomes"`
}

// Blocked — статус, в котором задача ждёт человека. Отдельной настройки для него
// нет намеренно: это ровно то место, куда роль отправляет задачу с вопросом,
// и раздваивать его — значит однажды развести их по разным статусам.
func (r RoleFlow) Blocked() string {
	return r.Outcomes[string(runner.OutcomeNeedsHuman)].To
}

// Returns — уводит ли next_owner в сторону от маршрута по умолчанию.
//
// Это и есть круг: работа пошла не вперёд по конвейеру, а назад, к тому, кто её
// делал. Передача вперёд — обычное движение, и считать её кругом значило бы
// упереться в предел вдвое раньше, чем задумано: у пары ролей передач вдвое
// больше, чем возвратов.
func (o Outcome) Returns(nextOwner string) bool { return o.Route(nextOwner) != o.To }

// Outcome — что делать с задачей при таком исходе.
type Outcome struct {
	To    string `yaml:"to"`
	Human bool   `yaml:"human"`
	// Attempts — дельта счётчика попыток: `attempts: +1` означает единицу.
	Attempts int `yaml:"attempts"`
	// ByNextOwner — маршрут по полю next_owner из результата агента. Карта
	// явная, а не выведенная из имён ролей: маршрут задаёт граф, а не агент
	// (DESIGN §2.1), и то, чего в карте нет, уезжает по умолчанию.
	ByNextOwner map[string]string `yaml:"by_next_owner"`
}

// Route — статус, в который уходит задача при этом исходе и таком next_owner.
func (o Outcome) Route(nextOwner string) string {
	if to, found := o.ByNextOwner[nextOwner]; found {
		return to
	}
	return o.To
}

// Limits — общие пределы конвейера.
type Limits struct {
	MaxAttempts int `yaml:"max_attempts"`
	// MaxReviewRounds — сколько раз подряд роль может вернуть задачу другой,
	// не одобрив её. Круги возможны там, где у исхода есть маршрут по next_owner;
	// без предела задача ходила бы между ролями вечно.
	MaxReviewRounds int `yaml:"max_review_rounds"`
	// MaxLeaseExpiries — сколько раз подряд прогон может не дожить до отчёта,
	// прежде чем задачу отдадут человеку. Предел отдельный от попыток намеренно:
	// смерть раннера — не провал агента, и разговор с человеком о ней другой.
	MaxLeaseExpiries int `yaml:"max_lease_expiries"`
	// MaxPushFailures — сколько раз подряд может не удаться публикация ветки.
	// Тоже отдельный предел и по той же причине: сломанный remote — не вина агента.
	MaxPushFailures int `yaml:"max_push_failures"`
	LeaseMarginSec  int `yaml:"lease_margin_sec"`
}

// HumanReplyRule — что делает раннер, увидев ответ человека на заблокированную задачу.
//
// Маршрута по умолчанию здесь нет: задача возвращается той роли, которая говорила
// последней, и её очередь известна из графа. Fallback — на случай, когда роли
// из маркера в графе больше нет: её переименовали или убрали, а задача с её
// вопросом осталась.
type HumanReplyRule struct {
	Fallback      string `yaml:"fallback"`
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

// Order — порядок обхода ролей. Роль одна — порядку неоткуда взяться,
// и требовать его незачем.
func (w Workflow) Order() []string {
	if len(w.TickOrder) > 0 {
		return w.TickOrder
	}
	return slices.Sorted(maps.Keys(w.Roles))
}

// IsTerminal — кончается ли жизнь задачи в этом статусе. Дальше её ведёт
// человек, а рабочая папка больше не нужна.
func (w Workflow) IsTerminal(status string) bool {
	return slices.Contains(w.Terminal, status)
}

// HumanStatuses — статусы, в которых задача ждёт человека: всё, куда ведут исходы
// с пометкой human. Порядок устойчивый, повторов нет — две роли вправе ждать
// человека в одном статусе.
//
// Набор нужен разбору ответов человека. Он идёт отдельным проходом, без роли:
// задачу в ожидание отправляет не только агент своим вопросом, но и сам раннер —
// исчерпав попытки или устав возвращать зависшую задачу, — и спрашивать «а чей
// это статус» в такой момент не у кого.
func (w Workflow) HumanStatuses() []string {
	var statuses []string
	for _, role := range w.Roles {
		for _, outcome := range role.Outcomes {
			if outcome.Human && !slices.Contains(statuses, outcome.To) {
				statuses = append(statuses, outcome.To)
			}
		}
	}
	slices.Sort(statuses)
	return statuses
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

	if len(w.Statuses) == 0 {
		errs = append(errs, errors.New("statuses пуст: графа нет"))
	}
	known := func(field, status string) {
		if status == "" {
			errs = append(errs, fmt.Errorf("%s не задан", field))
			return
		}
		if !slices.Contains(w.Statuses, status) {
			errs = append(errs, fmt.Errorf("%s=%q: такого статуса нет в statuses", field, status))
		}
	}

	for _, status := range w.Terminal {
		known("terminal", status)
	}

	if len(w.Roles) == 0 {
		errs = append(errs, errors.New("roles пуст: работать некому"))
	}
	rounds := false
	for name, role := range w.Roles {
		known(name+".reads_from", role.ReadsFrom)
		// Рабочий статус необязателен: без него «в работе» означает живую
		// аренду в статусе, из которого роль читает.
		if role.Working != "" {
			known(name+".working", role.Working)
		}

		// Исход без перехода — это задача, застрявшая в рабочем статусе
		// без объяснения. Лучше не запуститься.
		for _, outcome := range outcomes {
			transition, found := role.Outcomes[outcome]
			if !found {
				errs = append(errs, fmt.Errorf("%s: для исхода %s не задан переход", name, outcome))
				continue
			}
			known(fmt.Sprintf("%s.outcomes.%s.to", name, outcome), transition.To)
		}
		for outcome, transition := range role.Outcomes {
			if !slices.Contains(outcomes, outcome) {
				errs = append(errs, fmt.Errorf("%s: неизвестный исход %q", name, outcome))
			}
			if len(transition.ByNextOwner) == 0 {
				continue
			}
			rounds = true
			// Разветвлять по next_owner имеет смысл только там, где задача
			// передаётся дальше: остальные исходы никому её не отдают.
			if outcome != string(runner.OutcomeDone) {
				errs = append(errs, fmt.Errorf("%s.outcomes.%s: by_next_owner допустим только у %s",
					name, outcome, runner.OutcomeDone))
			}
			for owner, to := range transition.ByNextOwner {
				known(fmt.Sprintf("%s.outcomes.%s.by_next_owner.%s", name, outcome, owner), to)
				// Ключ, названный именем роли, обязан вести в её очередь: иначе
				// карта и reads_from разъедутся, и задача уедет туда, где эта
				// роль её не ищет.
				if target, found := w.Roles[owner]; found && to != target.ReadsFrom {
					errs = append(errs, fmt.Errorf("%s.outcomes.%s.by_next_owner.%s=%q: роль %s читает из %q",
						name, outcome, owner, to, owner, target.ReadsFrom))
				}
			}
		}
	}

	errs = append(errs, w.checkOrder()...)

	// Круги считаются там, где роль вправе вернуть задачу другой. Без предела
	// задача ходила бы между ролями вечно.
	if rounds && w.Limits.MaxReviewRounds <= 0 {
		errs = append(errs, fmt.Errorf("limits.max_review_rounds=%d: у графа есть маршрут по next_owner, круги нужно ограничить",
			w.Limits.MaxReviewRounds))
	}

	if w.Limits.MaxAttempts <= 0 {
		errs = append(errs, fmt.Errorf("limits.max_attempts=%d: ожидается положительное число", w.Limits.MaxAttempts))
	}
	if w.Limits.MaxLeaseExpiries <= 0 {
		errs = append(errs, fmt.Errorf("limits.max_lease_expiries=%d: ожидается положительное число", w.Limits.MaxLeaseExpiries))
	}
	if w.Limits.MaxPushFailures <= 0 {
		errs = append(errs, fmt.Errorf("limits.max_push_failures=%d: ожидается положительное число", w.Limits.MaxPushFailures))
	}
	if w.Limits.LeaseMarginSec < 0 {
		errs = append(errs, fmt.Errorf("limits.lease_margin_sec=%d: ожидается неотрицательное число", w.Limits.LeaseMarginSec))
	}
	known("human_reply.fallback", w.HumanReply.Fallback)

	return errors.Join(errs...)
}

// checkOrder проверяет порядок обхода ролей: каждая роль ровно один раз.
//
// Порядок задаётся явно, потому что вывести его неоткуда: порядок YAML-карты
// в Go не сохраняется, а обход по алфавиту — совпадение, которое однажды
// перестанет совпадать с замыслом. С одной ролью порядок не нужен.
func (w Workflow) checkOrder() []error {
	if len(w.Roles) < 2 && len(w.TickOrder) == 0 {
		return nil
	}

	var errs []error
	seen := make(map[string]bool, len(w.TickOrder))
	for _, name := range w.TickOrder {
		switch {
		case seen[name]:
			errs = append(errs, fmt.Errorf("tick_order: роль %q названа дважды", name))
		case w.Roles[name].ReadsFrom == "":
			errs = append(errs, fmt.Errorf("tick_order: роль %q в roles не описана", name))
		}
		seen[name] = true
	}
	for name := range w.Roles {
		if !seen[name] {
			errs = append(errs, fmt.Errorf("tick_order: роль %q не названа, порядок обхода неизвестен", name))
		}
	}
	return errs
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

// Keys — ключи проектов по порядку. Порядок устойчивый: обход проектов не должен
// зависеть от того, как в этот раз лёг хеш.
func (p Projects) Keys() []string { return slices.Sorted(maps.Keys(p)) }

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
