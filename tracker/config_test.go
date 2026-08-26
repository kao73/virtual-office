package tracker

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Файлы графа и проектов лежат в конфиг-репозитории и едут в прод как есть.
// Битый workflow.yaml иначе обнаружился бы в проде первым же tick'ом.
func TestShippedConfigIsValid(t *testing.T) {
	root := filepath.Join("..")

	wf, err := LoadWorkflow(filepath.Join(root, WorkflowFile))
	if err != nil {
		t.Fatalf("%s не загружен: %v", WorkflowFile, err)
	}
	role, err := wf.Role("implementer")
	if err != nil {
		t.Fatalf("роль implementer не найдена: %v", err)
	}
	if role.ReadsFrom == "" || role.Working == "" {
		t.Errorf("роль implementer описана не полностью: %+v", role)
	}
	if wf.Limits.MaxAttempts <= 0 {
		t.Errorf("max_attempts = %d", wf.Limits.MaxAttempts)
	}

	// Машинную половину репозиторий не хранит и хранить не должен, поэтому
	// здесь проверяется только то, что его половина разбирается и границу
	// не нарушает.
	if err := checkOfficeHalf(filepath.Join(root, ProjectsFile)); err != nil {
		t.Error(err)
	}
	var office map[string]officeProject
	if err := decodeStrict(filepath.Join(root, ProjectsFile), &office); err != nil {
		t.Fatalf("%s не разобран: %v", ProjectsFile, err)
	}
	// Ключ defaults в реальном файле обязан пройти те же проверки, что
	// и синтетический: не содержать default_branch/branch_prefix,
	// не участвовать в парности office/machine.
	if _, err := extractDefaultsOffice(office); err != nil {
		t.Errorf("defaults в %s не проходит проверку: %v", ProjectsFile, err)
	}
	if len(office) == 0 {
		t.Error("список проектов пуст: раннеру нечего брать в работу")
	}
}

func writeTemp(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("%s не записан: %v", name, err)
	}
	return path
}

const validWorkflow = `statuses: [Ready, InProgress, Review, Blocked, Done]
roles:
  implementer:
    reads_from: Ready
    working: InProgress
    outcomes:
      done:        { to: Review }
      needs_human: { to: Blocked, human: true }
      blocked:     { to: Ready, attempts: +1 }
      failed:      { to: Ready, attempts: +1 }
limits:
  max_attempts: 3
  max_lease_expiries: 3
  max_push_failures: 3
  max_idle_runs: 3
  lease_margin_sec: 300
human_reply:
  fallback: Ready
  reset_attempts: true
`

// Граф этапа 3: две роли, у одной рабочего статуса нет, маршрут исхода `done`
// зависит от того, кому агент передаёт задачу.
const twoRoleWorkflow = `statuses: [Backlog, Ready, InProgress, Review, Approved, Blocked]
terminal: [Approved]
tick_order: [reviewer, implementer]
roles:
  implementer:
    reads_from: Ready
    working: InProgress
    outcomes:
      done:        { to: Review }
      needs_human: { to: Blocked, human: true }
      blocked:     { to: Ready, attempts: +1 }
      failed:      { to: Ready, attempts: +1 }
  reviewer:
    reads_from: Review
    outcomes:
      done:
        to: Approved
        by_next_owner:
          implementer: Ready
          human: Approved
      needs_human: { to: Blocked, human: true }
      blocked:     { to: Review, attempts: +1 }
      failed:      { to: Review, attempts: +1 }
limits:
  max_attempts: 3
  max_lease_expiries: 3
  max_push_failures: 3
  max_idle_runs: 3
  max_return_rounds: 3
  lease_margin_sec: 300
human_reply:
  fallback: Ready
  reset_attempts: true
`

// Рабочий статус — опция роли. У implementer'а он есть: «в работе» на доске
// ждут увидеть. У reviewer'а его нет — проверка длится минуты, и отдельный статус
// под неё была бы шумом; «сейчас смотрят» там означает живую аренду.
func TestLoadWorkflowWithOptionalWorking(t *testing.T) {
	wf, err := LoadWorkflow(writeTemp(t, WorkflowFile, twoRoleWorkflow))
	if err != nil {
		t.Fatalf("граф не загружен: %v", err)
	}

	reviewer, err := wf.Role("reviewer")
	if err != nil {
		t.Fatalf("роль reviewer не найдена: %v", err)
	}
	if reviewer.Working != "" {
		t.Errorf("рабочий статус reviewer'а %q, ожидался пустой", reviewer.Working)
	}
	if implementer, _ := wf.Role("implementer"); implementer.Working != "InProgress" {
		t.Errorf("рабочий статус implementer'а %q, ожидался InProgress", implementer.Working)
	}
}

