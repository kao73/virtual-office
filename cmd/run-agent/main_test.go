package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kao73/virtual-office/internal/ledger"
	"github.com/kao73/virtual-office/internal/runagent"
	"github.com/kao73/virtual-office/internal/runner"
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
	root, err := filepath.Abs(filepath.Join("..", ".."))
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

// Найдено живым прогоном (задача 16 → задача 19): --task-key был прокинут
// до passport.TaskKey, но ни один тест не проверял именно этот шов — только
// его соседей по отдельности (composeContext уже покрыт при заданном
// TaskKey, invoke.go — что --task-key долетает до фиктивного агента).
// Мутационный тест ревью подтвердил дыру: испортить значение в main.go
// (passport.TaskKey на другую строку) — весь go test ./... остаётся
// зелёным. Этот тест закрывает именно её.
func TestDryRunNamesCometChangeDirFromTaskKey(t *testing.T) {
	bin := buildRunAgent(t)
	workdir := gitRepo(t)

	const taskKey = "eval-brief"
	changeDir := filepath.Join(workdir, runner.CometChangeDirRel(taskKey))
	if err := os.MkdirAll(changeDir, 0o755); err != nil {
		t.Fatalf("каталог изменения не создан: %v", err)
	}

	code, out := runAgent(t, bin,
		[]string{"OFFICE_CONFIG_ROOT=" + repoRoot(t), "OFFICE_HOME=" + t.TempDir(), "ANTHROPIC_API_KEY=ключ", "CLAUDE_CODE_OAUTH_TOKEN="},
		"--role", "reviewer", "--workdir", workdir, "--task", taskFile(t), "--task-key", taskKey, "--dry-run")
	if code != 0 {
		t.Fatalf("код %d, ожидался 0; вывод: %s", code, out)
	}

	context, err := os.ReadFile(filepath.Join(workdir, runner.Dir, runner.FileContext))
	if err != nil {
		t.Fatalf("контекст не прочитан: %v", err)
	}
	if want := "Каталог изменения: " + runner.CometChangeDirRel(taskKey); !strings.Contains(string(context), want) {
		t.Errorf("в контексте нет %q — --task-key не дошёл до composeContext:\n%s", want, context)
	}
}

// --clone меняет саму природу Workspaces (клон вместо бинд-маунта) — dry-run
// обязан показать это тем же взглядом, каким показывает обычные рабочие
// пространства, иначе выбор бэкенда остаётся невидим до настоящего платного
// прогона.
func TestDryRunCloneSetsCloneSync(t *testing.T) {
	bin := buildRunAgent(t)
	workdir := gitRepo(t)
	branch := currentBranch(t, workdir)

	code, out := runAgent(t, bin,
		[]string{"OFFICE_CONFIG_ROOT=" + repoRoot(t), "OFFICE_HOME=" + t.TempDir(), "ANTHROPIC_API_KEY=ключ", "CLAUDE_CODE_OAUTH_TOKEN="},
		"--role", "implementer", "--workdir", workdir, "--task", taskFile(t), "--clone", "--dry-run")
	if code != 0 {
		t.Fatalf("код %d, ожидался 0; вывод: %s", code, out)
	}
	// Полная отрендеренная строка, не подстрока: ".agent"/".comet" сами по
	// себе не показательны — системный промпт implementer'а упоминает
	// ".agent" и без --clone (проверено: без флага "каталоги:" в выводе нет
	// вовсе, а голое ".agent" встречается 7 раз).
	for _, want := range []string{
		"ветка:      " + branch,
		"вернуть в:  " + workdir,
		"каталоги:   .agent, .comet/runtime",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("в выводе --clone нет %q:\n%s", want, out)
		}
	}
}

