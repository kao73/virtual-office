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
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kao73/virtual-office/internal/runner"
)

// Файлы конфигурации. Граф переходов описывает офис и живёт в репозитории;
// проекты описывают инстанс и живут в хозяйстве раннера. Граница — не вкус
// раскладки: репозиторий — это фреймворк, и всё, что правят, заводя новую
// машину, обязано лежать вне его, иначе правка под себя пачкает рабочее
// дерево и метка config:…-dirty перестаёт что-либо значить.
const (
	// WorkflowFile — граф переходов. Его читает раннер; агент его не видит.
	// Описывает офис: одинаков у всех, кто его поднимет.
	WorkflowFile = "workflow.yaml"
	// ProjectsLocalFile — единственный файл проектов: где репозиторий, ветка
	// по умолчанию, трекер, forge, добавки к правилам. Живёт в ${OFFICE_HOME}.
	ProjectsLocalFile = "projects.local.yaml"
	// ProjectsLocalExampleFile — образец ProjectsLocalFile. Лежит в репозитории
	// и в поставке бинарника; `runner init` кладёт его в ${OFFICE_HOME}, откуда
	// его копируют под рабочее имя. Раннер образец не читает никогда.
	ProjectsLocalExampleFile = "projects.local.example.yaml"
	// OfficeProjectsFile — имя файла, которого в репозитории больше нет.
	// Остался от прежней раскладки — раннер о нём скажет, а не промолчит
	// (RefuseLeftoverOfficeFile).
	OfficeProjectsFile = "projects.yaml"
)

// DefaultBranchPrefix — префикс веток задач, если проект не назвал свой.
const DefaultBranchPrefix = "agent/"

