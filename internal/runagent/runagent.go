// Package runagent — один прогон агента: роль плюс рабочая папка на входе,
// машиночитаемый результат на выходе.
//
// Здесь сходятся три части, которые друг о друге не знают: контракт обмена
// (runner), перевод роли в вызов конкретного агента (adapters) и место
// исполнения (backends). Пакет нужен затем, чтобы прогон звали одинаково
// и CLI `run-agent` для ручной отладки, и конвейер этапа 2.
//
// Каталог обмена пакет **не готовит**: постановку и контекст собирает тот,
// кто их знает, — CLI из файла, конвейер из тикета.
package runagent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kao73/virtual-office/internal/adapters/claude"
	"github.com/kao73/virtual-office/internal/backends/local"
	"github.com/kao73/virtual-office/internal/backends/sbx"
	"github.com/kao73/virtual-office/internal/runner"
)

// Backend по умолчанию: изоляция должна быть тем, что получаешь, ничего не указав.
const DefaultBackend = "sbx"

// BackendLocal — запуск прямо на хосте, без изоляции. Выбирается явно и границей
// не является: агент бежит в файловой системе и в сети владельца машины.
const BackendLocal = "local"

// NetworkNotice — что сказать о сети перед прогоном. Пусто — сказать нечего.
//
// Роль называет домены, а закрывает сеть песочница. Бэкенд local песочницы
// не заводит вовсе, поэтому список роли на нём не значит ничего — и человек,
// вписавший его, обязан это услышать. Молчание читалось бы как «применено».
//
// Про роль без сети на local не говорится: у этого бэкенда изоляции нет вовсе,
// и сказано об этом там, где его выбирают (README, DESIGN §2.6). Строка на каждый
// прогон отладки утопила бы ту, которая важна.
func NetworkNotice(backend string, allow []string) string {
	if backend != BackendLocal || len(allow) == 0 {
		return ""
	}
	return fmt.Sprintf("бэкенд %s сетевой политики не применяет: роль просит %s, "+
		"а агент бежит в сети хоста и дотянется куда угодно",
		BackendLocal, strings.Join(allow, ", "))
}

// NetworkAudit — что сказать о базовой политике машины перед прогоном.
// Пусто — сказать нечего: сеть закрыта, и обещание «только по списку роли» держится.
//
// Проверка нужна потому, что закрытое умолчание раннеру не подчиняется. Своё
// правило он выдаёт разрешающим, а запрет сильнее разрешения, — значит закрыть
// сеть может только человек и только на машине. Раз закрыть нельзя, остаётся
// знать: на машине с открытой политикой роль без единого домена получит всю сеть,
// и без этой строки об этом не узнает никто (DESIGN §2.6, docs/notes/sbx.md).
//
// Бэкенд local не спрашивается вовсе: песочниц у него нет, а про его сеть
// сказано в NetworkNotice.
//
// Неудача проверки говорится вслух и не выдаётся за закрытую сеть: «не узнал» —
// это не «закрыто». Прогон при этом не отменяется: гарантию сети даёт машина,
// а роняет прогон только то, что делает работу невозможной.
func NetworkAudit(backend string) string {
	if backend == BackendLocal {
		return ""
	}
	notice, err := sbx.BasePolicy{}.Notice()
	if err != nil {
		return fmt.Sprintf("базовая политика сети не проверена, "+
			"закрыта ли она — неизвестно: %v", err)
	}
	return notice
}

// Options — что нужно для прогона.
type Options struct {
	ConfigRoot string      // корень конфиг-репозитория: оттуда роль и ограждения
	Role       runner.Role // уже загруженная роль
	Workdir    string      // рабочая папка агента; каталог обмена уже подготовлен
	Backend    string      // local или sbx
	Passport   runner.Run  // паспорт прогона

	// Mounts — что отдать изоляции сверх рабочей папки. Для worktree сюда идёт
	// bare-репозиторий: без него git внутри песочницы не заводится.
	Mounts []runner.Workspace
}

// Launch — подготовленный, но не исполненный запуск. Им пользуется --dry-run:
// посмотреть, что получит агент, не тратя токенов.
type Launch struct {
	*runner.Launch
	Platform  runner.Platform
	Validator string
}

