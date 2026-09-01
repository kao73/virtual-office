package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kao73/virtual-office/internal/runner"
)

func buildFakeAgent(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fakeagent")
	cmd := exec.Command("go", "build", "-o", bin, "./testdata/fakeagent")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fakeagent не собран: %v: %s", err, out)
	}
	return bin
}

func TestRunRoleAgentParsesResult(t *testing.T) {
	bin := buildFakeAgent(t)
	workdir := t.TempDir()
	gitInit(t, workdir) // fakeagent теперь вызывает runner.ExcludeAgentDir, а ей нужен git-репозиторий
	taskPath := filepath.Join(t.TempDir(), "task.md")
	if err := os.WriteFile(taskPath, []byte("тестовая задача\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("FAKE_AGENT_RESULT", `{"outcome":"done","summary":"готово","next_owner":"none"}`)
	t.Setenv("FAKE_AGENT_EXIT", "0")

	result, code, err := runRoleAgent(bin, ".", "implementer", workdir, taskPath, "", false)
	if err != nil {
		t.Fatalf("run-agent не разобран: %v", err)
	}
	if code != 0 {
		t.Errorf("код %d, ожидался 0", code)
	}
	if result.Outcome != runner.OutcomeDone {
		t.Errorf("outcome=%q, ожидался done", result.Outcome)
	}
}

// Найденный живым прогоном (задача 16, reviewer/capability-spot-defect):
// без --task-key run-agent ведёт себя как при обычном ручном запуске — без
// трекера, TaskKey пуст, composeContext не может назвать каталог изменения
// фикстуры в context.md, даже когда фикстура его честно завела. Проверяем,
// что eval-roles реально прокидывает ключ до run-agent, а не только считает
// его сама (discoverFixtureTaskKey уже покрыт отдельно, в fixture_test.go).
func TestRunRoleAgentPassesTaskKey(t *testing.T) {
	bin := buildFakeAgent(t)
	workdir := t.TempDir()
	gitInit(t, workdir)
	taskPath := filepath.Join(t.TempDir(), "task.md")
	if err := os.WriteFile(taskPath, []byte("тестовая задача\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_AGENT_RESULT", `{"outcome":"done","summary":"готово","next_owner":"none"}`)

	if _, _, err := runRoleAgent(bin, ".", "implementer", workdir, taskPath, "eval-brief", false); err != nil {
		t.Fatalf("run-agent не разобран: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(workdir, ".agent", ".fake-task-key"))
	if err != nil {
		t.Fatalf("--task-key не дошёл до run-agent: %v", err)
	}
	if string(got) != "eval-brief" {
		t.Errorf("--task-key=%q, ожидался %q", got, "eval-brief")
	}
}

func TestRunRoleAgentReportsInfraFailure(t *testing.T) {
	bin := buildFakeAgent(t)
	workdir := t.TempDir()
	gitInit(t, workdir) // fakeagent теперь вызывает runner.ExcludeAgentDir, а ей нужен git-репозиторий
	taskPath := filepath.Join(t.TempDir(), "task.md")
	if err := os.WriteFile(taskPath, []byte("задача\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("FAKE_AGENT_EXIT", "2")

	_, code, err := runRoleAgent(bin, ".", "implementer", workdir, taskPath, "", false)
	if err == nil {
		t.Fatal("инфраструктурная беда (код 2) не замечена")
	}
	if code != 2 {
		t.Errorf("код %d, ожидался 2", code)
	}
}