// Порядок обхода ролей задан явно: порядок YAML-карты не сохраняется, и «по
// алфавиту» — совпадение, а не правило. Сначала разгрузить конвейер, потом
// брать новое.
func TestTickOrder(t *testing.T) {
	wf, err := LoadWorkflow(writeTemp(t, WorkflowFile, twoRoleWorkflow))
	if err != nil {
		t.Fatalf("граф не загружен: %v", err)
	}
	if got := wf.Order(); !slices.Equal(got, []string{"reviewer", "implementer"}) {
		t.Errorf("порядок ролей %v, ожидался [reviewer implementer]", got)
	}

	// Роль одна — порядку неоткуда взяться и незачем его требовать.
	single, err := LoadWorkflow(writeTemp(t, WorkflowFile, validWorkflow))
	if err != nil {
		t.Fatalf("граф с одной ролью не загружен: %v", err)
	}
	if got := single.Order(); !slices.Equal(got, []string{"implementer"}) {
		t.Errorf("порядок ролей %v, ожидался [implementer]", got)
	}
}

// Маршрут исхода `done` у reviewer'а зависит от того, кому агент передаёт
// задачу. Карта явная: маршрут задаёт граф, а не агент (DESIGN §2.1) — значение,
// которого в карте нет, уезжает по умолчанию.
func TestRouteByNextOwner(t *testing.T) {
	wf, err := LoadWorkflow(writeTemp(t, WorkflowFile, twoRoleWorkflow))
	if err != nil {
		t.Fatalf("граф не загружен: %v", err)
	}
	reviewer, _ := wf.Role("reviewer")
	done := reviewer.Outcomes["done"]

	cases := map[string]string{
		"implementer": "Ready",    // вернул на доработку
		"human":       "Approved", // одобрил
		"none":        "Approved", // ничего не сказал — маршрут по умолчанию
		"planner":     "Approved", // роли нет в карте — тоже по умолчанию
	}
	for next, want := range cases {
		if got := done.Route(next); got != want {
			t.Errorf("next_owner=%s ведёт в %q, ожидалось %q", next, got, want)
		}
	}

	if !wf.IsTerminal("Approved") {
		t.Error("в этом графе Approved объявлена терминальной, а IsTerminal её не признаёт")
	}
	if wf.IsTerminal("Review") {
		t.Error("Review сочтена терминальной")
	}
}

// Задача с вопросом уходит в статус ожидания, и туда же её отправляет раннер,
// заблокировав по своим причинам. Безролевому проходу разбора ответов набор
// таких колонок нужен целиком: роли у него нет, а искать ожидающие задачи
// где-то надо. Повторов в наборе быть не должно — две роли вправе ждать человека
// в одном статусе.
func TestHumanStatuses(t *testing.T) {
	wf, err := LoadWorkflow(writeTemp(t, WorkflowFile, twoRoleWorkflow))
	if err != nil {
		t.Fatalf("граф не загружен: %v", err)
	}
	if got := wf.HumanStatuses(); !slices.Equal(got, []string{"Blocked"}) {
		t.Errorf("статусы ожидания %v, ожидался один Blocked", got)
	}
}

// Статус, на который не ссылается ни одна роль, — не ошибка: Backlog и Done
// человеческие, офис их не читает и в них не пишет, но `runner ls` показывает
// задачи и в них.
func TestLoadWorkflowAllowsHumanStatuses(t *testing.T) {
	yaml := strings.Replace(twoRoleWorkflow,
		"statuses: [Backlog, Ready, InProgress, Review, Approved, Blocked]",
		"statuses: [Backlog, Ready, InProgress, Review, Approved, Done, Blocked]", 1)
	if _, err := LoadWorkflow(writeTemp(t, WorkflowFile, yaml)); err != nil {
		t.Fatalf("граф с человеческим статусом не загружен: %v", err)
	}
}

