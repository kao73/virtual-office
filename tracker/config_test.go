package tracker

import (
	"os"
	"path/filepath"
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

const validWorkflow = `columns: [Ready, InProgress, Review, Blocked, Done]
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
  lease_margin_sec: 300
human_reply:
  to: Ready
  reset_attempts: true
`

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
			name: "исход ведёт в несуществующую колонку",
			yaml: strings.Replace(validWorkflow, "done:        { to: Review }", "done:        { to: Готово }", 1),
			want: "Готово",
		},
		{
			name: "роль читает из несуществующей колонки",
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
			name: "ответ человека ведёт в никуда",
			yaml: strings.Replace(validWorkflow, "human_reply:\n  to: Ready", "human_reply:\n  to: Todo", 1),
			want: "Todo",
		},
		{
			name: "попыток не бывает",
			yaml: strings.Replace(validWorkflow, "max_attempts: 3", "max_attempts: 0", 1),
			want: "max_attempts",
		},
		{
			name: "колонки не заданы",
			yaml: strings.Replace(validWorkflow, "columns: [Ready, InProgress, Review, Blocked, Done]", "columns: []", 1),
			want: "columns",
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
