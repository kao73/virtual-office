// Package pipeline — конвейер: берёт задачу из трекера, готовит рабочую папку,
// зовёт агента, разбирает исход и двигает задачу по графу.
//
// Никакого LLM здесь нет и быть не может. Всё, что конвейер «решает», решается
// по `workflow.yaml` и `result.json`: куда двигать задачу, считать ли попытку,
// звать ли человека. Промпт на это не влияет (DESIGN §2.1).
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/kao73/virtual-office/budget"
	"github.com/kao73/virtual-office/forge"
	"github.com/kao73/virtual-office/guard"
	"github.com/kao73/virtual-office/ledger"
	"github.com/kao73/virtual-office/runner"
	"github.com/kao73/virtual-office/tracker"
	"github.com/kao73/virtual-office/workspace"
)

// AgentRun — всё, что прогон принёс конвейеру.
//
// Заявление агента здесь ровно одно — Result. Остальное наблюдает раннер, и это
// не педантизм: result.json пишет агент, и смешать его слова с наблюдениями
// значило бы дать ему называть и свою цену, и причину собственной смерти.
type AgentRun struct {
	// Result — что агент сказал о работе. У прогона без результата это
	// синтетический failed, и верить ему нельзя: смотреть надо на Termination.
	Result runner.Result
	// Usage — во что прогон обошёлся. Пустое значение законно: прогон, убитый
	// на середине, о цене не отчитывается.
	Usage runner.Usage
	// Termination — чем прогон кончился. Решает маршрут раньше исхода: «не
	// начинал» и «не успел» не тратят попытку задачи, «не справился» тратит.
	Termination runner.Termination
}

// Agent — один прогон агента. Интерфейс нужен ровно затем, чтобы конвейер
// проверялся целиком без трат на модель: в тестах его закрывает подделка,
// возвращающая заданный результат.
type Agent interface {
	Run(ctx context.Context, req Request) (AgentRun, error)
}

// Request — что конвейер отдаёт агенту. Каталог обмена к этому моменту
// уже подготовлен: постановка, контекст и паспорт лежат в рабочей папке.
type Request struct {
	Role     runner.Role
	Workdir  string
	Passport runner.Run
	Mounts   []runner.Workspace
}

// Sandboxes — уборка песочниц прогонов, не переживших своего раннера.
// Реализует бэкенд: имя песочницы знает он, а reap знает run_id мёртвой аренды.
// Пустое значение означает «убирать нечего» — так устроен бэкенд local.
type Sandboxes interface {
	// Remove сносит песочницу прогона и отвечает, нашлось ли что сносить:
	// на машине, где прогон не жил, её нет и не было.
	Remove(runID string) (bool, error)
}

// Office — конвейер над одним трекером.
type Office struct {
	// Tracker — трекер под общей учёткой офиса. Под ней идёт всё, у чего роли нет:
	// reap, разбор ответов человека, системные записи.
	Tracker tracker.Tracker

	// Trackers — трекер на роль, если у роли своя учётка. Роли без записи
	// работают через Tracker: одна учётка на всех агентов — штатный режим,
	// роли раннер различает по маркеру, а не по автору.
	Trackers map[string]tracker.Tracker

	Workspaces *workspace.Manager
	Workflow   tracker.Workflow
	Projects   tracker.Projects
	Agent      Agent
	Sandboxes  Sandboxes

	// Forges — реализации forge по имени из projects.local.yaml. Проект без forge
	// живёт по тому же графу: PR-проход для него вырождается. Пустая карта
	// означает офис, который pull request не открывает вовсе.
	Forges map[string]forge.Forge

	// Ledger — учёт прогонов. Ведётся всегда и ничего не решает; пустой означает,
	// что учёта нет вовсе — так конвейер проверяется там, где расход не при чём.
	Ledger *ledger.Ledger
	// Budgets — необязательная политика поверх учёта. Пустая означает «только учёт»:
	// это естественное состояние офиса, а не недонастроенное.
	Budgets budget.Budgets

	ConfigRoot string // корень конфиг-репозитория: оттуда роли
	ConfigSHA  string // отпечаток конфигурации для маркеров

	// Accounts — все учётки офиса: общая, учётки ролей и чужая автоматизация.
	// Всё, написанное не ими, считается словами человека. Список собирается
	// один раз при сборке офиса, а не спрашивается у трекера на каждом тике.
	Accounts []string

	Now func() time.Time
	Log io.Writer

	// said — о чём в этом процессе уже сказано в лог. Нужно одному сообщению:
	// «роль исчерпала дневной бюджет» повторялось бы каждый цикл, а цикл идёт
	// раз в две минуты. Память живёт ровно столько, сколько процесс: отдельный
	// запуск скажет своё, и заводить ради этого файл-отметку в хозяйстве
	// раннера было бы состоянием без хозяина.
	said map[string]bool
}

// Tick — один цикл роли: разобрать ответы человека, взять не больше одной задачи,
// выполнить её и вернуть в граф. Отвечает, нашлась ли работа.
func (o *Office) Tick(ctx context.Context, roleName string) (bool, error) {
	// Ответы человека разбираются раньше поиска работы: разблокированная задача
	// должна попасть в очередь до того, как роль выберет кандидата. Проход идёт
	// и здесь, и в TickAll: cron с одной ролью не должен оставлять ожидающие
	// задачи в ожидании навсегда.
	if _, err := o.HumanReplies(ctx); err != nil {
		return false, err
	}
	// Системный проход тоже безролевой и идёт до работы: ответ человека мог
	// вернуть задачу в очередь прохода, и ждать следующего цикла ей незачем.
	if err := o.PRPass(ctx); err != nil {
		return false, err
	}
	return o.as(roleName).tickRole(ctx, roleName)
}

// as — тот же офис, ходящий в трекер под учёткой роли. Копия мелкая и живёт
// один тик: меняется только трекер, всё остальное — общее хозяйство.
func (o *Office) as(roleName string) *Office {
	// Память лога заводится здесь, на корневом офисе, и достаётся копиям вместе
	// с остальным хозяйством. Заводить её внутри тика было бы бессмысленно:
	// копия живёт один заход, и сказанное ею забывалось бы сразу.
	if o.said == nil {
		o.said = map[string]bool{}
	}

	role, found := o.Trackers[roleName]
	if !found {
		return o
	}
	clone := *o
	clone.Tracker = role
	return &clone
}

// sayOnce — говорить ли об этом в лог. Второй раз за жизнь процесса — уже нет.
func (o *Office) sayOnce(what string) bool {
	if o.said == nil {
		// Офис собран без памяти: повтор не подавляется. Соврать в другую
		// сторону — промолчать о том, о чём ещё не говорили, — было бы хуже.
		return true
	}
	if o.said[what] {
		return false
	}
	o.said[what] = true
	return true
}

