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
	root := filepath.Join("..", "..")

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
      split:       { to: Blocked, human: true }
      blocked:     { to: Ready, attempts: +1 }
      failed:      { to: Ready, attempts: +1 }
limits:
  max_attempts: 3
  max_lease_expiries: 3
  max_push_failures: 3
  max_idle_runs: 3
  max_merge_refusals: 3
  max_pr_returns: 3
  max_merge_pending_sec: 3600
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
      split:       { to: Blocked, human: true }
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
      split:       { to: Blocked, human: true }
      blocked:     { to: Review, attempts: +1 }
      failed:      { to: Review, attempts: +1 }
limits:
  max_attempts: 3
  max_lease_expiries: 3
  max_push_failures: 3
  max_idle_runs: 3
  max_merge_refusals: 3
  max_pr_returns: 3
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
			yaml: strings.Replace(twoRoleWorkflow, "      needs_human: { to: Blocked, human: true }\n      split:       { to: Blocked, human: true }\n      blocked:     { to: Review, attempts: +1 }",
				"      needs_human: { to: Blocked, human: true, by_next_owner: { human: Blocked } }\n      split:       { to: Blocked, human: true }\n      blocked:     { to: Review, attempts: +1 }", 1),
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
			// Требуется только там, где вообще есть PR-проход (pr: в графе) —
			// prWorkflow, не validWorkflow, см. TestLimitsOptionalWithoutPRPass
			// для обратного случая.
			name: "предел отказов мержа не задан",
			yaml: strings.Replace(prWorkflow, "max_merge_refusals: 3", "max_merge_refusals: 0", 1),
			want: "max_merge_refusals",
		},
		{
			name: "предел возвратов PR не задан",
			yaml: strings.Replace(prWorkflow, "max_pr_returns: 3", "max_pr_returns: 0", 1),
			want: "max_pr_returns",
		},
		{
			name: "предел ожидания слияния не задан",
			yaml: strings.Replace(prWorkflow, "max_merge_pending_sec: 3600", "max_merge_pending_sec: 0", 1),
			want: "max_merge_pending_sec",
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

// Проект целиком описан одной записью machine-файла: второй половины нет.
// Обязательны repo_url, tracker и default_branch; branch_prefix — умолчание.
const validMachine = `OFF:
  repo_url: https://example.test/office.git
  tracker: mock
  default_branch: master
`

// load — проекты из временного projects.local.yaml.
func load(t *testing.T, machine string) (Projects, error) {
	t.Helper()
	return LoadProjects(writeTemp(t, ProjectsLocalFile, machine))
}

// Минимальной записи хватает, чтобы вести задачу: ветка — agent/<KEY>,
// база PR-прохода — default_branch.
func TestLoadProjects(t *testing.T) {
	projects, err := load(t, validMachine)
	if err != nil {
		t.Fatalf("проекты не загружены: %v", err)
	}
	p, err := projects.Get("OFF")
	if err != nil {
		t.Fatalf("проект OFF не найден: %v", err)
	}
	if got := p.Branch("OFF-12"); got != "agent/OFF-12" {
		t.Errorf("ветка %q, ожидалась agent/OFF-12 (branch_prefix по умолчанию)", got)
	}
	if got := p.PRBranch(); got != "master" {
		t.Errorf("база PR-прохода %q, ожидалась master", got)
	}
	if p.RepoURL != "https://example.test/office.git" || p.Tracker != "mock" {
		t.Errorf("запись доехала не целиком: %+v", p)
	}

	// Задачу неизвестного проекта раннер брать не вправе: ему негде взять
	// репозиторий и некуда пушить. Отказ называет единственный файл проектов.
	_, err = projects.Get("НЕТ")
	if err == nil {
		t.Fatal("неизвестный проект найден")
	}
	if !strings.Contains(err.Error(), ProjectsLocalFile) {
		t.Errorf("отказ не назвал файл проектов: %v", err)
	}
}

// Явный branch_prefix уважается: agent/ — умолчание, а не закон.
func TestLoadProjectsHonoursBranchPrefix(t *testing.T) {
	projects, err := load(t, validMachine+"  branch_prefix: office/\n")
	if err != nil {
		t.Fatalf("проекты не загружены: %v", err)
	}
	p, _ := projects.Get("OFF")
	if got := p.Branch("OFF-1"); got != "office/OFF-1" {
		t.Errorf("ветка %q, ожидалась office/OFF-1", got)
	}
}

// Каждый отказ называет ключ, проект и файл: чинить надо там, а не гадать.
func TestLoadProjectsRejectsIncomplete(t *testing.T) {
	cases := []struct {
		name, machine, want string
	}{
		{"неизвестное поле", strings.Replace(validMachine, "repo_url:", "repo:", 1), "repo"},
		{"нет репозитория", strings.Replace(validMachine, "  repo_url: https://example.test/office.git\n", "", 1), "repo_url"},
		{"нет ветки по умолчанию", strings.Replace(validMachine, "  default_branch: master\n", "", 1), "default_branch"},
		{"относительный worktree_root", validMachine + "  worktree_root: ../рядом\n", "worktree_root"},
		{"нет трекера", strings.Replace(validMachine, "  tracker: mock\n", "", 1), "tracker"},
		{"чужой трекер", strings.Replace(validMachine, "tracker: mock", "tracker: youtrack", 1), "youtrack"},
		{"auto_merge без forge", validMachine + "  auto_merge:\n    enabled: true\n", "forge"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := load(t, tc.machine)
			if err == nil {
				t.Fatal("неполный проект загружен без ошибки")
			}
			for _, want := range []string{tc.want, "OFF", ProjectsLocalFile} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("в ошибке не назван %q: %v", want, err)
				}
			}
		})
	}
}

