package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kao73/virtual-office/internal/budget"
	"github.com/kao73/virtual-office/internal/forge"
	"github.com/kao73/virtual-office/internal/ledger"
	"github.com/kao73/virtual-office/internal/pipeline"
	"github.com/kao73/virtual-office/internal/runagent"
	"github.com/kao73/virtual-office/internal/runner"
	"github.com/kao73/virtual-office/internal/tracker"
	"github.com/kao73/virtual-office/internal/tracker/jira"
	"github.com/kao73/virtual-office/internal/tracker/mock"
	"github.com/kao73/virtual-office/internal/workspace"
)

// office собирает конвейер: трекер, хозяйство рабочих папок, граф, проекты
// и настоящий запуск агента.
//
// Сборка вся здесь, в точке входа: pipeline не знает, какой трекер и какой
// бэкенд ему достались, и именно поэтому его можно проверить целиком
// на файловом трекере с поддельным агентом.
func office(fs *flag.FlagSet, args []string, out io.Writer) (*pipeline.Office, error) {
	trackerName := fs.String("tracker", "mock",
		"трекер задач: "+strings.Join(tracker.Trackers(), " или "))
	backend := fs.String("backend", runagent.DefaultBackend, "бэкенд агента: sbx или local")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	configRoot, err := configRoot()
	if err != nil {
		return nil, err
	}
	// Хозяйство раннера — вторая половина конфигурации. Репозиторий описывает
	// офис, ${OFFICE_HOME} — этот инстанс: пути, адреса, учётки, номера полей.
	home, err := runner.Home()
	if err != nil {
		return nil, err
	}
	sources := configSources{out: out}

	workflow, err := tracker.LoadWorkflow(sources.office(configRoot, tracker.WorkflowFile))
	if err != nil {
		return nil, err
	}

	// Трекеров столько, сколько учёток: общий и по одному на роль, у которой
	// своя. Открываются они здесь и разом — узнать о неверном креде роли
	// в середине цикла, уже захватив задачу, было бы поздно.
	var tasks tracker.Tracker
	var byRole map[string]tracker.Tracker
	var accounts []string
	switch *trackerName {
	case "mock":
		office, err := mock.Default()
		if err != nil {
			return nil, err
		}
		tasks, byRole, accounts = office, map[string]tracker.Tracker{}, []string{mock.Account}
		for _, role := range workflow.Order() {
			byRole[role] = office.As(mock.RoleAccount(role))
			accounts = append(accounts, mock.RoleAccount(role))
		}
	case "jira":
		cfg, err := jira.LoadConfig(sources.machine(home, jira.TrackerFile))
		if err != nil {
			return nil, err
		}
		office, err := jira.Open(cfg)
		if err != nil {
			return nil, err
		}
		// Сверка имени с сервером — здесь, а не в Open: она стоит запроса,
		// и делать её на каждом открытии трекера незачем.
		if err := office.CheckAccount(); err != nil {
			return nil, fmt.Errorf("общая учётка офиса: %w", err)
		}
		tasks = office

		byRole = map[string]tracker.Tracker{}
		for _, role := range workflow.Order() {
			roleTracker, err := jira.OpenAs(cfg, role)
			if err != nil {
				return nil, fmt.Errorf("трекер роли %s не открыт: %w", role, err)
			}
			if err := roleTracker.CheckAccount(); err != nil {
				return nil, fmt.Errorf("учётка роли %s: %w", role, err)
			}
			byRole[role] = roleTracker
		}
		if accounts, err = cfg.AgentAccounts(); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("неизвестный трекер %q: доступны %s", *trackerName, strings.Join(tracker.Trackers(), ", "))
	}

	projects, err := tracker.LoadProjects(
		sources.office(configRoot, tracker.ProjectsFile),
		sources.machine(home, tracker.ProjectsLocalFile),
	)
	if err != nil {
		return nil, err
	}
	// Раннер запускается с одним трекером и работает только со своими проектами.
	// Чужие не просто бесполезны: трекер отвечает на них «нет такого проекта»
	// на каждом проходе, а уборка системного прохода снесла бы их рабочие папки.
	projects = projects.For(*trackerName)

	forges, err := forgesOf(projects)
	if err != nil {
		return nil, err
	}
	workspaces, err := workspace.Default()
	if err != nil {
		return nil, err
	}
	// Реестр прогонов ведётся всегда, бюджеты — необязательны. Файлов у них два —
	// дефолты офиса и накладка машины, — и нет ни одного значит нет лимитов:
	// это нормальное состояние офиса: учёт от него не зависит.
	runs, err := ledger.Default()
	if err != nil {
		return nil, err
	}
	budgets, err := budget.Load(
		sources.office(configRoot, budget.File),
		sources.machine(home, budget.File),
	)
	if err != nil {
		return nil, err
	}
	configSHA, err := runner.ConfigSHA(configRoot)
	if err != nil {
		return nil, err
	}
	// Уборщик песочниц нужен reap: убитый раннер оставляет за собой живую
	// microVM, и снести её больше некому.
	sandboxes, err := runagent.SandboxesOf(*backend)
	if err != nil {
		return nil, err
	}

	return &pipeline.Office{
		Tracker:    tasks,
		Trackers:   byRole,
		Workspaces: workspaces,
		Workflow:   workflow,
		Projects:   projects,
		Forges:     forges,
		Agent:      pipeline.SandboxAgent{ConfigRoot: configRoot, Backend: *backend, Log: out},
		Sandboxes:  sandboxes,
		Ledger:     runs,
		Budgets:    budgets,
		ConfigRoot: configRoot,
		ConfigSHA:  configSHA,
		Accounts:   accounts,
		Log:        out,
	}, nil
}