// tickRole — цикл одной роли без разбора ответов: его делает вызывающий,
// один раз на весь заход, сколько бы ролей ни было в графе.
func (o *Office) tickRole(ctx context.Context, roleName string) (bool, error) {
	flow, err := o.Workflow.Role(roleName)
	if err != nil {
		return false, err
	}
	role, err := runner.LoadRole(o.ConfigRoot, roleName)
	if err != nil {
		return false, err
	}

	// Дневной предел роли — до всего остального: он не про задачу, а про роль,
	// и спрашивать очередь, чтобы потом всё равно ничего не взять, незачем.
	switch stop, err := o.roleOverspent(roleName); {
	case err != nil:
		return false, err
	case stop:
		return false, nil
	}

	task, err := o.claim(roleName, flow, role)
	if err != nil || task.ref.Key == "" {
		return false, err
	}
	// Захват только что положил задачу в рабочий статус — есть на чём спросить
	// трекер о его workflow. Без работы этот вопрос не задаётся вовсе.
	o.checkWorkflow(task.ref.Project, flow)

	// Рабочую папку прогон за собой не убирает: её убирает системный проход,
	// обходя папки, а не задачи. Задача, дошедшая до конца, ещё ждёт слияния,
	// и вернуться в её папку может понадобиться конфликтом.
	return true, o.work(ctx, task, roleName, flow, role)
}

// checkWorkflow предупреждает о workflow, в котором захват задачи не может стать
// CAS: рабочий статус, доступный переходом из него самого, позволяет двум прогонам
// захватить одну задачу и уйти работать в один worktree.
//
// Проверка ничего не блокирует — с одним раннером на проект такой workflow
// безопасен, и это ровно тот случай, в котором офис работает сейчас. Сколько
// раннеров запущено на самом деле, отсюда не видно: сказать об этом человеку
// раннер может, решить за него — нет.
//
// Зовётся она после захвата, а не при старте раннера, и это не мелочь. Переходы
// трекер показывает только у конкретной задачи, а задача в рабочем статусе есть
// не всегда — при старте вопрос чаще всего оставался без ответа, и раннер вместо
// проверки печатал «проверить было не на чем» на каждый заход. Захват же кладёт
// задачу в рабочий статус сам: образец появляется ровно к моменту вопроса,
// а тик без работы молчит и ничего не спрашивает.
//
// Спрашивается один раз на проект за жизнь процесса: workflow меняют руками
// и редко, а захватов за час бывают десятки. Второй попытки не получает и неудачная:
// строка о ней в логе уже есть, а долбить трекер вопросом, на который он только
// что не ответил, незачем — проверка ничего не решает.
//
// Трекер, не умеющий отвечать про свой workflow, пропускается молча: у файлового
// workflow нет вовсе, и жаловаться было бы не на что. Роль без рабочего статуса —
// тоже: её захват статуса не меняет, и лазейка к ней не относится.
func (o *Office) checkWorkflow(project string, flow tracker.RoleFlow) {
	checker, able := o.Tracker.(tracker.WorkflowChecker)
	if !able || flow.Working == "" {
		return
	}
	if !o.sayOnce("workflow:" + project + ":" + flow.Working) {
		return
	}

	check, err := checker.CheckWorkflow(project, flow.Working)
	switch {
	case err != nil:
		o.logf("%s: проверка workflow не выполнена: %v", project, err)
	case check.Sample == "":
		// Задачу в рабочий статус только что перевели мы сами. Не найти её
		// там — не «нечего проверять», а расхождение трекера с самим собой.
		o.logf("%s: проверка workflow не выполнена: трекер не видит задач в рабочем статусе %s, "+
			"хотя задача туда только что переведена", project, flow.Working)
	case check.SelfEntry:
		o.logf("workflow %s допускает двух владельцев (в %s можно войти из него самого); "+
			"безопасно только с одним раннером на проект, см. docs/contracts/tracker-protocol.md",
			project, flow.Working)
	}
}

// TickAll прогоняет по циклу на каждую роль графа, в порядке имён.
//
// Ответы человека разбираются один раз на весь заход, а не перед каждой ролью:
// проход безролевой, и повторять его — лишние запросы к трекеру ради заведомо
// пустого результата.
func (o *Office) TickAll(ctx context.Context) error {
	if _, err := o.HumanReplies(ctx); err != nil {
		return err
	}
	if err := o.PRPass(ctx); err != nil {
		return err
	}
	// Порядок обхода задаёт граф: он не выводится ни из имён, ни из порядка
	// YAML-карты. Сначала разгрузить конвейер, потом брать новое.
	for _, role := range o.Workflow.Order() {
		if _, err := o.as(role).tickRole(ctx, role); err != nil {
			return err
		}
	}
	return nil
}

// claimed — взятая в работу задача: рабочая папка и аренда уже наши.
type claimed struct {
	ref     tracker.TaskRef
	runID   string
	project tracker.Project
	ws      workspace.Workspace
}

// claim выбирает первого годного кандидата и берёт его. Пустой ключ означает,
// что работы нет.
func (o *Office) claim(roleName string, flow tracker.RoleFlow, role runner.Role) (claimed, error) {
	lease := o.now().Add(time.Duration(role.Limits.TimeoutSec)*time.Second + o.Workflow.LeaseMargin())

	for _, project := range o.projects() {
		refs, err := o.Tracker.ListReady(project, flow.ReadsFrom)
		if o.skipProject(project, err) {
			continue
		}
		if err != nil {
			return claimed{}, err
		}
		for _, ref := range refs {
			if ref.Attempts >= o.Workflow.Limits.MaxAttempts {
				o.logf("%s: попытки исчерпаны (%d), пропускаю", ref.Key, ref.Attempts)
				continue
			}
			task, taken, err := o.take(ref, roleName, flow, lease)
			if err != nil {
				return claimed{}, err
			}
			if taken {
				return task, nil
			}
		}
	}
	return claimed{}, nil
}