// projectKeys обязан повторять теги machineProject: запись со всеми ключами
// контракта грузится, иначе забытый в списке ключ отвергал бы годный файл.
func TestLoadProjectsAcceptsEveryContractKey(t *testing.T) {
	machine := validMachine +
		"  branch_prefix: a/\n  worktree_root: /abs/OFF\n  forge: github\n" +
		"  auto_merge:\n    enabled: true\n    target_branch: office-integration\n" +
		"  network: [a.test]\n  tools:\n    allow: [Read]\n    deny: [\"Bash(rm*)\"]\n"
	projects, err := load(t, machine)
	if err != nil {
		t.Fatalf("запись со всеми ключами контракта отвергнута: %v", err)
	}
	p, _ := projects.Get("OFF")
	if !p.AutoMerge.Enabled || p.PRBranch() != "office-integration" || p.Forge != "github" {
		t.Errorf("auto_merge/forge доехали не целиком: %+v", p)
	}
	if !slices.Equal(p.Network, []string{"a.test"}) ||
		!slices.Equal(p.Tools.Allow, []string{"Read"}) || !slices.Equal(p.Tools.Deny, []string{"Bash(rm*)"}) {
		t.Errorf("network/tools на уровне записи не разобраны: %+v %+v", p.Network, p.Tools)
	}
}

// «Завёл файл, ещё не заполнил» — обычное состояние на новой машине; отказ
// называет файл и причину, а не «не разобран: EOF». Ни одного проекта — тоже
// отказ: промолчать значило бы крутить пустые тики без объяснений.
func TestLoadProjectsRejectsFileWithoutProjects(t *testing.T) {
	for name, body := range map[string]string{
		"пустой":          "",
		"из комментария":  "# сюда допишу позже\n",
		"только defaults": "defaults:\n  network: [a.test]\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := load(t, body)
			if err == nil {
				t.Fatal("файл без проектов принят за годный")
			}
			if strings.Contains(err.Error(), "EOF") {
				t.Errorf("отказ говорит про EOF вместо причины: %v", err)
			}
			if !strings.Contains(err.Error(), ProjectsLocalFile) || !strings.Contains(err.Error(), "ни одного проекта") {
				t.Errorf("отказ не назвал файл или причину: %v", err)
			}
		})
	}
}

// Файла нет — отказ называет его и обязательные ключи: заводить его
// человеку, и сказать надо, из чего.
func TestLoadProjectsRejectsMissingFile(t *testing.T) {
	_, err := LoadProjects(filepath.Join(t.TempDir(), ProjectsLocalFile))
	if err == nil {
		t.Fatal("проекты загружены без файла")
	}
	for _, want := range []string{ProjectsLocalFile, "repo_url", "tracker", "default_branch"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("отказ не назвал %q: %v", want, err)
		}
	}
}

// defaults — правила, а не проект: любой ключ, кроме network и tools, —
// отказ по имени. Раньше auto_merge под defaults молча пропадал.
func TestLoadProjectsRejectsProjectKeysUnderDefaults(t *testing.T) {
	for key, body := range map[string]string{
		"auto_merge":     "defaults:\n  auto_merge:\n    enabled: true\n",
		"default_branch": "defaults:\n  default_branch: main\n",
		"branch_prefix":  "defaults:\n  branch_prefix: x/\n",
		"repo_url":       "defaults:\n  repo_url: https://example.test/x.git\n",
	} {
		t.Run(key, func(t *testing.T) {
			_, err := load(t, validMachine+body)
			if err == nil {
				t.Fatalf("%s под defaults принят без ошибки", key)
			}
			if !strings.Contains(err.Error(), "defaults") || !strings.Contains(err.Error(), key) {
				t.Errorf("ошибка не называет причину: %v", err)
			}
		})
	}
}

// Пустой defaults законен — слоя нет.
func TestLoadProjectsAllowsEmptyDefaults(t *testing.T) {
	for name, body := range map[string]string{"пусто": "defaults:\n", "фигурные": "defaults: {}\n"} {
		t.Run(name, func(t *testing.T) {
			if _, err := load(t, validMachine+body); err != nil {
				t.Errorf("пустой defaults отвергнут: %v", err)
			}
		})
	}
}

