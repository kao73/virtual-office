package pipeline

import (
	"context"
	"fmt"
	"io"

	"github.com/kao73/virtual-office/internal/runagent"
	"github.com/kao73/virtual-office/internal/runner"
	"github.com/kao73/virtual-office/internal/workspace"
)

// SandboxAgent — настоящий прогон агента: тот же путь, которым идёт ручной
// `run-agent`, только рабочую папку и контекст готовит конвейер.
type SandboxAgent struct {
	ConfigRoot string
	Backend    string
	Log        io.Writer
}

// cloneDirs — то же самое, что cmd/run-agent/main.go называет --clone'у своим
// Dirs: каталог обмена и кэш локального исполнения .comet/runtime — то, что
// git-клон не приносит сам (см. internal/backends/sbx/clone.go).
var cloneDirs = []string{runner.Dir, ".comet/runtime"}

// cloneOptionsFor решает, каким Workdir'ом и Clone'ом снабдить runagent.Execute
// для этого прогона, и отдаёт функцию уборки одноразового клона-источника.
//
// Вынесена из Run отдельно ради проверяемости: решение не трогает ни бэкенд,
// ни настоящего агента, и юнит-тест не должен тратиться на живой sbx или
// claude ради него.
//
// На бэкенде local изоляции --clone нет вовсе (см. runagent.CloneNotice) —
// прогон идёт прямо по req.Workdir, как и раньше, и Clone остаётся nil:
// local.Run это поле не смотрит по контракту, а держать его пустым здесь —
// не полагаться на это молча. cleanup всё равно небустой: вызывающий зовёт
// его безусловно, и на local это просто no-op.
//
// На остальных бэкендах (sbx) заводится одноразовый клон-источник
// (internal/workspace.CloneSource) от той же ветки, что несёт req.Branch —
// PrepareInput конвейера успевает отработать раньше (pipeline.go, work()),
// и клон захватывает любой её системный коммит. Неудача клонирования — повод
// провалить прогон целиком: тихого отката на бинд-маунт нет, задача останется
// арендованной, и её вернёт reaper (docs/superpowers/specs/
// 2026-09-02-pipeline-clone-wiring-design.md).
func cloneOptionsFor(backend string, req Request) (workdir string, clone *runner.CloneSync, cleanup func() error, err error) {
	if backend == runagent.BackendLocal {
		return req.Workdir, nil, func() error { return nil }, nil
	}

	src, cleanupSrc, err := workspace.CloneSource(req.Workdir, req.Branch)
	if err != nil {
		return "", nil, nil, fmt.Errorf("клон-источник для --clone не заведён: %w", err)
	}
	return src, &runner.CloneSync{
		FetchInto: req.Workdir,
		Branch:    req.Branch,
		Dirs:      cloneDirs,
	}, cleanupSrc, nil
}

// Run исполняет прогон.
//
// Ошибку возвращает только то, из-за чего прогона не случилось: конвейер
// понимает её как «задача осталась арендованной, вернёт reaper». Всё, что
// произошло с самим агентом, приходит исходом — включая синтетический failed.
func (a SandboxAgent) Run(ctx context.Context, req Request) (AgentRun, error) {
	// Сеть роли закрывает песочница. Бэкенд, который её не закрывает, обязан
	// сказать об этом вслух: молчание читалось бы как «применено».
	if notice := runagent.NetworkNotice(a.Backend, req.Role.Network.Allow); notice != "" {
		a.logf("%s: %s", req.Passport.TaskKey, notice)
	}
	// А закрывает ли что-нибудь сама машина — вопрос отдельный: базовую политику
	// ставит человек, и без неё список роли ничего не ограничивает. Спрашивается
	// это перед каждым прогоном, а не при старте раннера: политику машины меняют
	// одной командой, и прогон обязан говорить о той сети, которую получил сам.
	if notice := runagent.NetworkAudit(a.Backend); notice != "" {
		a.logf("%s: %s", req.Passport.TaskKey, notice)
	}
	// Изоляция --clone запрашивается конвейером всегда: применимость зависит
	// только от бэкенда, и об этом надо сказать вслух там, где он её не даёт —
	// тем же приёмом, что и NetworkNotice выше.
	if notice := runagent.CloneNotice(a.Backend, true); notice != "" {
		a.logf("%s: %s", req.Passport.TaskKey, notice)
	}

	workdir, clone, cleanup, err := cloneOptionsFor(a.Backend, req)
	if err != nil {
		return AgentRun{}, err
	}
	defer func() {
		if err := cleanup(); err != nil {
			a.logf("%s: клон-источник --clone не убран: %v", req.Passport.TaskKey, err)
		}
	}()

	opts := runagent.Options{
		ConfigRoot: a.ConfigRoot,
		Role:       req.Role,
		Workdir:    workdir,
		Backend:    a.Backend,
		Passport:   req.Passport,
		Clone:      clone,
	}
	// Mounts и Clone несовместимы (runagent.Prepare это проверяет сама) —
	// бинд-маунт нужен только там, где --clone нет вовсе.
	if clone == nil {
		opts.Mounts = req.Mounts
	}

	out, err := runagent.Execute(ctx, opts)
	// Пределы поставщика — наблюдение, а не решение: исход прогона от них
	// не зависит, но человек о них узнаёт. Об открытом окне не говорится —
	// оно открыто у каждого прогона, и строка о нём стояла бы над каждым.
	if notice := out.Limit.Notice(); notice != "" {
		a.logf("%s: %s", req.Passport.TaskKey, notice)
	}

	run := AgentRun{Result: out.Result, Usage: out.Usage, Termination: out.Termination}

	if err != nil {
		// Исход есть — значит прогон состоялся, а сорвалось что-то после него
		// (например, архивация). Хоронить из-за этого задачу незачем, но и молчать
		// нельзя: беда уходит в лог раннера.
		if out.Result.Outcome != "" {
			a.logf("%s: %v", req.Passport.TaskKey, err)
			return run, nil
		}
		return AgentRun{}, err
	}
	a.logf("%s: прогон кончился как %s, лог %s, архив %s",
		req.Passport.TaskKey, out.Termination.Kind, out.LogPath, out.Archive)
	return run, nil
}

func (a SandboxAgent) logf(format string, args ...any) {
	if a.Log == nil {
		return
	}
	fmt.Fprintf(a.Log, format+"\n", args...)
}