// take берёт одного кандидата: сперва рабочую папку, потом задачу.
//
// Порядок именно такой, и это не косметика. Захват в JIRA не CAS, поэтому
// на одной машине два прогона могут счесть задачу своей; захватывая первым,
// второй успевал перезаписать аренду первого — и только потом упирался в замок
// рабочей папки. Барьер, взятый раньше аренды, останавливает его до того, как
// он тронет трекер.
//
// Занятая папка и проигранный захват — не беда, а обычное дело: берём следующего
// кандидата. Замок при этом снимается, иначе победитель гонки упрётся в него
// на пустом месте.
func (o *Office) take(ref tracker.TaskRef, roleName string, flow tracker.RoleFlow, lease time.Time) (claimed, bool, error) {
	project, err := o.Projects.Get(ref.Project)
	if err != nil {
		// Проект отсеивается до захвата: раньше задача успевала стать
		// арендованной и ждала reaper'а, хотя работать над ней было негде.
		o.logf("%s: %v, пропускаю", ref.Key, err)
		return claimed{}, false, nil
	}

	// Бюджет задачи — до Ensure, а не просто «до захвата»: иначе на задачу,
	// которую всё равно пропустим, уже заведён клон проекта и worktree.
	switch skip, err := o.taskOverspent(ref, roleName, flow); {
	case err != nil:
		return claimed{}, false, err
	case skip:
		return claimed{}, false, nil
	}

	ws, err := o.Workspaces.Ensure(ref, project)
	if errors.Is(err, workspace.ErrWorktreeBusy) {
		o.logf("%s: %v, беру следующую", ref.Key, err)
		return claimed{}, false, nil
	}
	if err != nil {
		return claimed{}, false, err
	}

	runID, err := runner.NewRunID()
	if err != nil {
		o.unlock(ref.Key, ws)
		return claimed{}, false, err
	}

	err = o.Tracker.Claim(tracker.ClaimRequest{
		Key: ref.Key, RunID: runID, Owner: roleName, LeaseUntil: lease,
		ExpectStatus: flow.ReadsFrom, WorkingStatus: flow.Working,
	})
	switch {
	case errors.Is(err, tracker.ErrClaimLost):
		o.logf("%s: захват не удался (%v), беру следующую", ref.Key, err)
		o.unlock(ref.Key, ws)
		return claimed{}, false, nil
	case err != nil:
		o.unlock(ref.Key, ws)
		return claimed{}, false, err
	}

	o.logf("%s: захвачена, прогон %s до %s", ref.Key, runID, lease.Format(time.RFC3339))
	return claimed{ref: ref, runID: runID, project: project, ws: ws}, true, nil
}

// unlock снимает барьер рабочей папки, жалуясь в лог на неудачу: продолжать
// после неё можно, а молчать нельзя — следующий прогон упрётся в этот замок.
func (o *Office) unlock(key string, ws workspace.Workspace) {
	if err := ws.Unlock(); err != nil {
		o.logf("%s: замок рабочей папки не снят: %v", key, err)
	}
}

// work выполняет взятую задачу и возвращает её в граф.
//
// Рабочую папку прогон за собой не сносит: она переживает его до слияния, и
// убирает её системный проход, обходя папки, а не задачи. Здесь на defer висит
// только снятие замка.
func (o *Office) work(ctx context.Context, c claimed, roleName string, flow tracker.RoleFlow, role runner.Role) error {
	task, err := o.Tracker.Get(c.ref.Key)
	if err != nil {
		return err
	}
	ws, runID := c.ws, c.runID

	// Барьер взят вместе с задачей и держится до конца прогона: пуш и отчёт —
	// тоже работа над ней.
	defer o.unlock(task.Key, ws)

	// Точку отсчёта наблюдает раннер, а не агент: ограждения сверяются с тем,
	// как папка выглядела до прогона, и заявление агента о собственной базе
	// было бы заявлением подсудимого.
	base, err := runner.HeadCommit(ws.Dir)
	if err != nil {
		return err
	}
	passport := runner.Run{
		RunID: runID, Role: roleName, ConfigSHA: o.ConfigSHA,
		StartedAt: o.now(), TaskKey: task.Key, BaseCommit: base,
	}
	// Каталог изменения готовит раннер: роль, объявившая область записи, получает
	// готовые файлы и право их править — создавать ей нечем. Делается это до сборки
	// контекста: путь к каталогу агент узнаёт оттуда, а на первом прогоне каталога
	// ещё нет вовсе. Имени роли здесь нет — поведение следует из её спецификации.
	created, err := runner.PrepareChangeDir(ws.Dir, role, task.Key)
	if err != nil {
		return err
	}
	input := runner.Input{
		Task:       taskBody(task),
		Branch:     ws.Branch,
		BaseBranch: "origin/" + c.project.DefaultBranch,
		Context:    contextBody(task, roleName, o.Accounts, o.Workflow.Limits.MaxAttempts),
	}
	if err := runner.PrepareInput(ws.Dir, role, passport, input); err != nil {
		return err
	}

	// Аренда продлевается, пока агент работает: иначе долгая задача досталась бы
	// reaper'у прямо посреди прогона.
	stop := o.keepLease(ctx, task.Key, runID, role)
	run, runErr := o.Agent.Run(ctx, Request{
		Role: role, Workdir: ws.Dir, Passport: passport, Mounts: ws.Mounts(),
	})
	stop()

	if runErr != nil {
		// Прогон не состоялся: аренда остаётся, задачу вернёт reaper.
		return fmt.Errorf("прогон %s не состоялся: %w", runID, runErr)
	}
	result, usage := run.Result, run.Usage
	if run.Termination.Idle() {
		o.logf("%s: прогон без результата (%s) — %s", task.Key, run.Termination.Kind, run.Termination.Detail)
	} else {
		o.logf("%s: исход %s — %s", task.Key, result.Outcome, result.Summary)
	}

	// Учёт — сразу после прогона и до всего остального: прогон состоялся и уже
	// оплачен, что бы дальше ни случилось с публикацией и арендой. Прогона
	// без результата это касается ровно так же: он тоже оплачен.
	o.account(ledger.Entry{
		RunID: runID, Task: task.Key, Role: roleName, Project: c.ref.Project,
		Started: passport.StartedAt, Usage: usage,
		Outcome: string(result.Outcome), Termination: string(run.Termination.Kind),
		ConfigSHA: o.ConfigSHA,
	})

	// Пуш идёт раньше всего остального и при любом исходе: работа не должна жить
	// только в worktree, который однажды удалят. Публикация ничего в задаче
	// не меняет, поэтому она не ждёт подтверждения аренды — прогон, у которого
	// аренду уже отобрали, всё равно обязан сохранить сделанное.
	branch, published := ws.Branch, ""
	pushed, pushErr := o.Workspaces.Push(ws)
	switch {
	case pushErr != nil:
		o.logf("%s: ветка не опубликована: %v", task.Key, pushErr)
		branch, published = "", fmt.Sprintf("Ветку опубликовать не удалось: %v", pushErr)
	case pushed:
		o.logf("%s: ветка %s опубликована", task.Key, branch)
		published = "Работа опубликована в ветке " + branch + "."
	default:
		branch, published = "", "Публиковать было нечего: новых коммитов прогон не оставил."
	}

	// Продление раньше любой записи в задачу. Прогон, доживший до конца после reap,
	// не вправе её трогать: задачу уже могли отдать другому.
	switch lost, err := o.renew(task, runID, roleName, result, usage, published); {
	case err != nil:
		return err
	case lost:
		return nil
	}

	if pushErr != nil {
		// Неопубликованная работа — не провал агента и не повод двигать задачу
		// вперёд: следующая роль искала бы в origin ветку, которой там нет.
		//
		// Идёт это раньше разбора причины завершения намеренно: беда с публикацией
		// сильнее. У неё своя серия и свой разговор с человеком, а работа, которую
		// не приняли, — забота более срочная, чем то, почему прогон не дожил.
		return o.pushFailed(task, runID, roleName, flow, result, usage, pushErr)
	}

	// Прогон без результата маршрута по графу не выбирает: выбирать не по чему.
	// Отчёта агента тоже нет — его слова были бы выдумкой раннера.
	if run.Termination.Idle() {
		return o.idleRun(task, runID, roleName, flow, run.Termination, usage)
	}

	// Ограждения роли — здесь же, где выбирается маршрут. Хук зовёт их, пока агент
	// жив и может починить сделанное, но прогон, убитый таймаутом или пределом
	// шагов, до хука не доживает; раннер зовёт их всегда. Прогон, у которого
	// отобрали аренду или не удалась публикация, маршрута не выбирает вовсе,
	// и сюда не доходит.
	rp := o.replayOf(role, ws.Dir, passport, result)
	if rp.Reason != "" {
		o.logf("%s: ограждение %s не пропустило прогон: %s", task.Key, rp.Guard, rp.Reason)
		// Вторая строка того же прогона, без расхода: заплачено за него один раз,
		// а сводка обязана показывать исход, по которому задача поехала.
		o.account(ledger.Entry{
			RunID: runID, Task: task.Key, Role: roleName, Project: c.ref.Project,
			Started: passport.StartedAt, Outcome: string(rp.Outcome),
			ConfigSHA: o.ConfigSHA, Overrides: true,
		})
	}
	// Заготовки, до которых у агента не дошли руки, не должны пережить прогон —
	// но убирать их можно только после ограждений: незакоммиченный шаблон
	// в каталоге и есть то, по чему видно, что плана нет.
	if err := runner.SweepChangeDir(ws.Dir, role, created); err != nil {
		o.logf("%s: заготовки каталога изменения не убраны: %v", task.Key, err)
	}

	// Куда задача уехала, здесь больше не спрашивается: рабочую папку убирает
	// системный проход, от папок, а не от задач. Прогон, доведший задачу
	// до конца, папку за собой не сносит — она переживёт его до слияния.
	_, err = o.finish(task, runID, roleName, flow, result, branch, usage, rp)
	return err
}

