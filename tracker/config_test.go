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

	projects, err := LoadProjects(filepath.Join(root, ProjectsFile))
	if err != nil {
		t.Fatalf("%s не загружен: %v", ProjectsFile, err)
	}
	if len(projects) == 0 {
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
  max_review_rounds: 3
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
		t.Error("Approved не терминальная: рабочую папку никто не уберёт")
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
			yaml: strings.Replace(twoRoleWorkflow, "max_review_rounds: 3", "max_review_rounds: 0", 1),
			want: "max_review_rounds",
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

const validProjects = `OFF:
  repo_url: https://example.test/office.git
  default_branch: master
  branch_prefix: agent/
`

func TestLoadProjects(t *testing.T) {
	projects, err := LoadProjects(writeTemp(t, ProjectsFile, validProjects))
	if err != nil {
		t.Fatalf("проекты не загружены: %v", err)
	}

	p, err := projects.Get("OFF")
	if err != nil {
		t.Fatalf("проект OFF не найден: %v", err)
	}
	if got := p.Branch("OFF-12"); got != "agent/OFF-12" {
		t.Errorf("ветка %q, ожидалась agent/OFF-12", got)
	}

	// Задачу неизвестного проекта раннер брать не вправе: ему негде взять
	// репозиторий и некуда пушить.
	if _, err := projects.Get("НЕТ"); err == nil {
		t.Error("неизвестный проект найден")
	}
}

func TestLoadProjectsRejectsIncomplete(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{"неизвестное поле", strings.Replace(validProjects, "repo_url:", "repo:", 1), "repo"},
		{"нет репозитория", strings.Replace(validProjects, "  repo_url: https://example.test/office.git\n", "", 1), "repo_url"},
		{"нет ветки по умолчанию", strings.Replace(validProjects, "  default_branch: master\n", "", 1), "default_branch"},
		{"нет префикса веток", strings.Replace(validProjects, "  branch_prefix: agent/\n", "", 1), "branch_prefix"},
		{"относительный worktree_root", validProjects + "  worktree_root: ../рядом\n", "worktree_root"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadProjects(writeTemp(t, ProjectsFile, tc.yaml))
			if err == nil {
				t.Fatal("неполный проект загружен без ошибки")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("в ошибке не назван %q: %v", tc.want, err)
			}
		})
	}
}