// Разбор графа двух ролей так же строг, как разбор графа одной: всё, что может
// разъехаться молча, обязано ломаться на загрузке.
func TestLoadWorkflowRejectsBrokenTwoRoleGraph(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "порядок обхода не назван при двух ролях",
			yaml: strings.Replace(twoRoleWorkflow, "tick_order: [reviewer, implementer]\n", "", 1),
			want: "tick_order",
		},
		{
			name: "в порядке обхода забыта роль",
			yaml: strings.Replace(twoRoleWorkflow, "tick_order: [reviewer, implementer]", "tick_order: [reviewer]", 1),
			want: "implementer",
		},
		{
			name: "в порядке обхода роль дважды",
			yaml: strings.Replace(twoRoleWorkflow, "tick_order: [reviewer, implementer]", "tick_order: [reviewer, reviewer, implementer]", 1),
			want: "reviewer",
		},
		{
			name: "в порядке обхода незнакомая роль",
			yaml: strings.Replace(twoRoleWorkflow, "tick_order: [reviewer, implementer]", "tick_order: [reviewer, implementer, planner]", 1),
			want: "planner",
		},
		{
			name: "терминальный статус выдуман",
			yaml: strings.Replace(twoRoleWorkflow, "terminal: [Approved]", "terminal: [Готово]", 1),
			want: "Готово",
		},
		{
			name: "рабочий статус выдуман",
			yaml: strings.Replace(twoRoleWorkflow, "working: InProgress", "working: Работаю", 1),
			want: "Работаю",
		},
		{
			// Маршрут по next_owner — только у done: остальные исходы задачу
			// никому не передают, и разветвлять их нечем.
			name: "маршрут по next_owner не у done",
			yaml: strings.Replace(twoRoleWorkflow, "      needs_human: { to: Blocked, human: true }\n      blocked:     { to: Review, attempts: +1 }",
				"      needs_human: { to: Blocked, human: true, by_next_owner: { human: Blocked } }\n      blocked:     { to: Review, attempts: +1 }", 1),
			want: "by_next_owner",
		},
		{
			name: "маршрут ведёт в несуществующий статус",
			yaml: strings.Replace(twoRoleWorkflow, "          implementer: Ready", "          implementer: Todo", 1),
			want: "Todo",
		},
		{
			// Ключ, совпадающий с именем роли, обязан вести в её очередь. Иначе
			// карта и reads_from разъедутся, и задача уедет туда, где её роль
			// не ищет.
			name: "маршрут роли мимо её очереди",
			yaml: strings.Replace(twoRoleWorkflow, "          implementer: Ready", "          implementer: Review", 1),
			want: "implementer",
		},
		{
			// Круги «правки → ревью» возможны там, где есть маршрут по next_owner.
			// Без предела задача ходила бы между ролями вечно.
			name: "предел кругов не задан",
			yaml: strings.Replace(twoRoleWorkflow, "max_return_rounds: 3", "max_return_rounds: 0", 1),
			want: "max_return_rounds",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadWorkflow(writeTemp(t, WorkflowFile, tc.yaml))
			if err == nil {
				t.Fatal("битый граф загружен без ошибки")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("в ошибке не назван %q: %v", tc.want, err)
			}
		})
	}
}

func TestLoadWorkflow(t *testing.T) {
	wf, err := LoadWorkflow(writeTemp(t, WorkflowFile, validWorkflow))
	if err != nil {
		t.Fatalf("граф не загружен: %v", err)
	}

	role, err := wf.Role("implementer")
	if err != nil {
		t.Fatalf("роль не найдена: %v", err)
	}
	// `attempts: +1` — дельта счётчика попыток, а не его значение.
	if got := role.Outcomes["failed"].Attempts; got != 1 {
		t.Errorf("attempts = %d, ожидалась дельта 1", got)
	}
	if !role.Outcomes["needs_human"].Human {
		t.Error("needs_human не помечен как требующий человека")
	}
	if got := wf.LeaseMargin(); got != 5*time.Minute {
		t.Errorf("запас аренды %s, ожидалось 5m", got)
	}
	if _, err := wf.Role("нет-такой"); err == nil {
		t.Error("несуществующая роль найдена")
	}
}