// outcomes — исходы, для которых граф обязан задать переход. Список берётся
// из контракта «раннер ↔ агент»: собственный разъехался бы с ним.
var outcomes = []string{
	string(runner.OutcomeDone),
	string(runner.OutcomeNeedsHuman),
	string(runner.OutcomeBlocked),
	string(runner.OutcomeFailed),
	string(runner.OutcomeSplit),
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
	MaxIdleRuns int `yaml:"max_idle_runs"`
	// MaxMergeRefusals — сколько раз подряд forge может отказать в мерже
	// при локально чистом состоянии (auto_merge.enabled), прежде чем задачу
	// отдадут человеку. Отдельный предел по той же причине, что у push
	// failures: это не работа implementer'а — локально мержить нечего.
	MaxMergeRefusals int `yaml:"max_merge_refusals"`
	// MaxPRReturns — сколько раз подряд PR-проход может вернуть задачу, не
	// сдвинув её вперёд (продвинувшейся базой или отказом forge в мерже),
	// прежде чем отдать её человеку. Общий счётчик по обоим видам — см.
	// tracker.PRReturns: два раздельных предела чередование обошло бы.
	MaxPRReturns int `yaml:"max_pr_returns"`
	// MaxMergePendingSec — сколько секунд подряд GitHub может отвечать, что сам
	// ещё не решил, годится ли pull request к слиянию (forge.ErrNotReady: не
	// прошли обязательные проверки, не дано обязательное ревью), прежде чем
	// задачу отдадут человеку. В секундах, а не тиках: длительность CI не
	// привязана к частоте тика (--every), а mergePending
	// (internal/pipeline/prpass.go) пишет одну запись на весь эпизод, не одну
	// на тик, — тик посчитать было бы нечем. Отдельный от max_merge_refusals
	// и заметно терпеливее — должен вытерпеть обычный CI, а не спутать
	// «ещё не закончилось» с окончательным отказом.
	MaxMergePendingSec int `yaml:"max_merge_pending_sec"`
	LeaseMarginSec     int `yaml:"lease_margin_sec"`
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
	// Оба предела нужны только там, где вообще есть PR-проход: граф без блока
	// pr — это офис, который PR не открывает (forge.go), и заставлять такой
	// граф объявлять пределы, которые он никогда не проверит, значило бы
	// требовать от него лишнего вопреки собственному правилу checkPR — блок
	// необязателен целиком.
	if w.PR.Set() && w.Limits.MaxMergeRefusals <= 0 {
		errs = append(errs, fmt.Errorf("limits.max_merge_refusals=%d: ожидается положительное число", w.Limits.MaxMergeRefusals))
	}
	if w.PR.Set() && w.Limits.MaxPRReturns <= 0 {
		errs = append(errs, fmt.Errorf("limits.max_pr_returns=%d: ожидается положительное число", w.Limits.MaxPRReturns))
	}
	if w.PR.Set() && w.Limits.MaxMergePendingSec <= 0 {
		errs = append(errs, fmt.Errorf("limits.max_merge_pending_sec=%d: ожидается положительное число", w.Limits.MaxMergePendingSec))
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
// его спрашивает LoadProjects, и разъехаться этим двум местам нельзя.
var trackers = []string{"mock", "jira"}

// Trackers — имена трекеров для подсказок и сообщений об ошибке.
func Trackers() []string { return slices.Clone(trackers) }

// Rules — сетевой и инструментальный слой одной записи ProjectsLocalFile:
// машинного `defaults` или проекта. Два других слоя сюда не входят: базовый
// (roles/_base/base.yaml) кладёт в роль LoadRole в форме role.yaml, а роль
// несёт собственные Network/Tools из runner.Role и сливается с этим слоем
// отдельным шагом — см. MergeProjectRules (tracker/rules.go), а не здесь.
type Rules struct {
	Network []string     `yaml:"network"`
	Tools   runner.Tools `yaml:"tools"`
}

// errors — нарушения контракта правил в этом слое, подписанные именем
// записи (defaults или проект) и файлом: сам контракт общий для всех
// четырёх слоёв (runner.RulesErrors), контекст — свой у каждого.
func (r Rules) errors(entry, path string) []error {
	var errs []error
	for _, err := range runner.RulesErrors(runner.Network{Allow: r.Network}, r.Tools) {
		errs = append(errs, fmt.Errorf("%s: %w (%s)", entry, err, path))
	}
	return errs
}

// AutoMerge — доверие конкретного инстанса конкретному проекту: мержить ли
// самим и куда. Решение машины, не офиса — тот же класс, что Forge.
//
// Enabled и TargetBranch — независимые решения, не одна опция с двумя полями:
// TargetBranch действует и без Enabled (см. Project.PRBranch) — так проект
// с изолированной интеграционной веткой заводит её базой прохода уже сегодня,
// сливая по-прежнему сам, а включает auto_merge отдельным шагом, когда будет
// готов.
type AutoMerge struct {
	Enabled      bool   `yaml:"enabled"`
	TargetBranch string `yaml:"target_branch"`
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
	// в файловом трекере и в JIRA, а офис одного трекера не должен видеть
	// чужих проектов.
	Tracker string `yaml:"tracker"`
	// Forge — куда открывать pull request. Пусто — forge у проекта нет:
	// PR-проход вырождается, но маршрут остаётся тем же (см. workflow.yaml: pr).
	Forge string `yaml:"forge"`
	// AutoMerge — сливает ли офис pull request сам, и в какую ветку. Пусто
	// (Enabled: false) — сегодняшнее поведение: сливает человек.
	AutoMerge AutoMerge `yaml:"auto_merge"`
	// Network — машинный (defaults) и проектный слои, уже объединённые
	// LoadProjects. Базовый слой (roles/_base/base.yaml) сюда не входит —
	// он уже в роли из LoadRole, — а роль добавляет MergeProjectRules
	// ближе к месту запуска.
	Network []string
	// Tools — то же самое для tools.allow/tools.deny.
	Tools runner.Tools
}

// machineProject — запись проекта в ProjectsLocalFile. Всё, что у проекта
// есть, — здесь: второй половины больше нет. Отдельный от Project тип нужен
// потому, что Network/Tools в Project — уже слитые слои, не сырые поля
// файла: разбирать файл прямо в него значило бы путать одно с другим.
type machineProject struct {
	RepoURL       string    `yaml:"repo_url"`
	DefaultBranch string    `yaml:"default_branch"`
	BranchPrefix  string    `yaml:"branch_prefix"`
	WorktreeRoot  string    `yaml:"worktree_root"`
	Tracker       string    `yaml:"tracker"`
	Forge         string    `yaml:"forge"`
	AutoMerge     AutoMerge `yaml:"auto_merge"`
	Rules         `yaml:",inline"`
}

// projectKeys — ключи записи проекта, ровно теги machineProject. Нужны
// свободному разбору: строгий отверг бы лишний ключ и сам, но назвал бы
// только поле и строку, а человеку нужен проект, ключ и файл.
var projectKeys = []string{
	"repo_url", "default_branch", "branch_prefix", "worktree_root",
	"tracker", "forge", "auto_merge", "network", "tools",
}

// Projects — проекты по ключу трекера.
type Projects map[string]Project

// Branch — ветка задачи.
func (p Project) Branch(key string) string { return p.BranchPrefix + key }

// PRBranch — ветка, от которой форкаются задачи, и цель PR-прохода:
// target_branch авто-мержа, а без него — default_branch. Не default_branch
// впрямую: иначе задача B (depends_on A) форкалась бы от main и не видела бы
// уже влитую в интеграционную ветку работу A — гейт зависимостей
// (claim(), internal/pipeline/deps.go) молча переставал бы что-либо значить.
func (p Project) PRBranch() string {
	if p.AutoMerge.TargetBranch != "" {
		return p.AutoMerge.TargetBranch
	}
	return p.DefaultBranch
}

// Keys — ключи проектов по порядку. Порядок устойчивый: обход проектов не должен
// зависеть от того, как в этот раз лёг хеш.
func (p Projects) Keys() []string { return slices.Sorted(maps.Keys(p)) }

// For — проекты одного трекера.
//
// Офис ведёт один трекер и работает только со своими проектами. Чужие
// не просто бесполезны: спрашивать о них трекер — значит получать ошибку
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

// TrackersInUse — трекеры, названные проектами, по алфавиту и без повторов.
// Порядок устойчив намеренно: по нему раннер обходит офисы, и два запуска
// обязаны обходить их одинаково.
func (p Projects) TrackersInUse() []string {
	var names []string
	for _, project := range p {
		names = append(names, project.Tracker)
	}
	return runner.Union(names)
}

// Get отдаёт проект по ключу. Задачу неизвестного проекта раннер брать не вправе:
// ему негде взять репозиторий и некуда пушить.
func (p Projects) Get(key string) (Project, error) {
	project, found := p[key]
	if !found {
		return Project{}, fmt.Errorf("проект %q не описан в %s: задача не может быть взята в работу",
			key, ProjectsLocalFile)
	}
	return project, nil
}

// reservedRulesKey — имя, под которым в ProjectsLocalFile живёт машинный
// слой умолчаний network/tools. Не проект: требования к обычным записям
// к нему не применяются, а ключи проекта под ним — ошибка.
const reservedRulesKey = "defaults"

// defaultsKeys — всё, что defaults вправе содержать. Один allow-list вместо
// перечня запрещённого: новое поле в machineProject не сможет снова
// проскочить под defaults молча, как проскакивал auto_merge.
var defaultsKeys = []string{"network", "tools"}

// checkKeys сверяет ключи каждой записи со списком дозволенных — до строгого
// разбора, потому что строгий назвал бы только поле, а человеку нужно знать
// проект, ключ и файл; для defaults — ещё и почему ключ не положен.
func checkKeys(path string, raw map[string]map[string]any) error {
	var errs []error
	for _, entry := range slices.Sorted(maps.Keys(raw)) {
		for _, key := range slices.Sorted(maps.Keys(raw[entry])) {
			switch {
			case entry == reservedRulesKey && !slices.Contains(defaultsKeys, key):
				errs = append(errs, fmt.Errorf("%s: ключ %s не положен — это свойство проекта, "+
					"а не умолчаний; здесь только %s", reservedRulesKey, key, strings.Join(defaultsKeys, " и ")))
			case entry != reservedRulesKey && !slices.Contains(projectKeys, key):
				errs = append(errs, fmt.Errorf("%s: ключ %s не описан контрактом проекта; известны %s",
					entry, key, strings.Join(projectKeys, ", ")))
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// LoadProjects читает проекты из единственного файла — машинного.
//
// Репозиторий — это фреймворк: проектов в нём нет, и всё, что у проекта есть,
// лежит в одной записи ProjectsLocalFile. Слои правил объединяются: базовый
// (roles/_base/base.yaml) кладёт в роль LoadRole, машинный (defaults здесь)
// и проектный (сама запись) — этот загрузчик; назвать можно, убрать — нет.
func LoadProjects(machinePath string) (Projects, error) {
	if _, err := os.Stat(machinePath); errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s не заведён: проекты — свойство инстанса, а не офиса, "+
			"и где они на этой машине — узнать неоткуда. Заведите файл, дав каждому проекту "+
			"ключи repo_url, tracker и default_branch; branch_prefix, worktree_root, forge, "+
			"auto_merge, network и tools необязательны", machinePath)
	}

	// Сперва свободный разбор — ради имён: строгий принял бы под defaults
	// любое поле machineProject, и лишнее там молча пропало бы (так пропадал
	// auto_merge), а лишний ключ записи назвал бы без имени проекта.
	var raw map[string]map[string]any
	if err := decodeLoose(machinePath, &raw); err != nil {
		return nil, err
	}
	if err := checkKeys(machinePath, raw); err != nil {
		return nil, err
	}

	var machine map[string]machineProject
	if err := decodeStrict(machinePath, &machine); err != nil {
		return nil, err
	}
	defaults := machine[reservedRulesKey].Rules
	delete(machine, reservedRulesKey)

	// Машинный и проектный слои держат тот же контракт правил, что роль
	// и база: запрет Write целиком или хост-URL здесь молча ушли бы в каждую
	// роль через объединение, и убрать их было бы некому.
	errs := defaults.errors(reservedRulesKey, machinePath)

	// Ни одного проекта — отказ, и это не педантизм: прочие беды говорят вслух,
	// а «ни одного проекта» промолчало бы, и раннер крутил бы пустые тики.
	// Беды defaults идут в тот же отказ: починив одну, узнать о второй
	// следующим запуском — лишний круг.
	if len(machine) == 0 {
		errs = append(errs, fmt.Errorf("%s не называет ни одного проекта: офису нечего вести", machinePath))
	}

	projects := Projects{}
	for _, key := range slices.Sorted(maps.Keys(machine)) {
		local := machine[key]
		errs = append(errs, local.Rules.errors(key, machinePath)...)
		if local.RepoURL == "" {
			errs = append(errs, fmt.Errorf("%s: repo_url не задан (%s)", key, machinePath))
		}
		if local.DefaultBranch == "" {
			errs = append(errs, fmt.Errorf("%s: default_branch не задан (%s)", key, machinePath))
		}
		// Пустой префикс здесь, а не в Branch(): проект никогда не увидит
		// ветку без префикса, и умолчание записано в одном месте.
		if local.BranchPrefix == "" {
			local.BranchPrefix = DefaultBranchPrefix
		}
		if root := local.WorktreeRoot; root != "" && !filepath.IsAbs(root) {
			errs = append(errs, fmt.Errorf("%s: worktree_root=%q должен быть абсолютным (%s)", key, root, machinePath))
		}
		if !slices.Contains(trackers, local.Tracker) {
			errs = append(errs, fmt.Errorf("%s: tracker=%q, ожидается один из %v (%s)",
				key, local.Tracker, trackers, machinePath))
		}
		if local.AutoMerge.Enabled && local.Forge == "" {
			errs = append(errs, fmt.Errorf(
				"%s: auto_merge.enabled=true, но forge не задан — мержить через API "+
					"некуда (%s)", key, machinePath))
		}
		projects[key] = Project{
			RepoURL:       local.RepoURL,
			DefaultBranch: local.DefaultBranch,
			BranchPrefix:  local.BranchPrefix,
			WorktreeRoot:  local.WorktreeRoot,
			Tracker:       local.Tracker,
			Forge:         local.Forge,
			AutoMerge:     local.AutoMerge,
			Network:       runner.Union(defaults.Network, local.Network),
			Tools: runner.Tools{
				Allow: runner.Union(defaults.Tools.Allow, local.Tools.Allow),
				Deny:  runner.Union(defaults.Tools.Deny, local.Tools.Deny),
			},
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("проекты нарушают контракт: %w", err)
	}
	return projects, nil
}

// RefuseLeftoverOfficeFile — отказ, если под корнем конфигурации лежит
// projects.yaml. Проекты живут в projects.local.yaml, общие правила —
// в roles/_base/base.yaml; файл, который раньше носил и то и другое,
// не читается, и молча пройти мимо него значило бы запустить офис на
// половине конфигурации: с проектами, но без правил, что в нём лежали.
func RefuseLeftoverOfficeFile(configRoot string) error {
	path := filepath.Join(configRoot, OfficeProjectsFile)
	_, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("%s не проверен: %w", path, err)
	}
	return fmt.Errorf("%s: этот файл больше не читается. Проекты живут в ${OFFICE_HOME}/%s, "+
		"общие правила ролей — в %s; уберите файл", path, ProjectsLocalFile,
		filepath.Join(runner.RolesDir, runner.BaseDir, runner.BaseRulesFile))
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
