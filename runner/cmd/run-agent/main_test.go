package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kao73/virtual-office/ledger"
	"github.com/kao73/virtual-office/runagent"
	"github.com/kao73/virtual-office/runner"
)

// buildRunAgent собирает CLI и возвращает путь к бинарнику: коды возврата —
// внешний контракт команды, и проверять их надо на настоящем запуске.
func buildRunAgent(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "run-agent")
	cmd := exec.Command("go", "build", "-o", path, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run-agent не собран: %v: %s", err, out)
	}
	return path
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("корень репозитория не определён: %v", err)
	}
	return root
}

// runAgent запускает CLI с окружением офиса и возвращает код выхода.
func runAgent(t *testing.T, bin string, env []string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()

	var exitErr *exec.ExitError
	switch {
	case errors.As(err, &exitErr):
		return exitErr.ExitCode(), string(out)
	case err != nil:
		t.Fatalf("run-agent не запустился: %v", err)
	}
	return 0, string(out)
}

// Коды возврата — то, по чему smoke-тест, cron и раннер этапа 2 отличают
// «агент не справился» от «запускать было нечем». Смешивать их нельзя:
// на инфраструктурную беду надо будить человека, а на failed — считать попытки.
func TestExitCodeTwoOnInfrastructureFailure(t *testing.T) {
	bin := buildRunAgent(t)
	root := repoRoot(t)
	env := []string{"OFFICE_CONFIG_ROOT=" + root, "ANTHROPIC_API_KEY=ключ", "CLAUDE_CODE_OAUTH_TOKEN="}

	cases := []struct {
		name string
		args []string
	}{
		{"без аргументов", nil},
		{"нет такой роли", []string{"--role", "нет-такой", "--workdir", t.TempDir()}},
		{"неизвестный бэкенд", []string{"--role", "implementer", "--workdir", t.TempDir(), "--backend", "vm"}},
		{"отложенный бэкенд docker", []string{"--role", "implementer", "--workdir", t.TempDir(), "--backend", "docker"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, out := runAgent(t, bin, env, tc.args...)
			if code != 2 {
				t.Errorf("код %d, ожидался 2; вывод: %s", code, out)
			}
		})
	}
}

// Без креда запускать нечем — это тоже инфраструктура, а не провал агента.
func TestExitCodeTwoWithoutCredential(t *testing.T) {
	bin := buildRunAgent(t)
	workdir := gitRepo(t)

	code, out := runAgent(t, bin,
		[]string{"OFFICE_CONFIG_ROOT=" + repoRoot(t), "ANTHROPIC_API_KEY=", "CLAUDE_CODE_OAUTH_TOKEN="},
		"--role", "implementer", "--workdir", workdir, "--dry-run")
	if code != 2 {
		t.Errorf("код %d, ожидался 2; вывод: %s", code, out)
	}
}

// Холостой показ входа не тратит токенов и обязан завершаться нулём: им проверяют
// конфигурацию перед настоящим прогоном.
func TestExitCodeZeroOnDryRun(t *testing.T) {
	bin := buildRunAgent(t)
	workdir := gitRepo(t)
	task := filepath.Join(t.TempDir(), "task.md")
	if err := os.WriteFile(task, []byte("Сделай что-нибудь.\n"), 0o644); err != nil {
		t.Fatalf("постановка не записана: %v", err)
	}

	code, out := runAgent(t, bin,
		[]string{"OFFICE_CONFIG_ROOT=" + repoRoot(t), "OFFICE_HOME=" + t.TempDir(), "ANTHROPIC_API_KEY=ключ", "CLAUDE_CODE_OAUTH_TOKEN="},
		"--role", "implementer", "--workdir", workdir, "--task", task, "--dry-run")
	if code != 0 {
		t.Errorf("код %d, ожидался 0; вывод: %s", code, out)
	}
}

// Ручной запуск обязан уметь назвать ветки: без базовой ветки reviewer'а нечем
// отлаживать — разницу по задаче он видит только относительно неё. Имя своей ветки
// раннер спрашивает у самой рабочей папки, база задаётся флагом.
func TestDryRunNamesBranches(t *testing.T) {
	bin := buildRunAgent(t)
	workdir := gitRepo(t)

	code, out := runAgent(t, bin,
		[]string{"OFFICE_CONFIG_ROOT=" + repoRoot(t), "OFFICE_HOME=" + t.TempDir(), "ANTHROPIC_API_KEY=ключ", "CLAUDE_CODE_OAUTH_TOKEN="},
		"--role", "reviewer", "--workdir", workdir, "--task", taskFile(t), "--base", "origin/master", "--dry-run")
	if code != 0 {
		t.Fatalf("код %d, ожидался 0; вывод: %s", code, out)
	}

	context, err := os.ReadFile(filepath.Join(workdir, runner.Dir, runner.FileContext))
	if err != nil {
		t.Fatalf("контекст не прочитан: %v", err)
	}
	for _, want := range []string{"origin/master", currentBranch(t, workdir)} {
		if !strings.Contains(string(context), want) {
			t.Errorf("в контексте нет %q:\n%s", want, context)
		}
	}
}