// Разбор строгий по той же причине, что у роли и у результата: опечатка,
// проглоченная молча, превращается в потерянную настройку.
func TestLoadWorkflowRejectsBrokenGraph(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "неизвестное поле",
			yaml: strings.Replace(validWorkflow, "limits:", "limmits:", 1),
			want: "limmits",
		},
		{
			name: "исход ведёт в несуществующий статус",
			yaml: strings.Replace(validWorkflow, "done:        { to: Review }", "done:        { to: Готово }", 1),
			want: "Готово",
		},
		{
			name: "роль читает из несуществующего статуса",
			yaml: strings.Replace(validWorkflow, "reads_from: Ready", "reads_from: Todo", 1),
			want: "Todo",
		},
		{
			// Исход, для которого не задан переход, — это задача, застрявшая
			// в InProgress без объяснений.
			name: "исход без перехода",
			yaml: strings.Replace(validWorkflow, "      blocked:     { to: Ready, attempts: +1 }\n", "", 1),
			want: "blocked",
		},
		{
			name: "запасной маршрут ответа ведёт в никуда",
			yaml: strings.Replace(validWorkflow, "human_reply:\n  fallback: Ready", "human_reply:\n  fallback: Todo", 1),
			want: "Todo",
		},
		{
			name: "попыток не бывает",
			yaml: strings.Replace(validWorkflow, "max_attempts: 3", "max_attempts: 0", 1),
			want: "max_attempts",
		},
		{
			name: "предел смертей прогона не задан",
			yaml: strings.Replace(validWorkflow, "max_lease_expiries: 3", "max_lease_expiries: 0", 1),
			want: "max_lease_expiries",
		},
		{
			name: "предел неудачных пушей не задан",
			yaml: strings.Replace(validWorkflow, "max_push_failures: 3", "max_push_failures: 0", 1),
			want: "max_push_failures",
		},
		{
			name: "предел пустых прогонов не задан",
			yaml: strings.Replace(validWorkflow, "max_idle_runs: 3", "max_idle_runs: 0", 1),
			want: "max_idle_runs",
		},
		{
			// Без предела прогон без результата возвращал бы задачу той же роли
			// вечно, не тратя попыток и не зовя человека: попытки его
			// не ограничивают, на то он и отдельная серия.
			name: "предела пустых прогонов нет вовсе",
			yaml: strings.Replace(validWorkflow, "  max_idle_runs: 3\n", "", 1),
			want: "max_idle_runs",
		},
		{
			name: "статусы не заданы",
			yaml: strings.Replace(validWorkflow, "statuses: [Ready, InProgress, Review, Blocked, Done]", "statuses: []", 1),
			want: "statuses",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadWorkflow(writeTemp(t, WorkflowFile, tc.yaml))
			if err == nil {
				t.Fatal("битый граф загружен без ошибки")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("в ошибке не назван %q: %v", tc.want, err)
			}
		})
	}
}

// Две половины одного проекта: офисная живёт в репозитории, машинная —
// в ${OFFICE_HOME}.
const (
	validOffice = `OFF:
  default_branch: master
  branch_prefix: agent/
`
	validMachine = `OFF:
  repo_url: https://example.test/office.git
  tracker: mock
`
)

// loadHalves — проекты, собранные из двух временных файлов.
func loadHalves(t *testing.T, office, machine string) (Projects, error) {
	t.Helper()
	return LoadProjects(writeTemp(t, ProjectsFile, office), writeTemp(t, ProjectsLocalFile, machine))
}

func TestLoadProjects(t *testing.T) {
	projects, err := loadHalves(t, validOffice, validMachine)
	if err != nil {
		t.Fatalf("проекты не загружены: %v", err)
	}

	p, err := projects.Get("OFF")
	if err != nil {
		t.Fatalf("проект OFF не найден: %v", err)
	}
	// Обе половины доехали: ветка собрана из офисной, репозиторий и трекер —
	// из машинной. Иначе склейка могла бы терять половину молча.
	if got := p.Branch("OFF-12"); got != "agent/OFF-12" {
		t.Errorf("ветка %q, ожидалась agent/OFF-12", got)
	}
	if p.RepoURL != "https://example.test/office.git" {
		t.Errorf("repo_url из машинной половины не доехал: %q", p.RepoURL)
	}
	if p.Tracker != "mock" {
		t.Errorf("tracker из машинной половины не доехал: %q", p.Tracker)
	}

	// Задачу неизвестного проекта раннер брать не вправе: ему негде взять
	// репозиторий и некуда пушить.
	if _, err := projects.Get("НЕТ"); err == nil {
		t.Error("неизвестный проект найден")
	}
}

