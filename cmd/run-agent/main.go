// Command run-agent запускает агента в заданной роли над указанной рабочей папкой
// и печатает машиночитаемый исход.
//
// run-agent не клонирует репозиториев и не знает, откуда взялся workdir: это любой
// git-репозиторий. В проде рабочую папку готовит раннер (`runner tick`), а эта
// команда остаётся для ручной отладки роли.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kao73/virtual-office/internal/ledger"
	"github.com/kao73/virtual-office/internal/runagent"
	"github.com/kao73/virtual-office/internal/runner"
	"github.com/kao73/virtual-office/internal/tracker"
)

// Коды возврата — внешний контракт команды.
//
//	0 — агент оставил валидный результат с исходом done, needs_human или blocked;
//	1 — исход failed, агентский или синтетический: агент не справился;
//	2 — инфраструктурная беда: роль, бэкенд, кред, тулчейн. Запускать было нечем.
//
// Смешивать 1 и 2 нельзя: на двойку надо будить человека, а единица — обычный
// исход прогона, который раннер считает попыткой.
const (
	exitFailed = 1
	exitInfra  = 2
)

// validateCmd — подкоманда для человека: почему раннер отверг результат.
// Тот же разбор, что у ограждения и у самого раннера, третьего парсера нет.
const validateCmd = "validate-result"

func main() {
	if len(os.Args) > 1 && os.Args[1] == validateCmd {
		if len(os.Args) != 3 {
			fmt.Fprintf(os.Stderr, "run-agent %s <путь к result.json>\n", validateCmd)
			os.Exit(exitInfra)
		}
		if _, err := runner.ReadResultFile(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(exitInfra)
		}
		return
	}

	code, err := execute()
	if err != nil {
		fmt.Fprintln(os.Stderr, "run-agent:", err)
		os.Exit(exitInfra)
	}
	os.Exit(code)
}

