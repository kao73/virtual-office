package pipeline

import (
	"context"
	"fmt"
	"io"

	"github.com/kao73/virtual-office/runagent"
	"github.com/kao73/virtual-office/runner"
)

// SandboxAgent — настоящий прогон агента: тот же путь, которым идёт ручной
// `run-agent`, только рабочую папку и контекст готовит конвейер.
type SandboxAgent struct {
	ConfigRoot string
	Backend    string
	Log        io.Writer
}

// Run исполняет прогон.
//
// Ошибку возвращает только то, из-за чего прогона не случилось: конвейер
// понимает её как «задача осталась арендованной, вернёт reaper». Всё, что
// произошло с самим агентом, приходит исходом — включая синтетический failed.
func (a SandboxAgent) Run(ctx context.Context, req Request) (runner.Result, runner.Usage, error) {
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

	out, err := runagent.Execute(ctx, runagent.Options{
		ConfigRoot: a.ConfigRoot,
		Role:       req.Role,
		Workdir:    req.Workdir,
		Backend:    a.Backend,
		Passport:   req.Passport,
		Mounts:     req.Mounts,
	})
	if err != nil {
		// Исход есть — значит прогон состоялся, а сорвалось что-то после него
		// (например, архивация). Хоронить из-за этого задачу незачем, но и молчать
		// нельзя: беда уходит в лог раннера.
		if out.Result.Outcome != "" {
			a.logf("%s: %v", req.Passport.TaskKey, err)
			return out.Result, out.Usage, nil
		}
		return runner.Result{}, runner.Usage{}, err
	}
	a.logf("%s: лог прогона %s, архив %s", req.Passport.TaskKey, out.LogPath, out.Archive)
	return out.Result, out.Usage, nil
}

func (a SandboxAgent) logf(format string, args ...any) {
	if a.Log == nil {
		return
	}
	fmt.Fprintf(a.Log, format+"\n", args...)
}
