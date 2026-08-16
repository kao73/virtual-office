// Command run-agent запускает агента в заданной роли над указанной рабочей папкой
// и печатает машиночитаемый исход.
//
// run-agent не клонирует репозитории и не знает, откуда взялся workdir:
// это любой git-репозиторий, в проде — worktree, который создаст раннер на этапе 2.
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

	"github.com/kao73/virtual-office/adapters/claude"
	"github.com/kao73/virtual-office/backends/local"
	"github.com/kao73/virtual-office/backends/sbx"
	"github.com/kao73/virtual-office/runner"
)

// Коды возврата — внешний контракт команды.
//
//	0 — агент оставил валидный результат с исходом done, needs_human или blocked;
//	1 — исход failed, агентский или синтетический: агент не справился;
//	2 — инфраструктурная беда: роль, бэкенд, кред, тулчейн. Запускать было нечем.
//
// Смешивать 1 и 2 нельзя: на двойку надо будить человека, а единица — обычный
// исход прогона, который раннер этапа 2 считает попыткой.
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
	// По умолчанию — песочница: изоляция должна быть тем, что получаешь, ничего
	// не указав. Бэкенд local отлаживает контур и выбирается осознанно.
	backend := flag.String("backend", "sbx", "бэкенд запуска: sbx (песочница) или local (без изоляции)")
	taskFlag := flag.String("task", "", "файл с постановкой задачи; без него берётся уже лежащий .agent/task.md")
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
	taskFile := ""
	if *taskFlag != "" {
		if taskFile, err = filepath.Abs(resolve(*taskFlag)); err != nil {
			return 0, fmt.Errorf("файл задачи не разрешён: %w", err)
		}
	}

	// Бэкенд выбирается до сборки запуска: он задаёт платформу, под которую
	// собирается ограждение. Хостовый бинарник в песочнице не запустится.
	runBackend, target, err := backendByName(*backend)
	if err != nil {
		return 0, err
	}

	role, err := runner.LoadRole(configRoot, *roleName)
	if err != nil {
		return 0, err
	}

	runID, err := runner.NewRunID()
	if err != nil {
		return 0, err
	}
	configSHA, err := headSHA(configRoot)
	if err != nil {
		return 0, err
	}
	passport := runner.Run{
		RunID:     runID,
		Role:      role.Name,
		ConfigSHA: configSHA,
		StartedAt: time.Now(),
	}

	if err := runner.PrepareInput(workdir, role, passport, taskFile); err != nil {
		return 0, err
	}

	// Ограждение проверяет результат тем же кодом, что и раннер, поэтому бинарник
	// собирается из этого же репозитория под платформу выбранного бэкенда.
	validator, err := runner.EnsureValidator(configRoot, target)
	if err != nil {
		return 0, err
	}

	launch, err := claude.Build(role, workdir, passport, validator)
	if err != nil {
		return 0, err
	}
	defer func() { _ = launch.Cleanup() }()

	if *dryRun {
		printDryRun(launch, passport, target, validator)
		return 0, nil
	}

	logPath := filepath.Join(workdir, runner.Dir, runner.FileLog)
	exitCode, runErr := runBackend(context.Background(), launch, logPath)

	result, err := runner.ReadResult(workdir)
	if err != nil {
		// Раннер не додумывает исход за агента: молчание — это failed.
		reason := err.Error()
		switch {
		case runErr != nil:
			reason = runErr.Error() + "; " + reason
		case exitCode != 0:
			reason = fmt.Sprintf("агент завершился с кодом %d; %s", exitCode, reason)
		}
		result = runner.FailedResult(reason)
	}

	// Каталог обмена эфемерен: на этапе 2 рабочей папкой станет worktree, и он
	// исчезнет вместе с ним. Неудача архивации не подменяет исход прогона —
	// материал прогона в этот момент ещё цел, — но и молчать о ней нельзя.
	archived, archiveErr := runner.Archive(workdir, passport.RunID)
	if archiveErr != nil {
		fmt.Fprintln(os.Stderr, "run-agent: прогон не заархивирован:", archiveErr)
	}

	printResult(result, passport, logPath, archived)
	if result.Outcome == runner.OutcomeFailed {
		return exitFailed, nil
	}
	return 0, nil
}

// backendRun — исполнение подготовленного запуска. Подпись одна у всех бэкендов:
// адаптер знает устройство агента, бэкенд — устройство изоляции.
type backendRun func(context.Context, *runner.Launch, string) (int, error)

// backendByName выдаёт исполнителя и платформу, под которой он запускает агента.
func backendByName(name string) (backendRun, runner.Platform, error) {
	switch name {
	case "local":
		return local.Run, local.Platform(), nil
	case "sbx":
		return sbx.Run, sbx.Platform(), nil
	case "docker":
		return nil, runner.Platform{}, errors.New("бэкенд docker отложен: изоляцию закрывает sbx, докер понадобится на машине без KVM")
	default:
		return nil, runner.Platform{}, fmt.Errorf("неизвестный бэкенд %q: доступны local и sbx", name)
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

// headSHA — отпечаток конфигурации, ушедшей агенту.
//
// Незакоммиченная правка в роли, промпте или ограждении меняет то, что получит агент,
// а SHA не меняет. Без пометки паспорт прогона утверждал бы, что агенту достался
// коммит, которого агент не видел, — и разбор «после какого коммита роль стала
// косячить» опёрся бы на враньё. Пометка не восстанавливает правку, а лишь запрещает
// доверять SHA; сам материал прогона хранит архив (см. долги этапа 1).
func headSHA(repo string) (string, error) {
	out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("не прочитан commit конфигурации в %s: %w", repo, err)
	}
	sha := strings.TrimSpace(string(out))

	// Неотслеживаемые файлы считаются наравне с правками: они тоже не описаны SHA.
	status, err := exec.Command("git", "-C", repo, "status", "--porcelain").Output()
	if err != nil {
		return "", fmt.Errorf("не прочитано состояние конфигурации в %s: %w", repo, err)
	}
	if strings.TrimSpace(string(status)) != "" {
		sha += "-dirty"
	}
	return sha, nil
}

func printDryRun(l *runner.Launch, passport runner.Run, target runner.Platform, validator string) {
	fmt.Printf("run_id:     %s\nроль:       %s\nconfig_sha: %s\nworkdir:    %s\nконфиг:     %s\nтаймаут:    %s\nплатформа:  %s\nограждение: %s\n",
		passport.RunID, passport.Role, passport.ConfigSHA, l.Workdir, l.ConfigDir, l.Timeout, target, validator)

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

func printResult(r runner.Result, passport runner.Run, logPath, archived string) {
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
	fmt.Printf("run_id: %s\nлог:    %s\n", passport.RunID, logPath)
	if archived != "" {
		fmt.Printf("архив:  %s\n", archived)
	}
}