// --clone без ветки подтягивать некуда: отделённый HEAD — не тот случай,
// когда можно молча продолжить и потерять работу агента при сносе песочницы.
func TestCloneWithoutBranchFails(t *testing.T) {
	bin := buildRunAgent(t)
	workdir := gitRepo(t)
	if out, err := exec.Command("git", "-C", workdir, "checkout", "-q", "--detach").CombinedOutput(); err != nil {
		t.Fatalf("HEAD не отделён: %v: %s", err, out)
	}

	code, out := runAgent(t, bin,
		[]string{"OFFICE_CONFIG_ROOT=" + repoRoot(t), "OFFICE_HOME=" + t.TempDir(), "ANTHROPIC_API_KEY=ключ", "CLAUDE_CODE_OAUTH_TOKEN="},
		"--role", "implementer", "--workdir", workdir, "--task", taskFile(t), "--clone", "--dry-run")
	if code != 2 {
		t.Errorf("код %d, ожидался 2 (инфраструктурная беда); вывод: %s", code, out)
	}
}

// syntheticRole — конфиг-репозиторий (git нужен для ConfigSHA) с одной
// минимальной ролью test-role; extra дописывается в конец role.yaml.
func syntheticRole(t *testing.T, extra string) (configRoot string) {
	t.Helper()
	configRoot = gitRepo(t)
	roleDir := filepath.Join(configRoot, "roles", "test-role")
	if err := os.MkdirAll(roleDir, 0o755); err != nil {
		t.Fatalf("каталог роли не создан: %v", err)
	}
	if err := os.WriteFile(filepath.Join(roleDir, "role.md"), []byte("# Тестовая роль\n"), 0o644); err != nil {
		t.Fatalf("промпт не написан: %v", err)
	}
	roleYAML := "name: test-role\nprompt: role.md\nincludes: []\nskills: []\ntools:\n  allow: [Read]\n" +
		"limits: { max_turns: 10, timeout_sec: 300 }\nresult_file: .agent/result.json\n" + extra
	if err := os.WriteFile(filepath.Join(roleDir, "role.yaml"), []byte(roleYAML), 0o644); err != nil {
		t.Fatalf("role.yaml не написан: %v", err)
	}
	return configRoot
}

// projectsLocal — хозяйство с одним mock-проектом OFFICE; extra дописывается
// в его запись. --dry-run ничего не клонирует: значение repo_url не важно.
func projectsLocal(t *testing.T, extra string) (home string) {
	t.Helper()
	home = t.TempDir()
	machine := "OFFICE:\n  repo_url: https://example.test/o.git\n  tracker: mock\n  default_branch: master\n" + extra
	if err := os.WriteFile(filepath.Join(home, "projects.local.yaml"), []byte(machine), 0o644); err != nil {
		t.Fatalf("projects.local.yaml не записан: %v", err)
	}
	return home
}

// Список доменов роли на бэкенде без песочницы не значит ничего. Промолчать
// об этом — значит дать человеку поверить, что сеть закрыта: он читает role.yaml,
// а не исходники бэкенда.
func TestRunAgentWarnsThatLocalIgnoresNetworkPolicy(t *testing.T) {
	bin := buildRunAgent(t)
	workdir := gitRepo(t)

	// Синтетическая роль с сетевой политикой — ради сообщения о её неприменённости.
	configRoot := syntheticRole(t, "network:\n  allow:\n    - example.com\n")

	_, out := runAgent(t, bin,
		[]string{"OFFICE_CONFIG_ROOT=" + configRoot, "OFFICE_HOME=" + t.TempDir(), "ANTHROPIC_API_KEY=ключ", "CLAUDE_CODE_OAUTH_TOKEN="},
		"--role", "test-role", "--workdir", workdir, "--task", taskFile(t),
		"--backend", "local", "--dry-run")

	if !strings.Contains(out, "сетевой политики не применяет") {
		t.Errorf("о неприменённой политике не сказано:\n%s", out)
	}

	// В песочнице список работает, и говорить нечего.
	_, sandboxed := runAgent(t, bin,
		[]string{"OFFICE_CONFIG_ROOT=" + configRoot, "OFFICE_HOME=" + t.TempDir(), "ANTHROPIC_API_KEY=ключ", "CLAUDE_CODE_OAUTH_TOKEN="},
		"--role", "test-role", "--workdir", workdir, "--task", taskFile(t), "--dry-run")
	if strings.Contains(sandboxed, "сетевой политики не применяет") {
		t.Errorf("предупреждение выдано там, где политика применяется:\n%s", sandboxed)
	}
}