// pushFailed разбирается с работой, которую не удалось опубликовать.
//
// Отчёт агента при этом не пишется намеренно: он объявил бы передачу, которой
// не было, и посчитался бы кругом ревью. Всё, что агент сделал, остаётся
// в рабочей папке и в локальной ветке, а его итог пересказывает эта запись —
// человеку видно и что сделано, и почему это не уехало.
//
// Задача возвращается в очередь той же роли, счётчик попыток не трогается:
// сломанный remote — беда обвязки, а не агента. Рабочая папка остаётся: следующий
// прогон продолжит с того же места и попробует опубликовать ту же работу.
func (o *Office) pushFailed(task tracker.Task, runID, roleName string, flow tracker.RoleFlow, result runner.Result, usage runner.Usage, pushErr error) error {
	by := tracker.ByRun(runID)
	failures := tracker.PushFailures(task.Comments, roleName) + 1

	if err := o.record(task.Key, by, tracker.Marker{
		RunID: runID, Role: roleName, Event: tracker.EventPushFailed, ConfigSHA: o.ConfigSHA,
	}, fmt.Sprintf("Ветка не опубликована: %v\n\nРабота никуда не делась — она в рабочей папке и в локальной "+
		"ветке, — но пока её не видит никто, кроме этой машины. Итог прогона run:%s — %s: %s\n\n"+
		"Задача возвращается в %s. Счётчик попыток не тронут: публикует ветку раннер, а не агент.%s",
		pushErr, short(runID), result.Outcome, result.Summary, flow.ReadsFrom, spent(usage))); err != nil {
		return err
	}

	to, human := flow.ReadsFrom, false
	if failures >= o.Workflow.Limits.MaxPushFailures {
		to, human = flow.Blocked(), true
		o.logf("%s: пуш не удаётся подряд %d раз, задача уходит к человеку", task.Key, failures)
		if err := o.record(task.Key, by, tracker.Marker{
			RunID: runID, Role: roleName, Event: tracker.EventPushFailuresExhausted, ConfigSHA: o.ConfigSHA,
		}, fmt.Sprintf("Публикация не удаётся %d раз подряд — это предел (limits.max_push_failures). "+
			"Дело не в задаче: смотреть надо на доступ к репозиторию, токен и сам remote. "+
			"Работа всех этих прогонов цела и лежит в рабочей папке.", failures)); err != nil {
			return err
		}
	}

	if err := o.move(task, by, to); err != nil {
		return err
	}
	if human {
		if err := o.Tracker.SetHumanFlag(task.Key, by, true); err != nil {
			return err
		}
	}
	return o.Tracker.Release(task.Key, by)
}

// idleRun разбирается с прогоном, не дошедшим до результата.
//
// Отчёт агента при этом не пишется намеренно, по той же причине, что и при
// неудачной публикации: агент ничего не сказал, и пересказывать за него было бы
// выдумкой. Всё, что он успел сделать, лежит в рабочей папке и в опубликованной
// ветке, а почему прогон кончился — говорит эта запись.
//
// Задача возвращается в очередь той же роли, счётчик попыток не трогается:
// «не начинал» и «не успел» — беды обвязки и поставщика, а не работы. Рабочая
// папка остаётся: усечённый прогон продолжают с того же места, и у implementer'а
// продолжение видно по отметкам в tasks.md, у аналитика — по черновикам.
func (o *Office) idleRun(task tracker.Task, runID, roleName string, flow tracker.RoleFlow,
	term runner.Termination, usage runner.Usage,
) error {
	by := tracker.ByRun(runID)
	idle := tracker.IdleRuns(task.Comments, roleName) + 1

	event, text := tracker.EventAgentUnavailable, fmt.Sprintf(
		"Прогон run:%s не начинался: %s\n\nДо работы дело не дошло — ни коммита, ни правки, "+
			"ни шагов сверх пары первых. Задача возвращается в %s. Счётчик попыток не тронут: "+
			"агенту это не в упрёк, смотреть надо на сеть и на доступ к API.%s",
		short(runID), term.Detail, flow.ReadsFrom, spent(usage))
	if term.Kind == runner.TerminationTruncated {
		event, text = tracker.EventRunTruncated, fmt.Sprintf(
			"Прогон run:%s срезан на ходу: %s\n\nЭто не провал: агент работал и не успел отчитаться. "+
				"Задача возвращается в %s, счётчик попыток не тронут, рабочая папка сохранена — "+
				"следующий прогон продолжит с того же места, а не начнёт заново.%s",
			short(runID), term.Detail, flow.ReadsFrom, spent(usage))
	}

	if err := o.record(task.Key, by, tracker.Marker{
		RunID: runID, Role: roleName, Event: event, ConfigSHA: o.ConfigSHA,
	}, text); err != nil {
		return err
	}

	to, human := flow.ReadsFrom, false
	if idle >= o.Workflow.Limits.MaxIdleRuns {
		to, human = flow.Blocked(), true
		o.logf("%s: прогоны роли %s не доходят до результата подряд %d раз, задача уходит к человеку",
			task.Key, roleName, idle)
		if err := o.record(task.Key, by, tracker.Marker{
			RunID: runID, Role: roleName, Event: tracker.EventIdleRunsExhausted, ConfigSHA: o.ConfigSHA,
		}, fmt.Sprintf("Прогоны роли %s не доходят до результата %d раз подряд — это предел "+
			"(limits.max_idle_runs). Считаются вместе оба вида: и «не начинал», и «не успел», — "+
			"потому что следствие у них одно, а чередование обошло бы два раздельных счётчика. "+
			"Смотреть надо не на задачу: либо не пускает сеть и API, либо задача не влезает "+
			"в предел шагов роли и её пора разрезать. Работа всех этих прогонов цела.",
			roleName, idle)); err != nil {
			return err
		}
	}

	if err := o.move(task, by, to); err != nil {
		return err
	}
	if human {
		if err := o.Tracker.SetHumanFlag(task.Key, by, true); err != nil {
			return err
		}
	}
	return o.Tracker.Release(task.Key, by)
}

