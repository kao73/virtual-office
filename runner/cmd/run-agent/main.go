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
	"path/filepath"
	"strings"
	"time"

	"github.com/kao73/virtual-office/runagent"
	"github.com/kao73/virtual-office/runner"
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
	dryRun := flag.Bool("dry-run", false, "показать, что получит агент, и ничего не запускать")
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

	runID, err := runner.NewRunID()
	if err != nil {
		return 0, err
	}
	configSHA, err := runner.ConfigSHA(configRoot)
	if err != nil {
		return 0, err
	}
	passport := runner.Run{
		RunID:     runID,
		Role:      role.Name,
		ConfigSHA: configSHA,
		StartedAt: time.Now(),
	}

	if err := runner.PrepareInput(workdir, role, passport, runner.Input{Task: task}); err != nil {
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
		return 0, nil
	}

	out, err := runagent.Execute(context.Background(), opts)
	if err != nil {
		// Прогон мог состояться, а сорваться архивация — тогда исход печатаем,
		// но о беде говорим отдельно.
		if out.Result.Outcome == "" {
			return 0, err
		}
		fmt.Fprintln(os.Stderr, "run-agent:", err)
	}

	printResult(out, passport)
	if out.Result.Outcome == runner.OutcomeFailed {
		return exitFailed, nil
	}
	return 0, nil
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
		fmt.Printf("вопрос: %s\n", q.Text)
		for _, o := range q.Options {
			fmt.Printf("        - %s\n", o)
		}
	}
	if len(r.Artifacts) > 0 {
		fmt.Printf("артефакты: %s\n", strings.Join(r.Artifacts, ", "))
	}
	fmt.Printf("run_id: %s\nлог:    %s\n", passport.RunID, out.LogPath)
	if out.Archive != "" {
		fmt.Printf("архив:  %s\n", out.Archive)
	}
}