// Раннер запускается с одним трекером и работает только со своими проектами:
// чужие он не спрашивает (иначе трекер отвечает «нет такого проекта» на каждом
// проходе) и их рабочие папки не убирает.
func TestProjectsFor(t *testing.T) {
	projects := Projects{
		"OFF": {Tracker: "mock"},
		"VO":  {Tracker: "jira"},
		"EXP": {Tracker: "jira"},
	}

	if got := projects.For("mock").Keys(); !slices.Equal(got, []string{"OFF"}) {
		t.Errorf("проекты mock: %v, ожидался только OFF", got)
	}
	if got := projects.For("jira").Keys(); !slices.Equal(got, []string{"EXP", "VO"}) {
		t.Errorf("проекты jira: %v, ожидались EXP и VO", got)
	}
	if got := projects.For("youtrack").Keys(); len(got) != 0 {
		t.Errorf("у незнакомого трекера нашлись проекты: %v", got)
	}
}

func TestLoadProjectsRejectsIncomplete(t *testing.T) {
	cases := []struct {
		name, office, machine, want string
	}{
		{"неизвестное поле офиса", strings.Replace(validOffice, "default_branch:", "branch:", 1), validMachine, "branch"},
		{"неизвестное поле машины", validOffice, strings.Replace(validMachine, "repo_url:", "repo:", 1), "repo"},
		{"нет репозитория", validOffice, strings.Replace(validMachine, "  repo_url: https://example.test/office.git\n", "", 1), "repo_url"},
		{"нет ветки по умолчанию", strings.Replace(validOffice, "  default_branch: master\n", "", 1), validMachine, "default_branch"},
		{"нет префикса веток", strings.Replace(validOffice, "  branch_prefix: agent/\n", "", 1), validMachine, "branch_prefix"},
		{"относительный worktree_root", validOffice, validMachine + "  worktree_root: ../рядом\n", "worktree_root"},
		{"нет трекера", validOffice, strings.Replace(validMachine, "  tracker: mock\n", "", 1), "tracker"},
		{"чужой трекер", validOffice, strings.Replace(validMachine, "tracker: mock", "tracker: youtrack", 1), "youtrack"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadHalves(t, tc.office, tc.machine)
			if err == nil {
				t.Fatal("неполный проект загружен без ошибки")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("в ошибке не назван %q: %v", tc.want, err)
			}
		})
	}
}

// Граница держится кодом, а не памятью того, кто через полгода будет править
// файл. Машинный ключ в файле офиса — отказ, и отказ называет, куда ключ переехал:
// строгий разбор сказал бы только «неизвестное поле», и человек пошёл бы искать
// опечатку.
func TestLoadProjectsRejectsMachineKeysInOfficeFile(t *testing.T) {
	for _, key := range machineKeys {
		t.Run(key, func(t *testing.T) {
			office := validOffice + "  " + key + ": что-нибудь\n"
			_, err := loadHalves(t, office, validMachine)
			if err == nil {
				t.Fatal("машинный ключ пропущен в файл офиса")
			}
			if !strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), ProjectsLocalFile) {
				t.Errorf("отказ не назвал ключ или файл, куда он переехал: %v", err)
			}
		})
	}
}

// Половины сверяются друг с другом по списку ключей, и обе стороны — отказ.
// Проект, названный офисом и не заведённый на машине, пропущенный молча,
// выглядел бы как проект, по которому просто нет задач. Проект, заведённый
// на машине и не названный офисом, — почти всегда опечатка в имени.
func TestLoadProjectsRequiresBothHalves(t *testing.T) {
	t.Run("нет машинной половины", func(t *testing.T) {
		_, err := loadHalves(t, validOffice+"VO:\n  default_branch: main\n  branch_prefix: a/\n", validMachine)
		if err == nil {
			t.Fatal("проект без машинных ключей загружен")
		}
		if !strings.Contains(err.Error(), "VO") {
			t.Errorf("отказ не назвал проект: %v", err)
		}
	})

	t.Run("нет офисной половины", func(t *testing.T) {
		_, err := loadHalves(t, validOffice, validMachine+"OFICE:\n  repo_url: https://example.test/x.git\n  tracker: mock\n")
		if err == nil {
			t.Fatal("проект, не названный офисом, загружен")
		}
		if !strings.Contains(err.Error(), "OFICE") {
			t.Errorf("отказ не назвал проект: %v", err)
		}
	})

	t.Run("машинного файла нет вовсе", func(t *testing.T) {
		_, err := LoadProjects(writeTemp(t, ProjectsFile, validOffice),
			filepath.Join(t.TempDir(), ProjectsLocalFile))
		if err == nil {
			t.Fatal("проекты загружены без машинной половины")
		}
		if !strings.Contains(err.Error(), ProjectsLocalFile) {
			t.Errorf("отказ не назвал недостающий файл: %v", err)
		}
	})
}