// renew продлевает аренду перед финализацией и отвечает, потеряна ли она.
//
// Потерянная аренда — не ошибка: прогон просто опоздал, задачу уже могли отдать
// другому. Всё, что он вправе сделать, — предупредить, и это делается системной
// записью: аренды у него больше нет, а значит нет и права писать от её имени.
func (o *Office) renew(task tracker.Task, runID, roleName string, result runner.Result, usage runner.Usage, published string) (bool, error) {
	lease := o.now().Add(o.Workflow.LeaseMargin())
	err := o.Tracker.Renew(task.Key, runID, lease)
	switch {
	case err == nil:
		return false, nil
	case !errors.Is(err, tracker.ErrNotOwner):
		return false, err
	}

	o.logf("%s: аренда потеряна, в трекер идёт только предупреждение", task.Key)
	return true, o.notice(task.Key, tracker.Marker{
		RunID: runID, Role: roleName, Event: tracker.EventLeaseLost, ConfigSHA: o.ConfigSHA,
	}, fmt.Sprintf("Прогон run:%s завершился после потери аренды с исходом %s. %s Задачу он не двигает: "+
		"её мог взять другой прогон. Итог прогона: %s%s",
		short(runID), result.Outcome, published, result.Summary, spent(usage)))
}

// spent — цена прогона отдельным абзацем, для записей, где отчёта агента нет
// вовсе: неудачной публикации, потерянной аренды и прогона, не дошедшего
// до результата. Во всех трёх случаях прогон состоялся и был оплачен, и это
// единственное место, где человек увидит цену, не заглядывая в реестр.
func spent(usage runner.Usage) string {
	if line := tracker.SpendLine(usage); line != "" {
		return "\n\n" + line
	}
	return ""
}

// replay — переигранный исход прогона: ограждение роли не пропустило сделанное.
//
// До сих пор раннер исход агента не додумывал: синтетический failed появлялся
// только там, где результата не было вовсе. Переигрыш — другое: результат есть
// и он валиден, но работа нарушает правило, которое роль объявила себе сама.
// Отчёт агента при этом остаётся в переписке как есть — это его слова, — а маршрут
// выбирается по переигранному исходу, и рядом появляется системная запись
// с причиной.
//
// Пустая причина означает, что переигрывать нечего.
type replay struct {
	Guard   string
	Outcome runner.Outcome
	Reason  string
}

// replayOf проверяет ограждения роли и решает, переигрывать ли исход.
//
// Уже провалившийся прогон не переигрывается: маршрут у него тот же самый,
// а вторая запись о том, что он провалился, ничего человеку не сообщает.
func (o *Office) replayOf(role runner.Role, workdir string, passport runner.Run, result runner.Result) replay {
	if len(role.Guards) == 0 || result.Outcome == runner.OutcomeFailed {
		return replay{}
	}
	env := guard.EnvFrom(runner.RunEnv(role, workdir, passport))
	// Исход раннер называет сам: у него результат уже разобран, и по нему же
	// поедет задача. Ограждение в хуке читает файл — другого источника у него
	// нет, — но там и прогон ещё жив.
	env.Outcome = result.Outcome

	name, err := guard.CheckAll(role.Guards, env)
	if err == nil {
		return replay{}
	}
	return replay{Guard: name, Outcome: runner.OutcomeFailed, Reason: err.Error()}
}

