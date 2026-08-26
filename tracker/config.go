package tracker

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kao73/virtual-office/runner"
)

// Файлы конфигурации. Одни описывают офис и живут в конфиг-репозитории, другие
// описывают инстанс и живут в хозяйстве раннера. Граница между ними — не вкус
// раскладки: всё, что правят, заводя новую машину, обязано лежать вне репозитория,
// иначе правка под себя пачкает рабочее дерево и метка config:…-dirty перестаёт
// что-либо значить.
const (
	// WorkflowFile — граф переходов. Его читает раннер; агент его не видит.
	// Описывает офис: одинаков у всех, кто его поднимет.
	WorkflowFile = "workflow.yaml"
	// ProjectsFile — проекты-клиенты офиса: имена и неизменные свойства.
	// Описывает офис.
	ProjectsFile = "projects.yaml"
	// ProjectsLocalFile — те же проекты на этой машине: где лежат репозитории,
	// в каком трекере задачи, куда открывать pull request. Описывает инстанс
	// и живёт в ${OFFICE_HOME}.
	ProjectsLocalFile = "projects.local.yaml"
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
	// Terminal — статусы, в которых жизнь задачи кончается: работа слита,
	// и рабочая папка больше не нужна. Убирает её системный проход, обходя
	// папки, а не задачи.
	Terminal []string `yaml:"terminal"`
	// TickOrder — порядок обхода ролей. Задаётся явно: порядок YAML-карты
	// не сохраняется, и «по алфавиту» вышло бы совпадением, а не правилом.
	TickOrder []string            `yaml:"tick_order"`
	Roles     map[string]RoleFlow `yaml:"roles"`
	// PR — проход pull request. Отдельным блоком, а не ролью в roles, и это
	// не оформление: роль в roles валидатор потребует в tick_order, а обход
	// полезет за roles/<имя>/role.yaml. Агента у прохода нет — работу делает
	// раннер, — и файла роли не будет никогда.
	PR         PRFlow         `yaml:"pr"`
	Limits     Limits         `yaml:"limits"`
	HumanReply HumanReplyRule `yaml:"human_reply"`
}

// PRFlow — маршрут pull request: откуда проход берёт задачи и куда уводит
// по каждому исходу.
//
// Role — имя, которым подписаны записи прохода. Оно конфигурация, а не
// константа кода: раннер знает его только отсюда.
type PRFlow struct {
	Role     string `yaml:"role"`
	From     string `yaml:"from"`
	Merged   string `yaml:"merged"`
	Conflict string `yaml:"conflict"`
	Closed   string `yaml:"closed"`
}

// Set — описан ли проход вовсе. Граф без блока pr — это офис, который PR
// не открывает; такой граф законен.
func (p PRFlow) Set() bool { return p.Role != "" }

// Flow — проход глазами остального раннера: такой же поток, как у роли.
//
// Нужен там, где код спрашивает граф по имени из маркера и не знает, роль это
// или проход: разбор ответа человека и записка в ожидание. Исход у прохода
// ровно один — «спросить человека»; остальные маршруты он выбирает сам,
// потому что у него нет агента, который назвал бы исход.
func (p PRFlow) Flow() RoleFlow {
	return RoleFlow{
		ReadsFrom: p.From,
		Outcomes: map[string]Outcome{
			string(runner.OutcomeNeedsHuman): {To: p.Closed, Human: true},
		},
	}
}