// Граф с PR-проходом: блок `pr` описывает маршрут pull request, а роли `office`
// в `roles` нет и быть не должно.
const prWorkflow = `statuses: [Ready, InProgress, Review, Approved, Done, Blocked]
terminal: [Done]
roles:
  implementer:
    reads_from: Ready
    working: InProgress
    outcomes:
      done:        { to: Review }
      needs_human: { to: Blocked, human: true }
      blocked:     { to: Ready, attempts: +1 }
      failed:      { to: Ready, attempts: +1 }
pr:
  role: office
  from: Approved
  merged: Done
  conflict: Ready
  closed: Blocked
limits:
  max_attempts: 3
  max_lease_expiries: 3
  max_push_failures: 3
  max_idle_runs: 3
  lease_margin_sec: 300
human_reply:
  fallback: Ready
  reset_attempts: true
`

// Проход — не роль: в обход ролей он не входит и файла роли у него нет.
// Но граф о нём отвечает: маркеры прохода подписаны его именем, и разбор ответа
// человека спрашивает граф именно по имени из маркера.
func TestWorkflowPRPassIsNotARole(t *testing.T) {
	w, err := LoadWorkflow(writeTemp(t, WorkflowFile, prWorkflow))
	if err != nil {
		t.Fatalf("граф не загружен: %v", err)
	}

	if slices.Contains(w.Order(), "office") {
		t.Errorf("проход попал в обход ролей: %v", w.Order())
	}
	flow, err := w.Role("office")
	if err != nil {
		t.Fatalf("граф не ответил про проход: %v", err)
	}
	if flow.ReadsFrom != "Approved" {
		t.Errorf("проход читает из %q, ожидался Approved", flow.ReadsFrom)
	}
	// Ответ человека на закрытый PR возвращает задачу в очередь прохода штатным
	// механизмом, а закрытый PR уводит её в ожидание — оба маршрута отсюда.
	if got := flow.Blocked(); got != "Blocked" {
		t.Errorf("проход зовёт человека в %q, ожидался Blocked", got)
	}
	if got := w.HumanStatuses(); !slices.Contains(got, "Blocked") {
		t.Errorf("статус ожидания прохода не попал в разбор ответов: %v", got)
	}
}

func TestWorkflowRejectsBrokenPRPass(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{"нет имени", strings.Replace(prWorkflow, "  role: office\n", "", 1), "pr.role"},
		{"чужой статус", strings.Replace(prWorkflow, "merged: Done", "merged: Смержено", 1), "Смержено"},
		{"проход назван ролью", strings.Replace(prWorkflow, "role: office", "role: implementer", 1), "implementer"},
		{"очередь прохода терминальна", strings.Replace(prWorkflow, "terminal: [Done]", "terminal: [Done, Approved]", 1), "pr.from"},
		{"слияние не терминально", strings.Replace(prWorkflow, "terminal: [Done]", "terminal: [Review]", 1), "pr.merged"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadWorkflow(writeTemp(t, WorkflowFile, tc.yaml))
			if err == nil {
				t.Fatal("сломанный блок pr загружен без ошибки")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("в ошибке не назван %q: %v", tc.want, err)
			}
		})
	}
}

// Граф без блока pr законен: это офис, который pull request не открывает.
func TestWorkflowWithoutPRPass(t *testing.T) {
	w, err := LoadWorkflow(writeTemp(t, WorkflowFile, validWorkflow))
	if err != nil {
		t.Fatalf("граф не загружен: %v", err)
	}
	if w.PR.Set() {
		t.Error("граф без блока pr считает проход описанным")
	}
	if _, err := w.Role("office"); err == nil {
		t.Error("граф без блока pr отвечает про роль office")
	}
}