// Машинный слой достаётся каждому проекту; специфика одного проекта другому
// не видна; проект без единого правила остаётся без правил.
func TestLoadProjectsLayersDefaultsAndProjectRules(t *testing.T) {
	machine := "defaults:\n  network: [common.test]\n  tools:\n    deny: [\"Bash(git *push*)\"]\n" +
		validMachine +
		"VO:\n  repo_url: https://example.test/vo.git\n  tracker: mock\n  default_branch: main\n  network: [vo-only.test]\n"
	projects, err := load(t, machine)
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
	if !slices.Equal(vo.Tools.Deny, []string{"Bash(git *push*)"}) {
		t.Errorf("VO.tools.deny = %v, defaults не доехал", vo.Tools.Deny)
	}

	bare, err := load(t, validMachine)
	if err != nil {
		t.Fatalf("проекты не загружены: %v", err)
	}
	p, _ := bare.Get("OFF")
	if len(p.Network) != 0 || len(p.Tools.Allow) != 0 || len(p.Tools.Deny) != 0 {
		t.Errorf("проект без единого правила получил их из ниоткуда: %+v", p)
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

// Трекеры, которые назвали проекты, — по алфавиту и без повторов: по этому
// списку раннер обходит офисы, и два запуска обязаны обходить их одинаково.
func TestTrackersInUse(t *testing.T) {
	projects := Projects{
		"OFF": {Tracker: "mock"},
		"VO":  {Tracker: "jira"},
		"EXP": {Tracker: "jira"},
	}
	if got := projects.TrackersInUse(); !slices.Equal(got, []string{"jira", "mock"}) {
		t.Errorf("TrackersInUse = %v, ожидалось [jira mock]", got)
	}
	if got := (Projects{}).TrackersInUse(); len(got) != 0 {
		t.Errorf("у пустого списка проектов нашлись трекеры: %v", got)
	}
}

// projects.yaml из прежней раскладки под корнем конфигурации — отказ, а не
// молча пропущенный файл: он носил и проекты, и общие правила, и оба адреса,
// куда они переехали, отказ называет.
func TestRefuseLeftoverOfficeFile(t *testing.T) {
	root := t.TempDir()
	if err := RefuseLeftoverOfficeFile(root); err != nil {
		t.Errorf("без projects.yaml отказ не положен: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, OfficeProjectsFile), []byte("OFF:\n"), 0o644); err != nil {
		t.Fatalf("projects.yaml не записан: %v", err)
	}
	err := RefuseLeftoverOfficeFile(root)
	if err == nil {
		t.Fatal("оставшийся projects.yaml пропущен молча")
	}
	for _, want := range []string{OfficeProjectsFile, ProjectsLocalFile, "roles/_base/base.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("отказ не назвал %q: %v", want, err)
		}
	}
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
      split:       { to: Blocked, human: true }
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
  max_merge_refusals: 3
  max_pr_returns: 3
  max_merge_pending_sec: 3600
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

// max_merge_refusals и max_pr_returns нужны только там, где вообще есть
// PR-проход: граф без блока pr — законная конфигурация (forge.go, checkPR),
// офис просто не открывает pull request, и заставлять такой граф объявлять
// пределы, которые он никогда не проверит, было бы лишним требованием —
// вопреки собственному правилу checkPR, что блок необязателен целиком.
func TestLimitsOptionalWithoutPRPass(t *testing.T) {
	yaml := strings.Replace(validWorkflow,
		"  max_merge_refusals: 3\n  max_pr_returns: 3\n  max_merge_pending_sec: 3600\n", "", 1)
	if strings.Contains(yaml, "max_merge_refusals") || strings.Contains(yaml, "max_pr_returns") ||
		strings.Contains(yaml, "max_merge_pending_sec") {
		t.Fatal("фикстура теста не убрала все три предела — проверка вырождена")
	}
	if _, err := LoadWorkflow(writeTemp(t, WorkflowFile, yaml)); err != nil {
		t.Errorf("граф без pr не должен требовать max_merge_refusals/max_pr_returns/max_merge_pending_sec: %v", err)
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

// PRBranch — ветка, от которой форкаются задачи и куда метит PR-проход:
// target_branch авто-мержа, если задан, иначе default_branch.
func TestPRBranch(t *testing.T) {
	p := Project{DefaultBranch: "master"}
	if got := p.PRBranch(); got != "master" {
		t.Errorf("PRBranch() = %q без target_branch, ожидался default_branch %q", got, "master")
	}
	p.AutoMerge.TargetBranch = "office-integration"
	if got := p.PRBranch(); got != "office-integration" {
		t.Errorf("PRBranch() = %q, ожидался target_branch %q", got, "office-integration")
	}
}