// Outcome — чем кончился прогон.
type Outcome struct {
	Result runner.Result
	// Usage — во что прогон обошёлся. Наблюдение раннера, а не заявление агента:
	// в result.json этих полей нет, читаются они из лога прогона. Пустое значение
	// законно — прогон, убитый на середине, о расходе не отчитывается.
	Usage runner.Usage
	// Limit — что лог сказал о пределах поставщика. Пока только наблюдение:
	// исход прогона от него не зависит, но человек о нём узнаёт.
	Limit runner.Limit
	// Termination — чем прогон кончился и почему так решено. В отличие
	// от Limit, это наблюдение решает: «не начинал» и «не успел» не тратят
	// попытку задачи, «не справился» тратит.
	Termination runner.Termination
	ExitCode    int    // код выхода агента; результат всё равно считается истиной
	LogPath     string // куда писался вывод агента
	Archive     string // копия каталога обмена; пусто, если заархивировать не вышло
}

// Prepare собирает запуск: ограждение под платформу бэкенда, промпты, настройки,
// командную строку. Ничего не исполняет.
func Prepare(opts Options) (Launch, error) {
	_, target, err := backendByName(opts.Backend)
	if err != nil {
		return Launch{}, err
	}

	// Ограждение проверяет результат тем же кодом, что и раннер, поэтому бинарник
	// собирается из конфиг-репозитория под платформу выбранного бэкенда.
	validator, err := runner.EnsureValidator(opts.ConfigRoot, target)
	if err != nil {
		return Launch{}, err
	}

	launch, err := claude.Build(opts.Role, opts.Workdir, opts.Passport, validator)
	if err != nil {
		return Launch{}, err
	}
	launch.Workspaces = append(launch.Workspaces, opts.Mounts...)

	return Launch{Launch: launch, Platform: target, Validator: validator}, nil
}

// Execute готовит запуск, исполняет его и разбирает результат.
//
// Ошибку возвращает только инфраструктура: не собралась роль, нет креда, не встал
// бэкенд. Всё, что случилось с самим агентом, — это Outcome с исходом, в том числе
// синтетический failed, когда агент не оставил валидного результата.
func Execute(ctx context.Context, opts Options) (Outcome, error) {
	run, _, err := backendByName(opts.Backend)
	if err != nil {
		return Outcome{}, err
	}

	launch, err := Prepare(opts)
	if err != nil {
		return Outcome{}, err
	}
	defer func() { _ = launch.Cleanup() }()

	logPath := filepath.Join(opts.Workdir, runner.Dir, runner.FileLog)
	exitCode, runErr := run(ctx, launch.Launch, logPath)

	usage := usageOf(logPath)

	result, err := runner.ReadResult(opts.Workdir)
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

	out := Outcome{
		Result:      result,
		Usage:       usage,
		Limit:       limitOf(logPath),
		Termination: terminationOf(opts, err == nil, endingOf(logPath), usage.Turns, runErr),
		ExitCode:    exitCode,
		LogPath:     logPath,
	}

	// Каталог обмена эфемерен: рабочей папкой служит worktree, а его удаляют.
	// Неудача архивации не подменяет исход прогона — материал в этот момент
	// ещё цел, — но и молчать о ней нельзя, поэтому она видна в Outcome.
	if out.Archive, err = runner.Archive(opts.Workdir, opts.Passport.RunID); err != nil {
		return out, fmt.Errorf("прогон не заархивирован: %w", err)
	}
	return out, nil
}

// usageOf читает расход прогона из его лога.
func usageOf(logPath string) runner.Usage { return fromLog(logPath, claude.ParseUsage) }

// endingOf читает оттуда же то, что агент сказал о конце прогона. Подсказка,
// а не приговор: решает terminationOf, и решает по следу работы.
func endingOf(logPath string) runner.Ending { return fromLog(logPath, claude.ParseEnding) }

