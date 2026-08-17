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

	"github.com/kao73/virtual-office/runner"
	"github.com/kao73/virtual-office/tracker"
	"github.com/kao73/virtual-office/workspace"
)

// Agent — один прогон агента. Интерфейс нужен ровно затем, чтобы конвейер
// проверялся целиком без трат на модель: в тестах его закрывает подделка,
// возвращающая заданный результат.
type Agent interface {
	Run(ctx context.Context, req Request) (runner.Result, error)
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

	ConfigRoot string // корень конфиг-репозитория: оттуда роли
	ConfigSHA  string // отпечаток конфигурации для маркеров

	// Accounts — все учётки офиса: общая, учётки ролей и чужая автоматизация.
	// Всё, написанное не ими, считается словами человека. Список собирается
	// один раз при сборке офиса, а не спрашивается у трекера на каждом тике.
	Accounts []string

	Now func() time.Time
	Log io.Writer
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
	return o.as(roleName).tickRole(ctx, roleName)
}

// as — тот же офис, ходящий в трекер под учёткой роли. Копия мелкая и живёт
// один тик: меняется только трекер, всё остальное — общее хозяйство.
func (o *Office) as(roleName string) *Office {
	role, found := o.Trackers[roleName]
	if !found {
		return o
	}
	clone := *o
	clone.Tracker = role
	return &clone
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

	task, err := o.claim(roleName, flow, role)
	if err != nil || task.ref.Key == "" {
		return false, err
	}
	return true, o.work(ctx, task, roleName, flow, role)
}

// CheckWorkflow предупреждает о workflow, в котором захват задачи не может стать
// CAS: рабочий статус, доступный переходом из него самого, позволяет двум прогонам
// захватить одну задачу и уйти работать в один worktree.
//
// Проверка ничего не блокирует — с одним раннером на проект такой workflow
// безопасен, и это ровно тот случай, в котором офис работает сейчас. Сколько
// раннеров запущено на самом деле, отсюда не видно: сказать об этом человеку
// раннер может, решить за него — нет.
//
// Трекер, не умеющий отвечать про свой workflow, пропускается молча: у файлового
// workflow нет вовсе, и жаловаться было бы не на что.
func (o *Office) CheckWorkflow() {
	checker, able := o.Tracker.(tracker.WorkflowChecker)
	if !able {
		return
	}

	for _, project := range o.projects() {
		for _, status := range o.workingStatuses() {
			check, err := checker.CheckWorkflow(project, status)
			if o.skipProject(project, err) {
				break
			}
			switch {
			case err != nil:
				o.logf("%s: проверка workflow не выполнена: %v", project, err)
			case check.Sample == "":
				// Переходы трекер показывает только у конкретной задачи. Нет
				// задачи в работе — нет и ответа; молчание здесь читалось бы
				// как «проверил, всё в порядке».
				o.logf("%s: проверка workflow не выполнена: нет задачи в рабочем статусе %s", project, status)
			case check.SelfEntry:
				o.logf("workflow %s допускает двух владельцев (в %s можно войти из него самого); "+
					"безопасно только с одним раннером на проект, см. docs/contracts/tracker-protocol.md",
					project, status)
			}
		}
	}
}