// Список доменов роли на бэкенде без песочницы не значит ничего. Промолчать
// об этом — значит дать человеку поверить, что сеть закрыта: он читает role.yaml,
// а не исходники бэкенда.
func TestRunAgentWarnsThatLocalIgnoresNetworkPolicy(t *testing.T) {
	bin := buildRunAgent(t)
	workdir := gitRepo(t)

	_, out := runAgent(t, bin,
		[]string{"OFFICE_CONFIG_ROOT=" + repoRoot(t), "OFFICE_HOME=" + t.TempDir(), "ANTHROPIC_API_KEY=ключ", "CLAUDE_CODE_OAUTH_TOKEN="},
		"--role", "implementer", "--workdir", workdir, "--task", taskFile(t),
		"--backend", "local", "--dry-run")

	if !strings.Contains(out, "сетевой политики не применяет") {
		t.Errorf("о неприменённой политике не сказано:\n%s", out)
	}

	// В песочнице список работает, и говорить нечего.
	_, sandboxed := runAgent(t, bin,
		[]string{"OFFICE_CONFIG_ROOT=" + repoRoot(t), "OFFICE_HOME=" + t.TempDir(), "ANTHROPIC_API_KEY=ключ", "CLAUDE_CODE_OAUTH_TOKEN="},
		"--role", "implementer", "--workdir", workdir, "--task", taskFile(t), "--dry-run")
	if strings.Contains(sandboxed, "сетевой политики не применяет") {
		t.Errorf("предупреждение выдано там, где политика применяется:\n%s", sandboxed)
	}
}

func taskFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "task.md")
	if err := os.WriteFile(path, []byte("Разбери работу.\n"), 0o644); err != nil {
		t.Fatalf("постановка не записана: %v", err)
	}
	return path
}

func currentBranch(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		t.Fatalf("ветка не определена: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// gitRepo — рабочая папка агента: любой git-репозиторий.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"commit", "-q", "--allow-empty", "-m", "начало"}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=тест", "GIT_AUTHOR_EMAIL=test@office.local",
			"GIT_COMMITTER_NAME=тест", "GIT_COMMITTER_EMAIL=test@office.local")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("репозиторий не создан: %v: %s", err, out)
		}
	}
	return dir
}

// Подкоманда для человека: тем же кодом, что ограждение и раннер, ответить,
// почему результат отвергнут. Иначе разбираться приходится через новый прогон.
func TestValidateResultSubcommand(t *testing.T) {
	bin := buildRunAgent(t)
	dir := t.TempDir()

	valid := filepath.Join(dir, "valid.json")
	if err := os.WriteFile(valid, []byte(`{"outcome":"done","summary":"с.","next_owner":"none"}`), 0o644); err != nil {
		t.Fatalf("файл результата не записан: %v", err)
	}
	broken := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(broken, []byte(`{"outcome":"done","summary":"с."}`), 0o644); err != nil {
		t.Fatalf("файл результата не записан: %v", err)
	}

	if code, out := runAgent(t, bin, nil, "validate-result", valid); code != 0 {
		t.Errorf("валидный результат отвергнут: код %d, вывод: %s", code, out)
	}
	code, out := runAgent(t, bin, nil, "validate-result", broken)
	if code != 2 {
		t.Errorf("код %d, ожидался 2; вывод: %s", code, out)
	}
	if !strings.Contains(out, "next_owner") {
		t.Errorf("причина не названа: %s", out)
	}
}

// Реестр один на машину, а сводка считает усечения по всем строкам подряд.
// Ручной прогон без вида завершения выглядел бы в ней обычным провалом, и число
// прогонов, срезанных пределом шагов, вышло бы заниженным — ровно то число,
// по которому подбирают max_turns.
func TestAccountWritesTermination(t *testing.T) {
	home := t.TempDir()
	t.Setenv(runner.HomeEnv, home)

	account(runner.Run{RunID: "прогон", Role: "implementer"}, runagent.Outcome{
		Result:      runner.FailedResult("результата нет"),
		Usage:       runner.Usage{CostUSD: 1.77, DurationMS: 432672, Turns: 51},
		Termination: runner.Termination{Kind: runner.TerminationTruncated, Detail: "предел шагов исчерпан"},
	})

	raw, err := os.ReadFile(filepath.Join(home, ledger.FileName))
	if err != nil {
		t.Fatalf("реестр не прочитан: %v", err)
	}
	var line ledger.Entry
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &line); err != nil {
		t.Fatalf("строка реестра не разобрана: %v\n%s", err, raw)
	}
	if line.Termination != string(runner.TerminationTruncated) {
		t.Errorf("termination=%q, ожидалось %q", line.Termination, runner.TerminationTruncated)
	}
	if line.CostUSD != 1.77 {
		t.Errorf("cost_usd=%v: прогон без результата всё равно оплачен", line.CostUSD)
	}
}