// «Завёл файл, ещё не заполнил» — обычное состояние на новой машине, и отказ
// должен назвать, чего не хватает, а не сказать «не разобран: EOF».
func TestLoadProjectsExplainsEmptyMachineHalf(t *testing.T) {
	for name, body := range map[string]string{
		"пустой":         "",
		"из комментария": "# сюда допишу позже\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadHalves(t, validOffice, body)
			if err == nil {
				t.Fatal("пустая машинная половина принята за годную")
			}
			if strings.Contains(err.Error(), "EOF") {
				t.Errorf("отказ говорит про EOF вместо причины: %v", err)
			}
			if !strings.Contains(err.Error(), "OFF") {
				t.Errorf("отказ не назвал проект: %v", err)
			}
		})
	}
}

// «Ни одного проекта» — тоже отказ. Прочие беды склейки говорят вслух, и промолчать
// здесь значило бы оставить раннер крутить пустые тики без объяснений.
func TestLoadProjectsRejectsEmptyOffice(t *testing.T) {
	_, err := loadHalves(t, "", validMachine)
	if err == nil {
		t.Fatal("офис без проектов принят за годный")
	}
	if !strings.Contains(err.Error(), ProjectsFile) {
		t.Errorf("отказ не назвал файл офиса: %v", err)
	}
}

// Поля network/tools встраиваются в officeProject через yaml:",inline" —
// строгий разбор (KnownFields(true)) обязан принимать их на том же уровне
// вложенности, что и default_branch. Проверено вручную на gopkg.in/yaml.v3
// v3.0.1 перед тем, как класть embedding в прод; тест фиксирует это как
// регресс, а не как разовую проверку.
func TestOfficeProjectAcceptsInlineNetworkAndTools(t *testing.T) {
	office := validOffice + "  network: [a.test]\n  tools:\n    allow: [Read]\n    deny: [\"Bash(rm*)\"]\n"
	var m map[string]officeProject
	if err := decodeStrict(writeTemp(t, ProjectsFile, office), &m); err != nil {
		t.Fatalf("network/tools на уровне проекта не разобраны: %v", err)
	}
	off := m["OFF"]
	if !slices.Equal(off.Network, []string{"a.test"}) {
		t.Errorf("network = %v, ожидалось [a.test]", off.Network)
	}
	if !slices.Equal(off.Tools.Allow, []string{"Read"}) || !slices.Equal(off.Tools.Deny, []string{"Bash(rm*)"}) {
		t.Errorf("tools = %+v, ожидалось allow:[Read] deny:[Bash(rm*)]", off.Tools)
	}
}

// defaults — не проект: отсутствие в одном из двух файлов не ошибка,
// а «на этом уровне добавок нет». Наличие в обоих — оба вклада учтены
// (это проверяет Task 3, здесь — что сам разбор ключа не падает).
func TestLoadProjectsAllowsDefaultsInEitherOrBothFiles(t *testing.T) {
	withDefaults := "defaults:\n  network: [a.test]\n"

	cases := []struct {
		name, office, machine string
	}{
		{"только в офисном файле", validOffice + withDefaults, validMachine},
		{"только в машинном файле", validOffice, validMachine + withDefaults},
		{"в обоих файлах", validOffice + withDefaults, validMachine + withDefaults},
		{"ни в одном", validOffice, validMachine},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := loadHalves(t, tc.office, tc.machine); err != nil {
				t.Fatalf("defaults не должен быть ошибкой: %v", err)
			}
		})
	}
}

// defaults — зарезервированное имя для repo-wide/машинного слоя, а не
// проект: default_branch/branch_prefix ему не положены, и загрузчик обязан
// сказать об этом явно, а не молча принять их как проект по имени "defaults".
func TestLoadProjectsRejectsDefaultBranchUnderDefaultsKey(t *testing.T) {
	office := validOffice + "defaults:\n  default_branch: master\n"
	_, err := loadHalves(t, office, validMachine)
	if err == nil {
		t.Fatal("default_branch под defaults принят без ошибки")
	}
	if !strings.Contains(err.Error(), "defaults") || !strings.Contains(err.Error(), "default_branch") {
		t.Errorf("ошибка не называет причину: %v", err)
	}
}