// workingStatuses — рабочие колонки всех ролей графа, без повторов и в устойчивом
// порядке: две роли вправе работать в одной колонке, спрашивать о ней дважды незачем.
//
// Роли без рабочей колонки пропускаются: их захват статуса не меняет, и лазейка
// «вход в рабочий статус из него самого» к ним не относится вовсе.
func (o *Office) workingStatuses() []string {
	var statuses []string
	for _, role := range o.Workflow.Roles {
		if role.Working != "" && !slices.Contains(statuses, role.Working) {
			statuses = append(statuses, role.Working)
		}
	}
	slices.Sort(statuses)
	return statuses
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
func (o *Office) work(ctx context.Context, c claimed, roleName string, flow tracker.RoleFlow, role runner.Role) error {
	task, err := o.Tracker.Get(c.ref.Key)
	if err != nil {
		return err
	}
	ws, runID := c.ws, c.runID

	// Барьер взят вместе с задачей и держится до конца прогона: пуш и отчёт —
	// тоже работа над ней.
	defer o.unlock(task.Key, ws)

	passport := runner.Run{
		RunID: runID, Role: roleName, ConfigSHA: o.ConfigSHA,
		StartedAt: o.now(), TaskKey: task.Key,
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
	result, runErr := o.Agent.Run(ctx, Request{
		Role: role, Workdir: ws.Dir, Passport: passport, Mounts: ws.Mounts(),
	})
	stop()

	if runErr != nil {
		// Прогон не состоялся: аренда остаётся, задачу вернёт reaper.
		return fmt.Errorf("прогон %s не состоялся: %w", runID, runErr)
	}
	o.logf("%s: исход %s — %s", task.Key, result.Outcome, result.Summary)

	// Работа не должна жить только в worktree, который однажды удалят, поэтому
	// пуш идёт при любом исходе. Его неудача не мешает вернуть задачу в граф,
	// но остаётся ошибкой цикла: работа не опубликована, и человек должен узнать.
	branch := ws.Branch
	pushed, pushErr := o.Workspaces.Push(ws)
	switch {
	case pushErr != nil:
		o.logf("%s: ветка не опубликована: %v", task.Key, pushErr)
		branch = ""
	case pushed:
		o.logf("%s: ветка %s опубликована", task.Key, branch)
	default:
		branch = ""
	}

	if err := o.finish(task, runID, roleName, flow, result, branch); err != nil {
		return err
	}
	return pushErr
}

// finish пишет отчёт и двигает задачу по графу.
//
// Порядок именно такой: комментарий раньше перехода. Упади раннер между ними —
// задача останется в прежней колонке с объяснением, а не уедет в новую молча.
func (o *Office) finish(task tracker.Task, runID, roleName string, flow tracker.RoleFlow, result runner.Result, branch string) error {
	// Перед финализацией — продление. Прогон, доживший до конца после reap,
	// не вправе трогать задачу: её уже могли отдать другому.
	lease := o.now().Add(o.Workflow.LeaseMargin())
	if err := o.Tracker.Renew(task.Key, runID, lease); err != nil {
		if !errors.Is(err, tracker.ErrNotOwner) {
			return err
		}
		o.logf("%s: аренда потеряна, в трекер идёт только предупреждение", task.Key)
		return o.notice(task.Key, tracker.Marker{
			RunID: runID, Role: roleName, Event: tracker.EventLeaseLost, ConfigSHA: o.ConfigSHA,
		}, fmt.Sprintf("Прогон run:%s завершился после потери аренды с исходом %s. Работа сохранена в ветке, "+
			"но задачу он не двигает: её мог взять другой прогон. Итог прогона: %s",
			short(runID), result.Outcome, result.Summary))
	}

	transition, found := flow.Outcomes[string(result.Outcome)]
	if !found {
		return fmt.Errorf("%s: для исхода %s нет перехода в графе", roleName, result.Outcome)
	}

	attempts := task.Attempts + transition.Attempts
	// Маршрут выбирает граф по тому, кому агент передал задачу. Агент называет
	// следующего владельца, но карту маршрутов пишет человек: значения, которого
	// в ней нет, хватает ровно на переход по умолчанию (DESIGN §2.1).
	to, human := transition.Route(result.NextOwner), transition.Human
	if transition.Attempts > 0 && attempts >= o.Workflow.Limits.MaxAttempts {
		// Попытки исчерпаны: дальше крутить бессмысленно, зовём человека.
		to, human = flow.Blocked(), true
		o.logf("%s: попытки исчерпаны (%d), задача уходит к человеку", task.Key, attempts)
	}

	by := tracker.ByRun(runID)
	marker := tracker.Marker{
		RunID: runID, Role: roleName, Outcome: string(result.Outcome),
		Next: result.NextOwner, ConfigSHA: o.ConfigSHA,
	}
	if err := o.Tracker.Comment(task.Key, by, tracker.ReportBody(marker, result, branch)); err != nil {
		return err
	}
	if attempts != task.Attempts {
		if err := o.Tracker.SetAttempts(task.Key, by, attempts); err != nil {
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
	o.logf("%s: %s → %s", task.Key, result.Outcome, to)
	return o.Tracker.Release(task.Key, by)
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
		for _, column := range o.Workflow.HumanColumns() {
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

// move переводит задачу в колонку, если она ещё не там.
//
// Перевод «в тот же самый статус» не делается вовсе, и это общее правило,
// а не частный случай. У роли без рабочей колонки в её же колонку ведут
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
	return o.Tracker.Comment(key, tracker.BySystem(), tracker.NoticeBody(marker, text))
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
	if !errors.Is(err, tracker.ErrNoProject) {
		return false
	}
	o.logf("%s: проект описан в %s, но трекер его не знает — пропускаю", project, tracker.ProjectsFile)
	return true
}

// projects — ключи проектов в устойчивом порядке: два прогона должны обходить
// очередь одинаково.
func (o *Office) projects() []string {
	return slices.Sorted(maps(o.Projects))
}

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

// maps — ключи карты последовательностью; slices.Sorted делает из них порядок.
func maps[K comparable, V any](m map[K]V) func(yield func(K) bool) {
	return func(yield func(K) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}