// finish пишет отчёт и двигает задачу по графу.
//
// Порядок именно такой: комментарий раньше перехода. Упади раннер между ними —
// задача останется в прежнем статусе с объяснением, а не уедет в новый молча.
func (o *Office) finish(task tracker.Task, runID, roleName string, flow tracker.RoleFlow, result runner.Result, branch string, usage runner.Usage, rp replay) (string, error) {
	// Исход, по которому едет задача: агентский, а при переигрыше — тот, который
	// назначило ограждение. Отчёт ниже пишется всё равно агентский.
	outcome := result.Outcome
	if rp.Reason != "" {
		outcome = rp.Outcome
	}
	transition, found := flow.Outcomes[string(outcome)]
	if !found {
		return "", fmt.Errorf("%s: для исхода %s нет перехода в графе", roleName, outcome)
	}

	// Роль графа, названная следующим владельцем, но не описанная в карте этого
	// исхода, по умолчанию не едет. Умолчание тут опасно: у ревьюера оно ведёт
	// в очередь PR-прохода, и работа, возвращённая аналитику, была бы принята
	// и уехала бы открывать pull request. Правило действует только у `done` и только при
	// непустой карте: провал с названным владельцем — это повтор, как и раньше,
	// а `human`, `none` и незнакомое имя едут по умолчанию, как ехали.
	stray := outcome == runner.OutcomeDone && len(transition.ByNextOwner) > 0 &&
		o.handsOver(roleName, result.NextOwner) && !transition.Routed(result.NextOwner)

	attempts := task.Attempts + transition.Attempts
	// Передача другой роли графа обнуляет счётчик: попытки считают провалы
	// текущей роли, а не возраст задачи. Правило действует и на возврате
	// (ревьюер → implementer, implementer → аналитик): следующему владельцу
	// достаётся полный запас, иначе на трёх ролях два провала одной роли
	// обезоруживали бы остальные. `human` и `none` — не роли графа и счётчик
	// не трогают: работу они никому не передают. Передача, которую граф не знает,
	// не состоялась вовсе — и счётчика не касается.
	if !stray && outcome == runner.OutcomeDone && o.handsOver(roleName, result.NextOwner) {
		attempts = 0
	}
	// Маршрут выбирает граф по тому, кому агент передал задачу. Агент называет
	// следующего владельца, но карту маршрутов пишет человек: значения, которого
	// в ней нет, хватает ровно на переход по умолчанию (DESIGN §2.1).
	to, human := transition.Route(result.NextOwner), transition.Human
	if transition.Attempts > 0 && attempts >= o.Workflow.Limits.MaxAttempts {
		// Попытки исчерпаны: дальше крутить бессмысленно, зовём человека.
		to, human = flow.Blocked(), true
		o.logf("%s: попытки исчерпаны (%d), задача уходит к человеку", task.Key, attempts)
	}
	if stray {
		to, human = flow.Blocked(), true
		o.logf("%s: роль %s назвала владельцем %s, маршрута нет — задача уходит к человеку",
			task.Key, roleName, result.NextOwner)
	}

	by := tracker.ByRun(runID)
	marker := tracker.Marker{
		RunID: runID, Role: roleName, Outcome: string(result.Outcome),
		Next: result.NextOwner, ConfigSHA: o.ConfigSHA,
	}

	// Круги считаются по маркерам, а этот ещё не написан: к прошлым добавляется
	// нынешний. Считаем до записи отчёта, чтобы не считать самих себя дважды.
	// Считается пара (роль, владелец): передача вперёд идёт подряд десятками
	// и кругом не является.
	rounds := 0
	if marker.IsHandover() && transition.Returns(result.NextOwner) {
		rounds = tracker.ReturnRounds(task.Comments, roleName, result.NextOwner, o.Accounts) + 1
		if rounds >= o.Workflow.Limits.MaxReturnRounds {
			to, human = flow.Blocked(), true
			o.logf("%s: круги исчерпаны (%d), спор решает человек", task.Key, rounds)
		}
	}

	if err := o.Tracker.Comment(task.Key, by, tracker.ReportBody(marker, result, branch, usage)); err != nil {
		return "", err
	}
	// Переигрыш объясняется сразу за отчётом: между словами агента и маршрутом
	// задачи не должно оставаться места для догадок.
	if rp.Reason != "" {
		if err := o.record(task.Key, by, tracker.Marker{
			RunID: runID, Role: roleName, Event: tracker.EventOutcomeOverridden, ConfigSHA: o.ConfigSHA,
		}, fmt.Sprintf("Ограждение %s не пропустило прогон: %s.\n\nОтчёт run:%s выше остаётся как есть — "+
			"это слова агента. Исход %s раннер не принял и ведёт задачу как %s. Ветку раннер публикует "+
			"до проверок и при любом исходе, чтобы работа не пропала, — значит лишний коммит, если он был, "+
			"уже опубликован: откатывать его надо `git revert`, а не забыть.",
			rp.Guard, rp.Reason, short(runID), result.Outcome, rp.Outcome)); err != nil {
			return "", err
		}
	}
	// Объяснение — отдельной записью после отчёта: замечания роли остаются
	// в переписке, а человек видит, почему разговор роли с ролью на этом кончился.
	if rounds > 0 && rounds >= o.Workflow.Limits.MaxReturnRounds {
		if err := o.record(task.Key, by, tracker.Marker{
			RunID: runID, Role: roleName, Event: tracker.EventReturnRoundsExhausted, ConfigSHA: o.ConfigSHA,
		}, fmt.Sprintf("Роль %s отдала задачу роли %s %d раза подряд и не сдвинула её вперёд — это предел "+
			"(limits.max_return_rounds). Дальше крутить круги бессмысленно: спор решает человек. "+
			"Замечания последнего разбора — в отчёте run:%s выше.",
			roleName, result.NextOwner, rounds, short(runID))); err != nil {
			return "", err
		}
	}
	if stray {
		if err := o.record(task.Key, by, tracker.Marker{
			RunID: runID, Role: roleName, Event: tracker.EventRouteUnknown, ConfigSHA: o.ConfigSHA,
		}, fmt.Sprintf("Роль %s назвала следующим владельцем роль %s, но маршрута для неё "+
			"у исхода done в графе нет — решите, куда задаче дальше. Везти её по умолчанию "+
			"раннер не стал: у этого исхода умолчание ведёт в %s. Работа цела, отчёт run:%s выше.",
			roleName, result.NextOwner, transition.To, short(runID))); err != nil {
			return "", err
		}
	}
	// Бухгалтерия — последней: то, что решает человек, должно стоять выше неё.
	if err := o.warnRunCost(task, runID, roleName, usage); err != nil {
		return "", err
	}
	if attempts != task.Attempts {
		if err := o.Tracker.SetAttempts(task.Key, by, attempts); err != nil {
			return "", err
		}
	}
	if err := o.move(task, by, to); err != nil {
		return "", err
	}
	if human {
		if err := o.Tracker.SetHumanFlag(task.Key, by, true); err != nil {
			return "", err
		}
	}
	o.logf("%s: %s → %s", task.Key, outcome, to)
	return to, o.Tracker.Release(task.Key, by)
}

// handsOver — передал ли отчёт задачу другой роли графа.
//
// Имя роли проверяется по графу, а не по формату: `next_owner` — свободная
// строка, и «analyst» от выбывшей роли или опечатка в имени задачей никого
// не наделяют. Себе самой роль передать не может: это продолжение работы,
// а не смена владельца.
func (o *Office) handsOver(role, nextOwner string) bool {
	_, known := o.Workflow.Roles[nextOwner]
	return known && nextOwner != role
}

// HumanReplies возвращает в работу задачи, которым ответил человек.
// Отвечает, сколько задач разблокировано.
//
// Проход безролевой и идёт один на цикл. Задачу в ожидание отправляет не только
// агент своим вопросом: раннер делает это сам, исчерпав попытки или устав
// возвращать зависшую задачу. Роли у такой блокировки нет, а обход по ролям
// вдобавок означал бы, что задачу, оставшуюся от выбывшей роли, не разберёт
// никто и никогда.
func (o *Office) HumanReplies(ctx context.Context) (int, error) {
	accounts := o.Accounts

	count := 0
	for _, project := range o.projects() {
		for _, column := range o.Workflow.HumanStatuses() {
			// Задачи, ждущие человека, лежат без аренды — их и отдаёт ListReady.
			refs, err := o.Tracker.ListReady(project, column)
			if o.skipProject(project, err) {
				break
			}
			if err != nil {
				return count, err
			}
			for _, ref := range refs {
				task, err := o.Tracker.Get(ref.Key)
				if err != nil {
					return count, err
				}
				if !task.HumanFlag {
					continue
				}
				reply, roleName, found := tracker.HumanReply(task.Comments, accounts)
				if !found {
					continue
				}

				if err := o.unblock(task, roleName, reply); err != nil {
					if errors.Is(err, tracker.ErrNotOwner) {
						// Задачу перехватили между чтением и записью — не наша забота.
						o.logf("%s: разблокировать не вышло (%v), пропускаю", task.Key, err)
						continue
					}
					return count, err
				}
				count++
			}
		}
	}
	return count, nil
}