// Зеркально — машинные поля под defaults в machine-файле.
func TestLoadProjectsRejectsRepoURLUnderDefaultsKeyInMachineFile(t *testing.T) {
	machine := validMachine + "defaults:\n  repo_url: https://example.test/x.git\n"
	_, err := loadHalves(t, validOffice, machine)
	if err == nil {
		t.Fatal("repo_url под defaults принят без ошибки")
	}
	if !strings.Contains(err.Error(), "defaults") || !strings.Contains(err.Error(), "repo_url") {
		t.Errorf("ошибка не называет причину: %v", err)
	}
}

// defaults не участвует в проверке парности ключей office/machine: он не
// проект, и требовать для него пару в другом файле значило бы обязать
// заводить пустой машинный (или офисный) слой ради синтаксиса.
func TestLoadProjectsDefaultsSkipsParityCheck(t *testing.T) {
	office := validOffice + "defaults:\n  network: [a.test]\n"
	if _, err := loadHalves(t, office, validMachine); err != nil {
		t.Fatalf("defaults только в офисном файле не должен требовать пары в машинном: %v", err)
	}
}

// Проект без собственных network/tools наследует только defaults —
// не пусто, но и не выдумывает ничего сверх repo-wide слоя.
func TestLoadProjectsProjectInheritsOnlyDefaults(t *testing.T) {
	office := validOffice + "defaults:\n  network: [a.test]\n  tools:\n    deny: [\"Bash(git *push*)\"]\n"
	projects, err := loadHalves(t, office, validMachine)
	if err != nil {
		t.Fatalf("проекты не загружены: %v", err)
	}
	p, err := projects.Get("OFF")
	if err != nil {
		t.Fatalf("проект OFF не найден: %v", err)
	}
	if !slices.Equal(p.Network, []string{"a.test"}) {
		t.Errorf("network = %v, ожидалось [a.test] (только из defaults)", p.Network)
	}
	if !slices.Equal(p.Tools.Deny, []string{"Bash(git *push*)"}) {
		t.Errorf("tools.deny = %v, ожидалось [Bash(git *push*)] (только из defaults)", p.Tools.Deny)
	}
}

// Специфика одного проекта не видна другому — иначе слой перестал бы
// быть per-project и превратился в ещё один repo-wide список.
func TestLoadProjectsProjectSpecificsAreIsolated(t *testing.T) {
	office := validOffice + "defaults:\n  network: [common.test]\n" +
		"VO:\n  default_branch: main\n  branch_prefix: a/\n  network: [vo-only.test]\n"
	machine := validMachine + "VO:\n  repo_url: https://example.test/vo.git\n  tracker: mock\n"

	projects, err := loadHalves(t, office, machine)
	if err != nil {
		t.Fatalf("проекты не загружены: %v", err)
	}
	off, _ := projects.Get("OFF")
	vo, _ := projects.Get("VO")

	if !slices.Equal(off.Network, []string{"common.test"}) {
		t.Errorf("OFF.network = %v, ожидалось [common.test] без утечки VO", off.Network)
	}
	if !slices.Equal(vo.Network, []string{"common.test", "vo-only.test"}) {
		t.Errorf("VO.network = %v, ожидалось [common.test vo-only.test]", vo.Network)
	}
}

// Проект без defaults вообще — сегодняшнее поведение не должно измениться:
// Network/Tools остаются пустыми, а не паникой на nil-слиянии.
func TestLoadProjectsWithoutDefaultsAtAllIsUnaffected(t *testing.T) {
	projects, err := loadHalves(t, validOffice, validMachine)
	if err != nil {
		t.Fatalf("проекты не загружены: %v", err)
	}
	p, _ := projects.Get("OFF")
	if len(p.Network) != 0 || len(p.Tools.Allow) != 0 || len(p.Tools.Deny) != 0 {
		t.Errorf("проект без единого defaults получил правила из ниоткуда: %+v", p)
	}
}

// unionStrings — дедуп и сортировка через все слои разом, тот же приём,
// что уже применяет adapters/claude/adapter.go:networkAllow, обобщённый
// на произвольное число слоёв.
func TestUnionStringsDedupsAndSorts(t *testing.T) {
	got := unionStrings([]string{"b", "a"}, nil, []string{"a", "c"})
	if want := []string{"a", "b", "c"}; !slices.Equal(got, want) {
		t.Errorf("unionStrings = %v, ожидалось %v", got, want)
	}
	if got := unionStrings(); got != nil {
		t.Errorf("unionStrings() без слоёв = %v, ожидался nil", got)
	}
}