// RoleFlow — место роли в графе.
type RoleFlow struct {
	ReadsFrom string `yaml:"reads_from"`
	// Working — рабочий статус, в который роль переводит захваченную задачу.
	// Необязателен: без него «в работе» означает живую аренду в том же статусе,
	// из которого роль читает.
	//
	// Занятость выражена арендой и без него, поэтому рабочий статус — дубль,
	// и берут его там, где дубль что-то даёт: он единственный CAS, какой есть
	// в JIRA (переход, доступный ровно из одного статуса), и он показывает
	// «в работе» тому, кто смотрит в трекер, а не в раннер. Роли, чей прогон
	// длится минуты и которую незачем параллелить, он не даёт ничего.
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

// Routed — описан ли этот владелец в карте маршрутов исхода.
//
// Нужен затем, что «маршрута нет» и «маршрут по умолчанию» — разные вещи, а Route
// их не различает: он обязан всегда отвечать статусом. Роль графа, названную
// владельцем и не описанную в карте, раннер по умолчанию не везёт.
func (o Outcome) Routed(nextOwner string) bool {
	_, found := o.ByNextOwner[nextOwner]
	return found
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
	// MaxReturnRounds — сколько раз подряд роль может отдать задачу одному и тому же
	// владельцу, не сдвинув её вперёд. Круги возможны там, где у исхода есть маршрут
	// по next_owner; без предела задача ходила бы между ролями вечно.
	MaxReturnRounds int `yaml:"max_return_rounds"`
	// MaxLeaseExpiries — сколько раз подряд прогон может не дожить до отчёта,
	// прежде чем задачу отдадут человеку. Предел отдельный от попыток намеренно:
	// смерть раннера — не провал агента, и разговор с человеком о ней другой.
	MaxLeaseExpiries int `yaml:"max_lease_expiries"`
	// MaxPushFailures — сколько раз подряд может не удаться публикация ветки.
	// Тоже отдельный предел и по той же причине: сломанный remote — не вина агента.
	MaxPushFailures int `yaml:"max_push_failures"`
	// MaxIdleRuns — сколько прогонов роли подряд может не дойти до результата,
	// прежде чем задачу отдадут человеку. Считает записи двух видов одним
	// счётчиком: «не начинал» и «не успел». Общий он потому, что следствие
	// у них одно, а два раздельных счётчика чередование обошло бы.
	MaxIdleRuns    int `yaml:"max_idle_runs"`
	LeaseMarginSec int `yaml:"lease_margin_sec"`
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

// IsTerminal — кончается ли жизнь задачи в этом статусе: работа слита,
// и рабочая папка больше не нужна. Убирает её системный проход.
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
	// Проход тоже отправляет задачу к человеку — закрытым без слияния PR.
	flows := slices.Collect(maps.Values(w.Roles))
	if w.PR.Set() {
		flows = append(flows, w.PR.Flow())
	}

	var statuses []string
	for _, role := range flows {
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
//
// Имя PR-прохода тоже отвечает: маркеры прохода подписаны им, и разбор ответа
// человека спрашивает граф именно по имени из маркера. В roles и в tick_order
// прохода при этом нет — см. PRFlow.
func (w Workflow) Role(name string) (RoleFlow, error) {
	if w.PR.Set() && name == w.PR.Role {
		return w.PR.Flow(), nil
	}
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
	errs = append(errs, w.checkPR(known)...)

	// Круги считаются там, где роль вправе вернуть задачу другой. Без предела
	// задача ходила бы между ролями вечно.
	if rounds && w.Limits.MaxReturnRounds <= 0 {
		errs = append(errs, fmt.Errorf("limits.max_return_rounds=%d: у графа есть маршрут по next_owner, круги нужно ограничить",
			w.Limits.MaxReturnRounds))
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
	if w.Limits.MaxIdleRuns <= 0 {
		errs = append(errs, fmt.Errorf("limits.max_idle_runs=%d: ожидается положительное число", w.Limits.MaxIdleRuns))
	}
	if w.Limits.LeaseMarginSec < 0 {
		errs = append(errs, fmt.Errorf("limits.lease_margin_sec=%d: ожидается неотрицательное число", w.Limits.LeaseMarginSec))
	}
	known("human_reply.fallback", w.HumanReply.Fallback)

	return errors.Join(errs...)
}

// checkPR проверяет блок PR-прохода.
//
// Проход необязателен: офис без него просто не открывает pull request. Но блок,
// заполненный наполовину, — это не «без прохода», а опечатка, и молчать о ней
// нельзя: задача застряла бы в статусе, из которого её никто не берёт.
func (w Workflow) checkPR(known func(field, status string)) []error {
	var errs []error
	filled := w.PR.Role != "" || w.PR.From != "" || w.PR.Merged != "" ||
		w.PR.Conflict != "" || w.PR.Closed != ""
	if !filled {
		return nil
	}
	if w.PR.Role == "" {
		errs = append(errs, errors.New("pr.role не задан: записями прохода нечего подписывать"))
	}
	// Проход — не роль: у него нет агента и не будет файла роли. Совпадение имён
	// увело бы задачи прохода в очередь роли и обратно.
	if _, clash := w.Roles[w.PR.Role]; clash {
		errs = append(errs, fmt.Errorf("pr.role=%q: такая роль есть в roles, а проход ролью не является", w.PR.Role))
	}
	known("pr.from", w.PR.From)
	known("pr.merged", w.PR.Merged)
	known("pr.conflict", w.PR.Conflict)
	known("pr.closed", w.PR.Closed)

	// Из pr.from проход задачи берёт — терминальным этот статус быть не может:
	// уборка снесла бы рабочую папку прямо под открытым PR.
	if slices.Contains(w.Terminal, w.PR.From) {
		errs = append(errs, fmt.Errorf("pr.from=%q числится терминальным: из этого статуса проход берёт задачи", w.PR.From))
	}
	// А pr.merged обязан быть терминальным: слитый PR — это конец жизни задачи,
	// и папку убирает уборка терминальных.
	if w.PR.Merged != "" && !slices.Contains(w.Terminal, w.PR.Merged) {
		errs = append(errs, fmt.Errorf("pr.merged=%q не числится терминальным: после слияния задача никуда больше не идёт", w.PR.Merged))
	}
	return errs
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

// Трекеры, которые офис умеет вести. Список живёт здесь, а не в точке входа:
// его спрашивает разбор машинной половины проектов, и разъехаться этим двум
// местам нельзя.
var trackers = []string{"mock", "jira"}

// Trackers — имена трекеров для подсказок и сообщений об ошибке.
func Trackers() []string { return slices.Clone(trackers) }

// Rules — сетевой и инструментальный слой, который может назвать любой
// уровень слоистой модели (repo-wide умолчания, конкретный проект, машина).
// Роль (уровень 4) сюда не входит: она использует собственные Network/Tools
// из runner.Role, и сливается с этим слоем отдельным шагом —
// см. MergeProjectRules (tracker/rules.go), а не здесь.
type Rules struct {
	Network []string     `yaml:"network"`
	Tools   runner.Tools `yaml:"tools"`
}

// Project — проект-клиент: где его репозиторий, как раннер зовёт ветки задач,
// в каком трекере лежат его задачи и куда офис открывает pull request.
type Project struct {
	RepoURL       string `yaml:"repo_url"`
	DefaultBranch string `yaml:"default_branch"`
	BranchPrefix  string `yaml:"branch_prefix"`
	// WorktreeRoot необязателен: пусто — значит ${OFFICE_HOME}/worktrees/<project>.
	WorktreeRoot string `yaml:"worktree_root"`
	// Tracker — трекер проекта. Обязателен: одна и та же задача не живёт разом
	// в файловом трекере и в JIRA, и раннер, запущенный с одним трекером,
	// не должен видеть чужих проектов.
	Tracker string `yaml:"tracker"`
	// Forge — куда открывать pull request. Пусто — forge у проекта нет:
	// PR-проход вырождается, но маршрут остаётся тем же (см. workflow.yaml: pr).
	Forge string `yaml:"forge"`
	// Network — уровни 1–3 слоистой модели (repo-wide + проект + машина),
	// уже объединённые LoadProjects. Уровень 4 (роль) сюда не входит —
	// его добавляет MergeProjectRules ближе к месту запуска.
	Network []string
	// Tools — то же самое для tools.allow/tools.deny.
	Tools runner.Tools
}

// Половины проекта, разложенные по двум файлам. Раздельные типы нужны разбору:
// строгое чтение отвергает поле, которого в типе нет, и потому само по себе
// не пускает машинный ключ в файл офиса, а свойство офиса — в файл машины.
type (
	// officeProject — то, что одинаково у всех, кто поднимет этот офис.
	officeProject struct {
		DefaultBranch string `yaml:"default_branch"`
		BranchPrefix  string `yaml:"branch_prefix"`
		Rules         `yaml:",inline"`
	}

	// machineProject — то, что правят, заводя новую машину или второй инстанс.
	machineProject struct {
		RepoURL      string `yaml:"repo_url"`
		WorktreeRoot string `yaml:"worktree_root"`
		Tracker      string `yaml:"tracker"`
		Forge        string `yaml:"forge"`
		Rules        `yaml:",inline"`
	}
)

// machineKeys — ключи, описывающие машину. Список нужен ради сообщения об ошибке:
// строгий разбор и без него отвергнет их в файле офиса, но скажет «неизвестное
// поле», а человеку нужно знать, куда ключ переехал и почему.
var machineKeys = []string{"repo_url", "worktree_root", "tracker", "forge"}

// Projects — проекты по ключу трекера.
type Projects map[string]Project

// Branch — ветка задачи.
func (p Project) Branch(key string) string { return p.BranchPrefix + key }

// Keys — ключи проектов по порядку. Порядок устойчивый: обход проектов не должен
// зависеть от того, как в этот раз лёг хеш.
func (p Projects) Keys() []string { return slices.Sorted(maps.Keys(p)) }

// For — проекты одного трекера.
//
// Раннер запускается с одним трекером и работает только со своими проектами.
// Чужие не просто бесполезны: спрашивать о них трекер — значит получать ошибку
// «нет такого проекта» на каждом проходе, а сносить их рабочие папки уборкой
// системного прохода — терять чужую работу.
func (p Projects) For(tracker string) Projects {
	mine := Projects{}
	for key, project := range p {
		if project.Tracker == tracker {
			mine[key] = project
		}
	}
	return mine
}

// Get отдаёт проект по ключу. Задачу неизвестного проекта раннер брать не вправе:
// ему негде взять репозиторий и некуда пушить.
//
// Причин у «неизвестного» теперь две, и сообщение называет обе: проекта может
// не быть в конфигурации вовсе, а может он быть чужого трекера — карта к этому
// моменту уже просеяна `For`. Назвать одну значило бы отправить искать не туда.
func (p Projects) Get(key string) (Project, error) {
	project, found := p[key]
	if !found {
		return Project{}, fmt.Errorf("проект %q не описан в %s и %s либо заведён под другой трекер: "+
			"задача не может быть взята в работу", key, ProjectsFile, ProjectsLocalFile)
	}
	return project, nil
}

// LoadProjects собирает проекты из двух половин: офисной и машинной.
//
// officePath лежит в конфиг-репозитории и называет проекты офиса, machinePath —
// в ${OFFICE_HOME} и говорит, где всё это на этой машине. Склейка поверхностная
// и по ключу проекта: половины не пересекаются, поэтому спорить им не о чем.
//
// Оба файла обязательны, и оба сверяются друг с другом по списку ключей.
// Проект, названный офисом и не заведённый на машине, — отказ, а не пропуск:
// пропущенный молча, он выглядел бы как проект, по которому просто нет задач,
// и искать причину пришлось бы неделю. Проект, заведённый на машине и не названный
// офисом, — тоже отказ: офис описан в репозитории, а так ловится опечатка в имени.
func LoadProjects(officePath, machinePath string) (Projects, error) {
	if err := checkOfficeHalf(officePath); err != nil {
		return nil, err
	}

	var office map[string]officeProject
	if err := decodeStrict(officePath, &office); err != nil {
		return nil, err
	}

	// Офис без единого проекта — тоже отказ, и это не педантизм: прочие беды
	// склейки говорят вслух, а «ни одного проекта» промолчало бы, и раннер крутил бы
	// пустые тики, ничего не объясняя.
	//
	// Проверка стоит раньше машинной половины намеренно: иначе отказ советовал бы
	// «дать каждому проекту из projects.yaml ключи», когда проектов там ни одного.
	if len(office) == 0 {
		return nil, fmt.Errorf("%s не называет ни одного проекта: офису нечего вести", officePath)
	}

	if _, err := os.Stat(machinePath); errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s не заведён: офис описан репозиторием, а где его проекты "+
			"на этой машине — узнать неоткуда. Заведите файл, дав каждому проекту из %s "+
			"ключи repo_url и tracker; worktree_root и forge необязательны",
			machinePath, officePath)
	}
	var machine map[string]machineProject
	if err := decodeStrict(machinePath, &machine); err != nil {
		return nil, err
	}

	var errs []error
	for key := range machine {
		if _, named := office[key]; !named {
			errs = append(errs, fmt.Errorf("%s: проект не назван в %s — офис описан репозиторием, "+
				"а на машине его только заводят; если это опечатка в имени, она здесь и видна",
				key, officePath))
		}
	}

	projects := Projects{}
	for key, half := range office {
		local, found := machine[key]
		if !found {
			errs = append(errs, fmt.Errorf("%s: проект назван в %s, но не заведён в %s — "+
				"неоткуда взять ни репозиторий, ни трекер",
				key, officePath, machinePath))
			continue
		}
		projects[key] = Project{
			RepoURL:       local.RepoURL,
			DefaultBranch: half.DefaultBranch,
			BranchPrefix:  half.BranchPrefix,
			WorktreeRoot:  local.WorktreeRoot,
			Tracker:       local.Tracker,
			Forge:         local.Forge,
		}
	}

	for key, project := range projects {
		if project.RepoURL == "" {
			errs = append(errs, fmt.Errorf("%s: repo_url не задан (%s)", key, machinePath))
		}
		if project.DefaultBranch == "" {
			errs = append(errs, fmt.Errorf("%s: default_branch не задан (%s)", key, officePath))
		}
		if project.BranchPrefix == "" {
			errs = append(errs, fmt.Errorf("%s: branch_prefix не задан (%s)", key, officePath))
		}
		if root := project.WorktreeRoot; root != "" && !filepath.IsAbs(root) {
			errs = append(errs, fmt.Errorf("%s: worktree_root=%q должен быть абсолютным (%s)", key, root, machinePath))
		}
		if !slices.Contains(trackers, project.Tracker) {
			errs = append(errs, fmt.Errorf("%s: tracker=%q, ожидается один из %v (%s)",
				key, project.Tracker, trackers, machinePath))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("проекты нарушают контракт: %w", err)
	}
	return projects, nil
}

// checkOfficeHalf ловит машинный ключ, забредший в файл офиса, и объясняет,
// куда он переехал.
//
// Строгий разбор отверг бы его и сам — поля в officeProject нет, — но сказал бы
// «неизвестное поле repo_url», и человек пошёл бы искать опечатку. Здесь ошибка
// называет причину: ключ описывает машину, а не офис.
func checkOfficeHalf(path string) error {
	var raw map[string]map[string]any
	if err := decodeLoose(path, &raw); err != nil {
		return err
	}
	var errs []error
	for _, key := range slices.Sorted(maps.Keys(raw)) {
		for _, field := range machineKeys {
			if _, found := raw[key][field]; found {
				errs = append(errs, fmt.Errorf("%s: ключ %s описывает машину, а не офис — "+
					"его место в %s", key, field, ProjectsLocalFile))
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("%s нарушает границу конфигурации: %w", path, err)
	}
	return nil
}

// decodeLoose читает YAML как есть, ничего не проверяя. Нужен там, где отказ
// должен объяснить причину сам, а строгий разбор сказал бы только «неизвестное
// поле».
func decodeLoose(path string, into any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%s не прочитан: %w", path, err)
	}
	if err := yaml.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("%s не разобран: %w", path, err)
	}
	return nil
}

// decodeStrict читает YAML, отвергая неизвестные поля.
func decodeStrict(path string, into any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%s не прочитан: %w", path, err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	// Пустой документ — это io.EOF, и жаловаться на него здесь нечем: «не разобран:
	// EOF» человеку не говорит ничего. Пусть цель останется нулевой, а объяснит
	// проверка выше — она назовёт, чего именно не хватает. Файл из одних
	// комментариев и файл, заведённый и ещё не заполненный, — обычные состояния
	// на новой машине.
	if err := dec.Decode(into); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s не разобран: %w", path, err)
	}
	return nil
}