// unblock возвращает разблокированную задачу в очередь роли, говорившей последней.
//
// Роли в графе может уже не быть — её убрали или переименовали, а задача с её
// вопросом осталась. Тогда работает запасной маршрут: оставить задачу в ожидании
// было бы хуже, чем вернуть её не в ту очередь.
func (o *Office) unblock(task tracker.Task, roleName string, reply tracker.Comment) error {
	runID, err := runner.NewRunID()
	if err != nil {
		return err
	}

	to := o.Workflow.HumanReply.Fallback
	if flow, err := o.Workflow.Role(roleName); err == nil {
		to = flow.ReadsFrom
	} else {
		o.logf("%s: роль %q в графе не описана, возвращаю задачу запасным маршрутом в %s", task.Key, roleName, to)
	}

	marker := tracker.Marker{
		RunID: runID, Role: roleName, Event: tracker.EventHumanReply, ConfigSHA: o.ConfigSHA,
	}
	text := fmt.Sprintf("Ответ получен (%s), возвращаю задачу в %s. Он попадёт в контекст следующего прогона роли %s.",
		reply.Author, to, roleName)
	if err := o.notice(task.Key, marker, text); err != nil {
		return err
	}

	by := tracker.BySystem()
	if o.Workflow.HumanReply.ResetAttempts && task.Attempts != 0 {
		// Человек снял причину: прежние попытки к новой постановке не относятся.
		if err := o.Tracker.SetAttempts(task.Key, by, 0); err != nil {
			return err
		}
	}
	if err := o.Tracker.SetHumanFlag(task.Key, by, false); err != nil {
		return err
	}
	o.logf("%s: ответ человека разобран, задача возвращается в %s", task.Key, to)
	return o.move(task, by, to)
}

// Reap возвращает в очередь задачи с истёкшей арендой: раннера могли убить
// посреди прогона, и без этого задача осталась бы «в работе» навсегда.
func (o *Office) Reap(ctx context.Context) error {
	now := o.now()

	for _, project := range o.projects() {
		refs, err := o.Tracker.ListExpired(project, now)
		if o.skipProject(project, err) {
			continue
		}
		if err != nil {
			return err
		}
		for _, ref := range refs {
			task, err := o.Tracker.Get(ref.Key)
			if err != nil {
				return err
			}
			if err := o.returnExpired(task); err != nil {
				if errors.Is(err, tracker.ErrNotOwner) {
					// Аренду успели продлить или перезахватить — задача не наша.
					o.logf("%s: аренда снова жива, пропускаю", task.Key)
					continue
				}
				return err
			}
		}
	}
	return nil
}

// returnExpired возвращает одну зависшую задачу.
func (o *Office) returnExpired(task tracker.Task) error {
	flow, err := o.Workflow.Role(task.Owner)
	if err != nil {
		// Владелец не из графа: вернуть задачу некуда, и гадать раннер не станет.
		o.logf("%s: владелец %q не описан в графе, оставляю как есть", task.Key, task.Owner)
		return nil
	}

	runID, err := runner.NewRunID()
	if err != nil {
		return err
	}

	// Счётчик попыток здесь не трогается вовсе: смерть раннера — не провал агента.
	// Перезагрузили машину, кончилось место, уронили процесс — агент об этом
	// не знает и исправить не может. Но и не считать нельзя: задача, на которой
	// прогон не доживает до отчёта каждый раз, крутилась бы вечно. Поэтому счёт
	// свой, по маркерам в тикете, и разговор с человеком тоже свой.
	deaths := tracker.LeaseExpiries(task.Comments, task.Owner) + 1
	limit := o.Workflow.Limits.MaxLeaseExpiries

	to, human := flow.ReadsFrom, false
	text := fmt.Sprintf("Аренда прогона run:%s истекла %s — он не отчитался и, судя по всему, не пережил запуск. "+
		"Возвращаю задачу в %s. Счётчик попыток не трогаю: смерть прогона агенту не в упрёк. "+
		"Рабочая папка сохранена, следующий прогон продолжит с того же места.",
		short(task.RunID), task.LeaseUntil.Format(time.RFC3339), flow.ReadsFrom)

	if deaths >= limit {
		// Иначе задача осталась бы в очереди навсегда: раз за разом браться
		// за неё будет некому и незачем.
		to, human = flow.Blocked(), true
		text = fmt.Sprintf("Прогон run:%s не дожил до отчёта, и это %d раз подряд из %d допустимых — "+
			"задача роняет раннер. Дело не в агенте: он ни разу не успел сказать, что у него не вышло. "+
			"Смотреть надо туда, где прогон обрывается, — машина, место на диске, таймауты. "+
			"Отдаю задачу человеку, рабочая папка сохранена.",
			short(task.RunID), deaths, limit)
	}

	marker := tracker.Marker{
		RunID: runID, Role: task.Owner, Event: tracker.EventLeaseExpired, ConfigSHA: o.ConfigSHA,
	}
	if err := o.notice(task.Key, marker, text); err != nil {
		return err
	}

	by := tracker.BySystem()
	if err := o.move(task, by, to); err != nil {
		return err
	}
	if human {
		if err := o.Tracker.SetHumanFlag(task.Key, by, true); err != nil {
			return err
		}
	}
	o.logf("%s: аренда истекла, возвращаю в %s (прогон не дожил до отчёта, %d раз подряд из %d)",
		task.Key, to, deaths, limit)
	if err := o.Tracker.Release(task.Key, by); err != nil {
		return err
	}
	o.sweep(task)
	return nil
}

// sweep сносит песочницу мёртвого прогона.
//
// Зовётся последним, после возврата задачи, и это порядок по существу.
// ListExpired — снимок: аренду могли продлить, пока reap до неё шёл. Тогда
// первая же запись упирается в правило владения, задача пропускается — и до
// уборки дело не доходит вовсе. Убирай мы раньше, эта же гонка означала бы
// снесённую песочницу живого прогона.
//
// Обратный риск безобиден: между возвратом задачи и уборкой её может взять
// следующий tick, но у него свой run_id, а значит и своя песочница.
//
// Отказ уборки не роняет reap: его дело — вернуть задачи, и терять их
// из-за песочницы нельзя. Беда уходит в лог, где её увидит человек.
func (o *Office) sweep(task tracker.Task) {
	if o.Sandboxes == nil || task.RunID == "" {
		return
	}
	removed, err := o.Sandboxes.Remove(task.RunID)
	switch {
	case err != nil:
		o.logf("%s: песочница прогона %s не убрана, уберите вручную: %v", task.Key, short(task.RunID), err)
	case removed:
		o.logf("%s: песочница прогона %s убрана", task.Key, short(task.RunID))
	default:
		o.logf("%s: песочницы прогона %s на этой машине нет", task.Key, short(task.RunID))
	}
}

// Loop гоняет цикл по расписанию, пока не остановят.
//
// Это не демон и не supervisor: он не следит за собой, не перезапускается
// и не держит состояния между циклами. Ошибка цикла — повод сказать о ней
// и пойти дальше, а не умереть: следующий заход может пройти.
func (o *Office) Loop(ctx context.Context, every time.Duration, roleName string) error {
	for {
		if err := o.Reap(ctx); err != nil {
			o.logf("reap: %v", err)
		}
		if err := o.tickOnce(ctx, roleName); err != nil {
			o.logf("tick: %v", err)
		}

		select {
		case <-ctx.Done():
			o.logf("остановка по сигналу")
			return nil
		case <-time.After(every):
		}
	}
}