func execute() (int, error) {
	roleName := flag.String("role", "", "имя роли из roles/")
	workdirFlag := flag.String("workdir", "", "рабочая папка агента: git-репозиторий")
	backend := flag.String("backend", runagent.DefaultBackend, "бэкенд запуска: sbx (песочница) или local (без изоляции)")
	taskFlag := flag.String("task", "", "файл с постановкой; без него берётся уже лежащий .agent/task.md")
	projectFlag := flag.String("project", "", "проект из projects.yaml/projects.local.yaml: подмешивает "+
		"repo-wide, project- и machine-слои network/tools поверх роли, как это делает конвейер; "+
		"без флага роль остаётся в изоляции — только то, что названо в её собственном role.yaml")
	baseFlag := flag.String("base", "", "базовая ветка: от неё считается разница по задаче (нужна reviewer'у)")
	dryRun := flag.Bool("dry-run", false, "показать, что получит агент, и ничего не запускать")
	evalFlag := flag.Bool("eval", false, "пометить прогон как eval-harness: не считается в per_role_daily")
	flag.Parse()

	if *roleName == "" || *workdirFlag == "" {
		flag.Usage()
		return 0, errors.New("нужны --role и --workdir")
	}

	configRoot, err := officeRoot()
	if err != nil {
		return 0, err
	}
	workdir, err := filepath.Abs(resolve(*workdirFlag))
	if err != nil {
		return 0, fmt.Errorf("workdir не разрешён: %w", err)
	}

	task := ""
	if *taskFlag != "" {
		path, err := filepath.Abs(resolve(*taskFlag))
		if err != nil {
			return 0, fmt.Errorf("файл задачи не разрешён: %w", err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return 0, fmt.Errorf("постановка задачи не прочитана: %w", err)
		}
		task = string(raw)
	}

	role, err := runner.LoadRole(configRoot, *roleName)
	if err != nil {
		return 0, err
	}
	// Ручное воспроизведение прогона не должно расходиться с тем, что видит
	// реальный конвейер (pipeline.tickRole сливает те же слои после claim()) —
	// иначе повторится ситуация исходной находки: 403 в живом прогоне
	// не воспроизводился одинаково без понимания, откуда на самом деле
	// берётся сеть (docs/notes/followup-network-and-permissions.md).
	if *projectFlag != "" {
		home, err := runner.Home()
		if err != nil {
			return 0, err
		}
		projects, err := tracker.LoadProjects(
			filepath.Join(configRoot, tracker.ProjectsFile),
			filepath.Join(home, tracker.ProjectsLocalFile),
		)
		if err != nil {
			return 0, err
		}
		project, err := projects.Get(*projectFlag)
		if err != nil {
			return 0, err
		}
		role = tracker.MergeProjectRules(project, role)
	}
	// До --dry-run: увидеть, что сеть роли на этом бэкенде не работает, человек
	// должен там же, где смотрит остальное, — и не потратив токенов.
	if notice := runagent.NetworkNotice(*backend, role.Network.Allow); notice != "" {
		fmt.Fprintln(os.Stderr, "run-agent:", notice)
	}
	// И там же — про базовую политику машины: список роли работает только поверх
	// закрытой сети, а закрывает её человек, а не раннер.
	if notice := runagent.NetworkAudit(*backend); notice != "" {
		fmt.Fprintln(os.Stderr, "run-agent:", notice)
	}

	runID, err := runner.NewRunID()
	if err != nil {
		return 0, err
	}
	configSHA, err := runner.ConfigSHA(configRoot)
	if err != nil {
		return 0, err
	}
	base, err := runner.HeadCommit(workdir)
	if err != nil {
		return 0, err
	}
	passport := runner.Run{
		RunID:      runID,
		Role:       role.Name,
		ConfigSHA:  configSHA,
		StartedAt:  time.Now(),
		BaseCommit: base,
	}

	// Каталог изменения готовится так же, как в проде: роль, объявившая область
	// записи, получает готовые артефакты по своим шаблонам. Ключа задачи здесь
	// нет — трекера нет вовсе, — и каталог называется _manual: отладка роли
	// обязана выглядеть как её работа.
	created, err := runner.PrepareChangeDir(workdir, role, passport.TaskKey)
	if err != nil {
		return 0, err
	}
	// Заготовки, до которых не дошли руки, не должны пережить прогон: три пустых
	// шаблона в чужой папке — мусор, который потом читается как план.
	sweep := func() {
		if err := runner.SweepChangeDir(workdir, role, created); err != nil {
			fmt.Fprintln(os.Stderr, "run-agent: заготовки каталога изменения не убраны:", err)
		}
	}

	// Свою ветку рабочая папка знает сама, базовую — нет: она задаётся флагом.
	// В проде обе называет раннер, здесь их подставляет человек.
	input := runner.Input{Task: task, Branch: headBranch(workdir), BaseBranch: *baseFlag}
	if err := runner.PrepareInput(workdir, role, passport, input); err != nil {
		return 0, err
	}

	opts := runagent.Options{
		ConfigRoot: configRoot,
		Role:       role,
		Workdir:    workdir,
		Backend:    *backend,
		Passport:   passport,
	}

	if *dryRun {
		launch, err := runagent.Prepare(opts)
		if err != nil {
			return 0, err
		}
		defer func() { _ = launch.Cleanup() }()
		printDryRun(launch, passport)
		sweep()
		return 0, nil
	}

	out, err := runagent.Execute(context.Background(), opts)
	sweep()
	if err != nil {
		// Прогон мог состояться, а сорваться архивация — тогда исход печатаем,
		// но о беде говорим отдельно.
		if out.Result.Outcome == "" {
			return 0, err
		}
		fmt.Fprintln(os.Stderr, "run-agent:", err)
	}

	// Ручной прогон тоже стоит денег и тоже попадает в реестр — без задачи
	// и проекта: трекера здесь нет. Иначе отладка роли была бы бесплатной
	// только на бумаге, а дневной расход роли считался бы неверно.
	//
	// Прогон eval-harness'а (--eval) — исключение из этого «неверно»: в реестр
	// он пишется тоже, но со своей меткой, и per_role_daily (budget.go)
	// сознательно исключает такие строки — иначе sweep золотых кейсов сам
	// исчерпывал бы дневной бюджет роли. См. Entry.Eval в internal/ledger.
	account(passport, out, *evalFlag)

	printResult(out, passport)
	if out.Result.Outcome == runner.OutcomeFailed {
		return exitFailed, nil
	}
	return 0, nil
}

// account записывает прогон в реестр хозяйства раннера.
//
// Неудача записи прогона не отменяет: работа сделана, результат напечатан.
// Молча ронять её нельзя — расход учитывается не полностью, — поэтому беда
// уходит в stderr, а код возврата остаётся исходом прогона.
//
// Вид завершения пишется наравне с расходом, и это не педантизм: реестр один
// на машину, а сводка считает усечения по всем строкам подряд. Ручной прогон
// без этого поля выглядел бы в ней обычным провалом, и число прогонов, срезанных
// пределом шагов, вышло бы заниженным — ровно то число, по которому подбирают
// max_turns.
func account(passport runner.Run, out runagent.Outcome, eval bool) {
	runs, err := ledger.Default()
	if err == nil {
		err = runs.Append(ledger.Entry{
			RunID: passport.RunID, Role: passport.Role, Started: passport.StartedAt,
			Usage: out.Usage, Outcome: string(out.Result.Outcome),
			Termination: string(out.Termination.Kind), ConfigSHA: passport.ConfigSHA,
			Eval: eval,
		})
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "run-agent: прогон не записан в реестр:", err)
	}
}

// officeRoot — корень конфиг-репозитория. Его сообщает обёртка bin/run-agent;
// при прямом запуске берётся текущий каталог.
func officeRoot() (string, error) {
	if root := os.Getenv("OFFICE_CONFIG_ROOT"); root != "" {
		return root, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("корень конфигурации не определён: %w", err)
	}
	return cwd, nil
}