// Базовые правила ролей (roles/_base/base.yaml) приходят через LoadRole и
// потому есть у роли и без --project; с флагом поверх них ложатся слои
// projects.local.yaml — сравнение двух прогонов и есть тест механизма.
// Хост machine-only.test существует только в машинном файле: увидеть его
// без флага значило бы, что слои перепутаны.
func TestDryRunProjectFlagMergesMachineRulesOverBase(t *testing.T) {
	bin := buildRunAgent(t)
	workdir := gitRepo(t)
	home := projectsLocal(t, "  network: [machine-only.test]\n")
	env := []string{"OFFICE_CONFIG_ROOT=" + repoRoot(t), "OFFICE_HOME=" + home, "ANTHROPIC_API_KEY=ключ", "CLAUDE_CODE_OAUTH_TOKEN="}

	_, withoutFlag := runAgent(t, bin, env, "--role", "implementer", "--workdir", workdir, "--task", taskFile(t), "--dry-run")
	for _, want := range []string{"registry-1.docker.io", "Bash(git *push*)"} {
		if !strings.Contains(withoutFlag, want) {
			t.Errorf("без --project роль не получила базовое правило %q:\n%s", want, withoutFlag)
		}
	}
	if strings.Contains(withoutFlag, "machine-only.test") {
		t.Errorf("без --project роль уже видит машинный слой:\n%s", withoutFlag)
	}

	code, withFlag := runAgent(t, bin, env, "--role", "implementer", "--workdir", workdir, "--task", taskFile(t), "--project", "OFFICE", "--dry-run")
	if code != 0 {
		t.Fatalf("код %d, ожидался 0; вывод: %s", code, withFlag)
	}
	if !strings.Contains(withFlag, "machine-only.test") {
		t.Errorf("с --project OFFICE в сети нет машинного хоста:\n%s", withFlag)
	}
	if !strings.Contains(withFlag, "registry-1.docker.io") {
		t.Errorf("с --project OFFICE базовый хост потерян при слиянии:\n%s", withFlag)
	}
}

// Опечатка в имени проекта не должна тихо проигнорироваться — Projects.Get
// уже даёт содержательную ошибку, используется как есть.
func TestDryRunProjectFlagRejectsUnknownProject(t *testing.T) {
	bin := buildRunAgent(t)
	workdir := gitRepo(t)
	home := projectsLocal(t, "")
	env := []string{"OFFICE_CONFIG_ROOT=" + repoRoot(t), "OFFICE_HOME=" + home, "ANTHROPIC_API_KEY=ключ", "CLAUDE_CODE_OAUTH_TOKEN="}

	code, out := runAgent(t, bin, env, "--role", "implementer", "--workdir", workdir, "--task", taskFile(t), "--project", "НЕТ-ТАКОГО", "--dry-run")
	if code != 2 {
		t.Errorf("код %d, ожидался 2 (инфраструктурная беда); вывод: %s", code, out)
	}
}

// tracker.RefuseLeftoverOfficeFile покрыт собственным юнит-тестом
// (internal/tracker), но до этого теста ничто не проверяло сам вызов внутри
// execute() (main.go, под --project) — рефакторинг мог бы его потерять
// незамеченным. configRoot здесь свой, не repoRoot(t): стрелять
// projects.yaml в настоящий репозиторий нельзя, а --project требует роль,
// так что configRoot собран тем же приёмом, что и в
// TestRunAgentWarnsThatLocalIgnoresNetworkPolicy — синтетическая роль
// без roles/_base (LoadRole ждёт его как опцию, не как обязанность).
func TestDryRunProjectFlagRefusesLeftoverProjectsYAML(t *testing.T) {
	bin := buildRunAgent(t)
	workdir := gitRepo(t)
	configRoot := syntheticRole(t, "")
	if err := os.WriteFile(filepath.Join(configRoot, "projects.yaml"), []byte("OFFICE: {}\n"), 0o644); err != nil {
		t.Fatalf("projects.yaml не записан: %v", err)
	}
	home := projectsLocal(t, "")
	env := []string{"OFFICE_CONFIG_ROOT=" + configRoot, "OFFICE_HOME=" + home, "ANTHROPIC_API_KEY=ключ", "CLAUDE_CODE_OAUTH_TOKEN="}

	// Сторож один и тот же с --project и без: дерево, которое runner
	// отвергает, run-agent не должен принимать молча ни в одном режиме.
	for name, extra := range map[string][]string{
		"с --project":   {"--project", "OFFICE"},
		"без --project": nil,
	} {
		t.Run(name, func(t *testing.T) {
			args := append([]string{"--role", "test-role", "--workdir", workdir, "--task", taskFile(t)}, extra...)
			code, out := runAgent(t, bin, env, append(args, "--dry-run")...)
			if code != 2 {
				t.Errorf("код %d, ожидался 2 (инфраструктурная беда); вывод: %s", code, out)
			}
			for _, want := range []string{"projects.local.yaml", "roles/_base/base.yaml"} {
				if !strings.Contains(out, want) {
					t.Errorf("отказ не назвал %q: %s", want, out)
				}
			}
		})
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
	}, false)

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