func (o *Office) tickOnce(ctx context.Context, roleName string) error {
	if roleName == "" {
		return o.TickAll(ctx)
	}
	_, err := o.Tick(ctx, roleName)
	return err
}

// keepLease продлевает аренду, пока идёт прогон, и возвращает функцию остановки.
//
// Интервал — треть таймаута роли: два пропущенных продления подряд ещё не роняют
// аренду, а третье уже честно означает, что прогон не жив.
func (o *Office) keepLease(ctx context.Context, key, runID string, role runner.Role) func() {
	timeout := time.Duration(role.Limits.TimeoutSec) * time.Second
	every := timeout / 3
	if every <= 0 {
		return func() {}
	}

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				until := o.now().Add(timeout + o.Workflow.LeaseMargin())
				if err := o.Tracker.Renew(key, runID, until); err != nil {
					// Аренду отобрали — продлевать больше нечего. Прогон об этом
					// узнает при финализации, а мешать ему сейчас незачем.
					o.logf("%s: продление аренды не удалось: %v", key, err)
					return
				}
			}
		}
	}()

	return func() {
		close(stop)
		<-done
	}
}

// move переводит задачу в статус, если она ещё не там.
//
// Перевод «в тот же самый статус» не делается вовсе, и это общее правило,
// а не частный случай. У роли без рабочего статуса в её же статус ведут
// и возвраты по исходам, и reap; в JIRA переход «в себя» существует не всегда,
// и раннер спотыкался бы на ровном месте — а сказать ему было бы нечего:
// задача уже там, где надо.
func (o *Office) move(task tracker.Task, by tracker.Actor, to string) error {
	if task.Status == to {
		return nil
	}
	return o.Tracker.Transition(task.Key, by, to)
}

// notice пишет системную запись от лица раннера.
func (o *Office) notice(key string, marker tracker.Marker, text string) error {
	return o.record(key, tracker.BySystem(), marker, text)
}

// record пишет запись раннера от указанного актора.
//
// Актор здесь не деталь, а требование протокола: пока аренда жива, системная
// запись невозможна — CheckOwner требует от системного актора её отсутствия.
// Поэтому всё, что раннер пишет внутри прогона, подписывается прогоном, а
// системным остаётся то, что делается, когда аренды уже нет.
func (o *Office) record(key string, by tracker.Actor, marker tracker.Marker, text string) error {
	return o.Tracker.Comment(key, by, tracker.NoticeBody(marker, text))
}

// skipProject решает, пропустить ли проект, которого трекер не знает.
//
// Такой проект — ошибка конфигурации, а не работы: строка в projects.yaml
// осталась от прежней задумки или опечатана. Роняя из-за неё весь цикл, раннер
// останавливал работу и по всем остальным проектам — а обходит он их по порядку,
// так что достаточно одной неудачной буквы в начале алфавита. Поймано живой
// проверкой: заглушка OFFICE не давала reap дойти до настоящего проекта.
//
// Жаловаться раннер продолжает каждый цикл, и это намеренно: молчаливый пропуск
// означал бы, что проект просто не обслуживается, и заметить это было бы нечем.
func (o *Office) skipProject(project string, err error) bool {
	notice, skip := tracker.SkipUnknownProject(project, err)
	if skip {
		o.logf("%s", notice)
	}
	return skip
}

// projects — ключи проектов в устойчивом порядке: два прогона должны обходить
// очередь одинаково.
func (o *Office) projects() []string { return o.Projects.Keys() }

func (o *Office) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

func (o *Office) logf(format string, args ...any) {
	out := o.Log
	if out == nil {
		out = os.Stderr
	}
	fmt.Fprintf(out, format+"\n", args...)
}

// taskBody — постановка задачи для агента. Всё, что знает трекер, и ничего
// про аренду и попытки: это хозяйство раннера, а не работа.
func taskBody(task tracker.Task) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — %s\n", task.Key, task.Summary)
	if description := strings.TrimSpace(task.Description); description != "" {
		fmt.Fprintf(&b, "\n%s\n", description)
	}
	if len(task.Labels) > 0 {
		fmt.Fprintf(&b, "\nМетки: %s\n", strings.Join(task.Labels, ", "))
	}
	return b.String()
}

// contextBody — то, чего агент не может узнать сам: какая это попытка и что
// говорили в тикете после его последнего отчёта.
//
// Историю целиком агент не читает (DESIGN §2.4): в контекст идёт только хвост
// после последнего маркера своей роли — в нём и ответ человека, если он был.
func contextBody(task tracker.Task, roleName string, accounts []string, maxAttempts int) string {
	var b strings.Builder

	fmt.Fprintf(&b, "## Задача\n\n- Ключ: %s\n- Попытка: %d из %d\n", task.Key, task.Attempts+1, maxAttempts)

	// Ответы человека — раньше переписки: это то, ради чего задачу разбудили,
	// и искать их в хвосте построчно агенту не надо. Вопросы берутся из тикета
	// заново: между вопросом и ответом лежит другой тик, и помнить заданное
	// раннеру негде.
	if answers := tracker.HumanAnswers(task.Comments, roleName, accounts); len(answers) > 0 {
		b.WriteString("\n## Ответы человека\n\n")
		for _, answer := range answers {
			fmt.Fprintf(&b, "- %s «%s» → %s\n", answer.Question.ID, answer.Question.Text, answerText(answer))
		}
	}

	tail := tracker.TailAfterRole(task.Comments, roleName)
	if len(tail) == 0 {
		return b.String()
	}

	b.WriteString("\n## Переписка в тикете\n")
	for _, comment := range tail {
		who := comment.Author
		if !slices.Contains(accounts, comment.Author) {
			who += " (человек)"
		}
		fmt.Fprintf(&b, "\n### %s, %s\n\n%s\n", who, comment.Created.Format(time.RFC3339), strings.TrimSpace(comment.Body))
	}
	return b.String()
}

// answerText — как ответ человека выглядит в контексте роли.
//
// Ответ мимо вариантов отдаётся как есть, с пометкой: валидировать человека
// раннер не станет — отвергнуть его ответ значит его потерять, а решить, что
// с ним делать, роль может сама.
func answerText(a tracker.Answer) string {
	switch {
	case !a.Answered():
		return "ответа нет"
	case a.Label != "" && !strings.EqualFold(a.Text, a.Label):
		return a.Text + " — " + a.Label
	case a.Label != "":
		return a.Label
	case a.OffOptions():
		return a.Text + " (такого варианта не предлагалось — решай сам)"
	}
	return a.Text
}

// short — первые восемь символов идентификатора: столько же, сколько в маркере.
// По символам, а не по байтам: обрезка посреди многобайтового символа испортила
// бы строку.
func short(id string) string {
	runes := []rune(id)
	if len(runes) <= 8 {
		return id
	}
	return string(runes[:8])
}