// terminationOf решает, чем кончился прогон.
//
// Три источника, и ни одному из них раннер не верит на слово: событие лога —
// подсказка, таймаут — факт от бэкенда, след работы — причина. Правило
// «проверять причину, а не следствие» здесь не украшение: `subtype` итогового
// события у оборванного прогона равен `"success"`, и офис на этом уже горел
// (DESIGN §2.3, docs/notes/stage-4-load.md).
//
// Порядок вопросов задан ценой ошибки:
//
//  1. Результат есть — прогон состоялся, чем бы ни кончился лог. Работа сделана
//     и опубликована, спорить не о чем.
//  2. Иначе усечение: предел шагов или таймаут. Оба означают «работал
//     и не успел», и продолжать надо с того же места.
//  3. Иначе решает след: пуст — не начинал, есть — поработал и сломался.
func terminationOf(opts Options, hasResult bool, ending runner.Ending, turns int, runErr error) runner.Termination {
	if hasResult {
		return runner.Termination{Kind: runner.TerminationCompleted}
	}

	switch {
	case errors.Is(runErr, runner.ErrRunTimeout):
		return runner.Termination{
			Kind:   runner.TerminationTruncated,
			Detail: "прогон остановлен по таймауту роли, отчитаться агент не успел",
		}
	case ending.Reason == runner.EndMaxTurns:
		return runner.Termination{
			Kind:   runner.TerminationTruncated,
			Detail: fmt.Sprintf("предел шагов роли исчерпан на %d-м шаге, отчитаться агент не успел", turns),
		}
	}

	left, err := runner.LeftTrace(opts.Workdir, opts.Passport.BaseCommit, turns)
	if err != nil {
		// Судить след не вышло — рабочая папка сломана. Считаем «не справился»,
		// и это выбор в сторону строгости: not_started и truncated попытку
		// не тратят, и уйти в них по незнанию значило бы молча ослабить предел.
		// Прогон, о котором нельзя сказать ничего, — это errored.
		return runner.Termination{
			Kind:   runner.TerminationErrored,
			Detail: fmt.Sprintf("след работы не разобран (%v), прогон считается сломавшимся", err),
		}
	}
	if left {
		return runner.Termination{Kind: runner.TerminationErrored, Detail: endDetail(ending, runErr)}
	}
	return runner.Termination{Kind: runner.TerminationNotStarted, Detail: endDetail(ending, runErr)}
}

// endDetail — что сказать человеку о прогоне, не оставившем результата.
//
// Слова агента идут первыми: «API Error: Unable to connect to API (ECONNRESET)»
// объясняет больше, чем любой наш пересказ. Дальше — слово поставщика, если
// оно незнакомо разбору, и уже потом беда самого запуска.
func endDetail(ending runner.Ending, runErr error) string {
	switch {
	case ending.Detail != "":
		return ending.Detail
	case ending.Reason == runner.EndUnknown && ending.Provider != "":
		return fmt.Sprintf("агент сказал о конце прогона незнакомое: %q", ending.Provider)
	case runErr != nil:
		return runErr.Error()
	default:
		return "агент завершился, не оставив ни результата, ни объяснения"
	}
}

// limitOf читает оттуда же состояние окна поставщика: исчерпанное окно —
// такое же наблюдение раннера за прогоном, как и цена, и в result.json его нет
// тем более (агент, отвергнутый окном, файла результата не пишет вовсе).
func limitOf(logPath string) runner.Limit { return fromLog(logPath, claude.ParseLimit) }

// fromLog читает лог прогона разбором адаптера.
//
// Неудача чтения — не беда прогона: он уже состоялся, работа сделана, а раннер
// просто не узнает, во что она обошлась и в каком состоянии были пределы.
// Ошибку здесь возвращать некому и незачем — пустое значение и означает
// «неизвестно».
func fromLog[T any](logPath string, parse func(io.Reader) T) T {
	log, err := os.Open(logPath)
	if err != nil {
		var unknown T
		return unknown
	}
	defer func() { _ = log.Close() }()
	return parse(log)
}

// backendRun — исполнение подготовленного запуска. Подпись одна у всех бэкендов:
// адаптер знает устройство агента, бэкенд — устройство изоляции.
type backendRun func(context.Context, *runner.Launch, string) (int, error)

// Sandboxes — уборка песочниц прогонов, не переживших своего раннера.
// Реализует её тот бэкенд, у которого песочницы есть.
type Sandboxes interface {
	Remove(runID string) (bool, error)
}

// SandboxesOf выдаёт уборщика песочниц бэкенда. Пустой ответ означает, что
// убирать нечего: у local никаких песочниц нет, агент бежит прямо на хосте.
//
// Живёт рядом с backendByName намеренно: знание об устройстве бэкендов —
// одно место, и вызывающему не нужно перечислять их имена ещё раз.
func SandboxesOf(name string) (Sandboxes, error) {
	if _, _, err := backendByName(name); err != nil {
		return nil, err
	}
	switch name {
	case "sbx", "":
		return sbx.Sandboxes{}, nil
	default:
		return nil, nil
	}
}

// backendByName выдаёт исполнителя и платформу, под которой он запускает агента.
func backendByName(name string) (backendRun, runner.Platform, error) {
	switch name {
	case BackendLocal:
		return local.Run, local.Platform(), nil
	case "sbx", "":
		return sbx.Run, sbx.Platform(), nil
	case "docker":
		return nil, runner.Platform{}, errors.New("бэкенд docker отложен: изоляцию закрывает sbx, докер понадобится на машине без KVM")
	default:
		return nil, runner.Platform{}, fmt.Errorf("неизвестный бэкенд %q: доступны local и sbx", name)
	}
}