// buildRunAgentRelease собирает CLI как релиз: с версией в ldflags. Без
// OFFICE_CONFIG_ROOT такой бинарник обязан брать офис из своей поставки.
func buildRunAgentRelease(t *testing.T, version string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "run-agent")
	cmd := exec.Command("go", "build", "-ldflags", "-X github.com/kao73/virtual-office.Version="+version, "-o", path, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run-agent не собран: %v: %s", err, out)
	}
	return path
}

// Бинарник без OFFICE_CONFIG_ROOT читает роли из распакованной поставки, а не
// из текущего каталога (cwd теста — корень клона, и раньше он и был офисом).
// Ограждения у сборки без -tags release нет — прогон обязан упереться именно
// в это, уже после загрузки роли с обоими хуками из распакованного офиса.
func TestPayloadModeReadsUnpackedOfficeNotCwd(t *testing.T) {
	bin := buildRunAgentRelease(t, "v0.0.0-test")
	home := t.TempDir()
	workdir := gitRepo(t)
	// --task обязателен (PrepareInput отказывает без него и без .agent/task.md
	// в рабочей папке) — иначе прогон упёрся бы в отсутствие постановки раньше,
	// чем в отсутствие ограждения, и тест ловил бы не ту причину.
	code, out := runAgent(t, bin,
		[]string{"OFFICE_CONFIG_ROOT=", "OFFICE_HOME=" + home, "ANTHROPIC_API_KEY=ключ", "CLAUDE_CODE_OAUTH_TOKEN="},
		"--role", "implementer", "--workdir", workdir, "--task", taskFile(t), "--backend", "local", "--dry-run")
	if code != 2 {
		t.Fatalf("код %d, ожидался 2 (встроенного ограждения нет); вывод: %s", code, out)
	}
	for _, want := range []string{"без ограждения", runtime.GOOS + "/" + runtime.GOARCH, "-tags release"} {
		if !strings.Contains(out, want) {
			t.Errorf("отказ не называет %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "не исполняемый") {
		t.Errorf("хук из распакованного офиса без бита исполняемости:\n%s", out)
	}
	root := filepath.Join(home, "office", "v0.0.0-test")
	for _, rel := range []string{"roles/implementer/role.yaml", "hooks/require-result.sh", "skills/comet/scripts/comet-hook-router.mjs"} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("%s не распакован: %v", rel, err)
		}
	}
}

func TestAccountWritesEvalFlag(t *testing.T) {
	home := t.TempDir()
	t.Setenv(runner.HomeEnv, home)

	account(runner.Run{RunID: "прогон-eval", Role: "implementer"}, runagent.Outcome{
		Result: runner.Result{Outcome: runner.OutcomeDone, Summary: "s", NextOwner: "none"},
		Usage:  runner.Usage{CostUSD: 0.5, DurationMS: 1000, Turns: 5},
	}, true)

	raw, err := os.ReadFile(filepath.Join(home, ledger.FileName))
	if err != nil {
		t.Fatalf("реестр не прочитан: %v", err)
	}
	var line ledger.Entry
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &line); err != nil {
		t.Fatalf("строка не разобрана: %v\n%s", err, raw)
	}
	if !line.Eval {
		t.Errorf("eval=%v, ожидался true: %+v", line.Eval, line)
	}
}