// forgesOf собирает реализации forge, нужные названным проектам.
//
// Собираются они разом и заранее, как трекеры ролей: узнать об отсутствующем
// токене в середине прохода, уже пообещав задаче pull request, было бы поздно.
//
// Проект без forge — законное состояние, а не недонастроенное: оба полигона
// смотрят в локальный bare-репозиторий, и PR-проход для них вырождается.
func forgesOf(projects tracker.Projects) (map[string]forge.Forge, error) {
	github := map[string]string{}
	for _, key := range projects.Keys() {
		project := projects[key]
		switch project.Forge {
		case "":
		case forge.Kind:
			github[key] = project.RepoURL
		default:
			return nil, fmt.Errorf("проект %s: forge=%q, известен только %s", key, project.Forge, forge.Kind)
		}
	}
	if len(github) == 0 {
		return nil, nil
	}

	impl, err := forge.NewGitHub(github)
	if err != nil {
		return nil, err
	}
	return map[string]forge.Forge{forge.Kind: impl}, nil
}

// tickCommand — один цикл: разобрать ответы человека, взять не больше одной
// задачи, выполнить и вернуть в граф.
func tickCommand(args []string, out io.Writer) error {
	fs := flags("tick")
	role := fs.String("role", "", "роль из workflow.yaml; без неё — по циклу на каждую роль")

	o, err := office(fs, args, out)
	if err != nil {
		return err
	}
	ctx := context.Background()
	if *role == "" {
		return o.TickAll(ctx)
	}
	worked, err := o.Tick(ctx, *role)
	if err != nil {
		return err
	}
	if !worked {
		fmt.Fprintf(out, "%s: работы нет\n", *role)
	}
	return nil
}

// reapCommand возвращает в очередь задачи с истёкшей арендой.
func reapCommand(args []string, out io.Writer) error {
	o, err := office(flags("reap"), args, out)
	if err != nil {
		return err
	}
	return o.Reap(context.Background())
}

// completeSplitsCommand достраивает и связывает тикеты-детей подтверждённых
// split-предложений — отдельно от loop, вручную или по cron (по образцу reap).
func completeSplitsCommand(args []string, out io.Writer) error {
	o, err := office(flags("complete-splits"), args, out)
	if err != nil {
		return err
	}
	return o.CompleteSplits(context.Background())
}

// loopCommand гоняет цикл по расписанию, пока не остановят сигналом.
//
// Это не демон: он не следит за собой и не перезапускается. Запускать его
// должен cron, launchd или systemd-timer — примеры в bootstrap/.
func loopCommand(args []string, out io.Writer) error {
	fs := flags("loop")
	role := fs.String("role", "", "роль из workflow.yaml; без неё — по циклу на каждую роль")
	every := fs.Duration("every", 2*time.Minute, "пауза между циклами")

	o, err := office(fs, args, out)
	if err != nil {
		return err
	}
	// Остановка между циклами, а не посреди: прерванный прогон оставил бы
	// задачу арендованной до истечения аренды.
	ctx, stop := signalContext()
	defer stop()

	fmt.Fprintf(out, "цикл каждые %s, остановка по SIGINT или SIGTERM\n", *every)
	return o.Loop(ctx, *every, *role)
}

// configRoot — корень конфиг-репозитория. Его сообщает обёртка bin/runner;
// при прямом запуске берётся текущий каталог.
func configRoot() (string, error) {
	if root := os.Getenv("OFFICE_CONFIG_ROOT"); root != "" {
		return root, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("корень конфигурации не определён: %w", err)
	}
	return cwd, nil
}

// configSources — откуда раннер взял каждый файл конфигурации.
//
// Конфигурация лежит в двух местах: репозиторий описывает офис, ${OFFICE_HOME} —
// этот инстанс. Без строки о каждом файле разбираться, почему офис ведёт себя
// не так, приходится догадками о том, какой из двух он открыл. Файлы, которых нет,
// называются тоже: «нет» — такой же ответ, как путь, и для необязательных
// бюджетов он законный.
type configSources struct {
	out    io.Writer
	header bool
}

// office — файл, описывающий офис: он в конфиг-репозитории.
func (c *configSources) office(root, name string) string { return c.add("офис", root, name) }

// machine — файл, описывающий инстанс: он в хозяйстве раннера.
func (c *configSources) machine(root, name string) string { return c.add("машина", root, name) }

// add печатает строку **сразу**, а не копит её до конца сборки. Это по существу:
// самый нужный случай — отказ загрузчика, и если печатать в конце, то при отказе
// не напечатается ничего. Человек увидит «tracker.yaml не заведён» и пойдёт искать,
// где раннер его ждал, — а строка про путь как раз и не вышла.
func (c *configSources) add(kind, root, name string) string {
	path := filepath.Join(root, name)
	if c.out == nil {
		return path
	}
	if !c.header {
		fmt.Fprintln(c.out, "конфигурация:")
		c.header = true
	}
	state := "есть"
	if _, err := os.Stat(path); os.IsNotExist(err) {
		state = "нет"
	}
	fmt.Fprintf(c.out, "  %-22s %s (%s, %s)\n", name, path, kind, state)
	return path
}