// resolve достраивает относительный путь от каталога, из которого позвали обёртку:
// сама обёртка переходит в корень репозитория, и относительные пути иначе поедут.
func resolve(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	if dir := os.Getenv("OFFICE_INVOCATION_DIR"); dir != "" {
		return filepath.Join(dir, path)
	}
	return path
}

// headBranch — ветка рабочей папки. Молчит, а не жалуется: агенту можно дать
// и репозиторий с отделённым HEAD, и тогда имени ветки просто нет.
func headBranch(workdir string) string {
	out, err := exec.Command("git", "-C", workdir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	if name := strings.TrimSpace(string(out)); name != "HEAD" {
		return name
	}
	return ""
}

func printDryRun(l runagent.Launch, passport runner.Run) {
	fmt.Printf("run_id:     %s\nроль:       %s\nconfig_sha: %s\nworkdir:    %s\nконфиг:     %s\nтаймаут:    %s\nплатформа:  %s\nограждение: %s\n",
		passport.RunID, passport.Role, passport.ConfigSHA, l.Workdir, l.ConfigDir, l.Timeout, l.Platform, l.Validator)

	fmt.Println("\n== команда ==")
	for _, arg := range l.Argv {
		fmt.Printf("  %s\n", arg)
	}

	fmt.Println("\n== окружение запуска ==")
	for _, kv := range l.Env {
		fmt.Printf("  %s\n", mask(kv, l.SecretVars))
	}

	// Здесь лежат ограничения неизолированного запуска — подменённый HOME и запрет
	// ssh-личностей. Без показа оператор не увидит, что именно защищает бэкенд local.
	fmt.Println("\n== окружение без изоляции (только бэкенд local) ==")
	for _, kv := range l.HostEnv {
		fmt.Printf("  %s\n", mask(kv, l.SecretVars))
	}

	fmt.Println("\n== рабочие пространства ==")
	for _, ws := range l.Workspaces {
		mode := "чтение и запись"
		if ws.ReadOnly {
			mode = "только чтение"
		}
		fmt.Printf("  %-16s %s\n", mode, ws.Path)
	}

	// Сеть показывается всегда, в том числе пустая: «роль никуда не ходит» —
	// такое же утверждение о прогоне, как список смонтированных каталогов.
	fmt.Println("\n== сеть сверх нужной агенту ==")
	if len(l.NetworkAllow) == 0 {
		fmt.Println("  (роль не просит доступа никуда)")
	}
	for _, host := range l.NetworkAllow {
		fmt.Printf("  %s\n", host)
	}

	fmt.Println("\n== скиллы ==")
	if len(l.Skills) == 0 {
		fmt.Println("  (роль не подключает скиллов)")
	}
	for _, s := range l.Skills {
		fmt.Printf("  %s\n", s)
	}

	fmt.Printf("\n== settings.json ==\n%s", l.Settings)
	fmt.Printf("\n== системный промпт ==\n%s\n", l.SystemPrompt)
	fmt.Printf("== стартовое сообщение ==\n%s\n", l.UserPrompt)
}

// mask прячет значения переменных с секретами: в выводе остаётся только имя.
func mask(kv string, secrets []string) string {
	name, _, found := strings.Cut(kv, "=")
	if !found {
		return kv
	}
	for _, secret := range secrets {
		if name == secret {
			return name + "=***"
		}
	}
	return kv
}

func printResult(out runagent.Outcome, passport runner.Run) {
	r := out.Result
	fmt.Printf("исход:  %s\n", r.Outcome)
	fmt.Printf("итог:   %s\n", r.Summary)
	fmt.Printf("дальше: %s\n", r.NextOwner)
	if r.Blocker != "" {
		fmt.Printf("блокер: %s\n", r.Blocker)
	}
	for _, q := range r.Questions {
		fmt.Printf("вопрос: %s: %s\n", q.ID, q.Text)
		for _, o := range q.Options {
			fmt.Printf("        %s) %s\n", o.ID, o.Label)
		}
	}
	if len(r.Artifacts) > 0 {
		fmt.Printf("артефакты: %s\n", strings.Join(r.Artifacts, ", "))
	}
	if u := out.Usage; u.Known() {
		fmt.Printf("расход: $%.4f, %s, %d шагов\n", u.CostUSD, u.Duration().Round(time.Second), u.Turns)
	}
	// Про открытое окно не говорится: оно открыто у каждого прогона.
	if notice := out.Limit.Notice(); notice != "" {
		fmt.Printf("пределы: %s\n", notice)
	}
	fmt.Printf("run_id: %s\nлог:    %s\n", passport.RunID, out.LogPath)
	if out.Archive != "" {
		fmt.Printf("архив:  %s\n", out.Archive)
	}
}
