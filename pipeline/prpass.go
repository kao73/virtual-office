package pipeline

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kao73/virtual-office/forge"
	"github.com/kao73/virtual-office/runner"
	"github.com/kao73/virtual-office/tracker"
	"github.com/kao73/virtual-office/workspace"
)

// PRPass — системный проход: открыть pull request разобранным задачам,
// проследить за открытыми и убрать рабочие папки законченных.
//
// Проход безролевой, как и разбор ответов человека: агента у него нет, работу
// делает раннер. Маршрут при этом описан графом (`workflow.yaml: pr`), а не
// зашит здесь: код спрашивает статусы у графа и имени `office` не знает.
//
// Идёт он после разбора ответов человека и до ролей. Порядок не случаен: ответ
// на закрытый PR возвращает задачу в очередь прохода, и ждать следующего цикла
// ей незачем.
func (o *Office) PRPass(ctx context.Context) error {
	if !o.Workflow.PR.Set() {
		return o.sweepWorktrees()
	}
	pr := o.Workflow.PR

	for _, name := range o.projects() {
		refs, err := o.Tracker.ListReady(name, pr.From)
		if o.skipProject(name, err) {
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
			if err := o.advancePR(task); err != nil {
				return err
			}
		}
	}
	return o.sweepWorktrees()
}

// advancePR двигает одну задачу, дошедшую до очереди прохода.
//
// Состояние PR выводится из переписки — по последней записи семейства, а не
// по факту в истории. Разница видна на живом случае: PR закрыли руками, задача
// ушла к человеку, человек ответил и вернул её сюда. По факту в истории второй
// PR не открылся бы никогда: `pr-opened` там уже есть.
func (o *Office) advancePR(task tracker.Task) error {
	event, url, found := tracker.PRState(task.Comments, o.Workflow.PR.Role)
	if found && event == tracker.EventPROpened {
		if url == "" {
			// Запись об открытии есть, а адреса в ней нет: комментарий правили
			// руками или он пришёл из чужого офиса. Открывать второй pull request
			// на такой находке нельзя — это молча удвоило бы их.
			o.logf("%s: запись об открытии pull request без адреса, задача остаётся на месте", task.Key)
			return nil
		}
		return o.followPR(task, url)
	}
	return o.openPR(task)
}

// openPR открывает pull request задаче, дошедшей до очереди прохода впервые
// или вернувшейся после закрытого PR.
func (o *Office) openPR(task tracker.Task) error {
	project, repo, err := o.clone(task)
	if err != nil {
		return err
	}

	merge, err := o.Workspaces.MergeCheck(repo, project.Branch(task.Key), project.DefaultBranch)
	if err != nil {
		// Слияемость не посчитана — это беда обвязки, а не ответ о задаче.
		// Двигать задачу нельзя: следующий проход попробует снова.
		o.logf("%s: слияние не проверено, задача остаётся на месте: %v", task.Key, err)
		return nil
	}
	switch {
	case merge.Empty():
		return o.prAnomaly(task, fmt.Sprintf(
			"Сливать нечего: в ветке %s нет коммитов, которых не было бы в %s. "+
				"Открывать pull request не из чего. Работа либо не дошла до ветки, либо уже в основной. "+
				"Это аномалия, а не провал прогона: попытка не потрачена.",
			project.Branch(task.Key), project.DefaultBranch))
	case merge.Conflict:
		return o.prConflict(task, project, "")
	case project.Forge == "":
		return o.prSkipped(task, project)
	}

	impl, known := o.Forges[project.Forge]
	if !known {
		o.logf("%s: forge %q не собран, задача остаётся на месте", task.Key, project.Forge)
		return nil
	}

	title, body, err := o.prBody(task, repo, project)
	if err != nil {
		return err
	}
	url, err := impl.OpenPR(task.Project, project.Branch(task.Key), project.DefaultBranch, title, body)
	switch {
	case errors.Is(err, forge.ErrRefused):
		// Окончательный ответ forge о самой задаче — разговор с человеком.
		return o.prAnomaly(task, fmt.Sprintf("Forge отказался открыть pull request: %v", err))
	case err != nil:
		// Сбой связи. Задачу за него двигать нельзя.
		o.logf("%s: pull request не открыт, задача остаётся на месте: %v", task.Key, err)
		return nil
	}

	runID, err := runner.NewRunID()
	if err != nil {
		return err
	}
	if err := o.record(task.Key, tracker.BySystem(), tracker.Marker{
		RunID: runID, Role: o.Workflow.PR.Role, Event: tracker.EventPROpened, ConfigSHA: o.ConfigSHA,
	}, fmt.Sprintf("Pull request открыт: %s\n\nСливает человек — офис за него этого не делает. "+
		"Слияние он увидит сам и переведёт задачу в %s; закрытый без слияния PR вернётся разговором.",
		url, o.Workflow.PR.Merged)); err != nil {
		return err
	}
	o.logf("%s: pull request открыт: %s", task.Key, url)
	return nil
}

// followPR смотрит, что стало с уже открытым pull request.
func (o *Office) followPR(task tracker.Task, url string) error {
	project, repo, err := o.clone(task)
	if err != nil {
		return err
	}
	impl, known := o.Forges[project.Forge]
	if !known {
		o.logf("%s: forge %q не собран, задача остаётся на месте", task.Key, project.Forge)
		return nil
	}

	state, err := impl.PRState(url)
	if err != nil {
		o.logf("%s: состояние pull request не выяснено, задача остаётся на месте: %v", task.Key, err)
		return nil
	}

	switch state {
	case forge.Merged:
		return o.prMerged(task, url)
	case forge.Closed:
		return o.prAnomaly(task, fmt.Sprintf(
			"Pull request %s закрыт без слияния. Работу не взяли — дальше решать человеку: "+
				"ответьте здесь, и офис откроет pull request заново; если работа не годится, "+
				"переведите задачу в очередь разработчика руками.", url))
	}

	// PR открыт. Ветка по умолчанию с тех пор могла уехать вперёд.
	merge, err := o.Workspaces.MergeCheck(repo, project.Branch(task.Key), project.DefaultBranch)
	if err != nil {
		o.logf("%s: слияние не проверено, задача остаётся на месте: %v", task.Key, err)
		return nil
	}
	if merge.Conflict {
		return o.prConflict(task, project, url)
	}
	return nil
}

// prConflict возвращает задачу в работу: конфликт — это работа, а не провал.
//
// Попытка не тратится и pull request не закрывается: задача вернётся сюда после
// разбора, и тот же PR подхватит её — в переписке последней остаётся запись
// об открытии.
func (o *Office) prConflict(task tracker.Task, project tracker.Project, url string) error {
	runID, err := runner.NewRunID()
	if err != nil {
		return err
	}
	to := o.Workflow.PR.Conflict
	by := tracker.BySystem()

	// Про открытый pull request говорится, только если он есть. Конфликт бывает
	// найден и до открытия — тогда обещать, что «PR подхватит задачу», значит
	// врать: подхватывать нечему. Поймано живой проверкой на GitHub.
	fate := "Pull request откроется, когда работа вернётся сюда."
	if url != "" {
		fate = fmt.Sprintf("Pull request %s остаётся открытым и подхватит задачу, когда она вернётся.", url)
	}
	if err := o.record(task.Key, by, tracker.Marker{
		RunID: runID, Role: o.Workflow.PR.Role, Event: tracker.EventMergeConflict, ConfigSHA: o.ConfigSHA,
	}, fmt.Sprintf("Ветка %s не сливается с %s. Задача возвращается в %s: разрешить конфликт — "+
		"это работа, а не провал, и счётчик попыток не тронут. Ветка по умолчанию уже принесена "+
		"в клон, сеть для слияния не нужна. %s",
		project.Branch(task.Key), project.DefaultBranch, to, fate)); err != nil {
		return err
	}
	o.logf("%s: конфликт слияния, задача возвращается в %s", task.Key, to)
	return o.move(task, by, to)
}

// prSkipped уводит задачу проекта без forge туда же, куда ушла бы слитая.
//
// Событие своё, не `merged`: слияния не было, и запись об этом врала бы. Так
// живут оба полигона — у них локальный bare-репозиторий, открывать PR негде,
// — и маршрут при этом остаётся тем же, графовым.
func (o *Office) prSkipped(task tracker.Task, project tracker.Project) error {
	runID, err := runner.NewRunID()
	if err != nil {
		return err
	}
	to := o.Workflow.PR.Merged
	by := tracker.BySystem()
	if err := o.record(task.Key, by, tracker.Marker{
		RunID: runID, Role: o.Workflow.PR.Role, Event: tracker.EventPRSkipped, ConfigSHA: o.ConfigSHA,
	}, fmt.Sprintf("У проекта %s нет forge — открывать pull request негде. Работа опубликована "+
		"в ветке %s, задача уходит в %s. Слияния не было: ветку сливает человек, если сочтёт нужным.",
		task.Project, project.Branch(task.Key), to)); err != nil {
		return err
	}
	o.logf("%s: forge у проекта нет, задача уходит в %s", task.Key, to)
	return o.move(task, by, to)
}

// prMerged закрывает жизнь задачи: работа в ветке по умолчанию.
func (o *Office) prMerged(task tracker.Task, url string) error {
	runID, err := runner.NewRunID()
	if err != nil {
		return err
	}
	to := o.Workflow.PR.Merged
	by := tracker.BySystem()
	if err := o.record(task.Key, by, tracker.Marker{
		RunID: runID, Role: o.Workflow.PR.Role, Event: tracker.EventMerged, ConfigSHA: o.ConfigSHA,
	}, fmt.Sprintf("Pull request %s слит. Задача уходит в %s, рабочая папка больше не нужна "+
		"и будет убрана.", url, to)); err != nil {
		return err
	}
	o.logf("%s: pull request слит, задача уходит в %s", task.Key, to)
	return o.move(task, by, to)
}

// prAnomaly уводит задачу к человеку: с pull request что-то не так, и решать
// это офису нечем.
func (o *Office) prAnomaly(task tracker.Task, text string) error {
	runID, err := runner.NewRunID()
	if err != nil {
		return err
	}
	to := o.Workflow.PR.Closed
	by := tracker.BySystem()
	if err := o.record(task.Key, by, tracker.Marker{
		RunID: runID, Role: o.Workflow.PR.Role, Event: tracker.EventPRClosed, ConfigSHA: o.ConfigSHA,
	}, text); err != nil {
		return err
	}
	if err := o.move(task, by, to); err != nil {
		return err
	}
	o.logf("%s: pull request не состоялся, задача уходит к человеку в %s", task.Key, to)
	return o.Tracker.SetHumanFlag(task.Key, by, true)
}

// clone — проект задачи и его свежий bare-клон.
//
// Клон нужен проходу без рабочей папки: он читает из него файлы изменения
// и считает слияемость, а папки у задачи может не быть — её сносит уборка.
func (o *Office) clone(task tracker.Task) (tracker.Project, string, error) {
	project, err := o.Projects.Get(task.Project)
	if err != nil {
		return tracker.Project{}, "", err
	}
	repo, err := o.Workspaces.Repo(task.Project, project)
	if err != nil {
		return tracker.Project{}, "", err
	}
	return project, repo, nil
}

// prBody собирает заголовок и тело pull request.
//
// Постановка читается из bare-клона, а не из рабочей папки: папки может не быть.
// Отчёт разбора читается из переписки тикета — в git его нет вовсе.
//
// Задача, пришедшая мимо аналитика (`Backlog → Ready`), каталога изменения
// не имеет: тогда телом идут summary и description тикета.
func (o *Office) prBody(task tracker.Task, repo string, project tracker.Project) (string, string, error) {
	title := fmt.Sprintf("%s %s", task.Key, task.Summary)

	brief, found, err := o.Workspaces.Show(repo, project.Branch(task.Key),
		filepath.Join(runner.ChangeDirRel(task.Key), runner.FileBrief))
	if err != nil {
		return "", "", err
	}
	if !found {
		// Постановки в ветке нет — задача пришла мимо аналитика. Тогда телом идёт
		// сам тикет целиком, вместе с темой: в заголовке она есть, но тело pull
		// request читают и отдельно от него.
		brief = task.Summary + "\n\n" + task.Description
	}

	body := strings.TrimSpace(brief)
	if report, ok := lastReport(task.Comments); ok {
		// Маркер отчёта в тело не идёт: pull request читают люди, а маркер —
		// разметка для раннера.
		body += "\n\n## Разбор\n\n" + strings.TrimSpace(tracker.WithoutMarker(report))
	}
	body += fmt.Sprintf("\n\n---\nЗадача: %s. Pull request открыт офисом; сливает человек.", task.Key)
	return title, body, nil
}

// lastReport — последний отчёт о прогоне в переписке. Именно он и перевёл задачу
// в очередь прохода, кто бы его ни писал: имя роли здесь не спрашивается,
// потому что маршрут задаёт граф, а не код.
func lastReport(comments []tracker.Comment) (string, bool) {
	for i := len(comments) - 1; i >= 0; i-- {
		m, ok := tracker.MarkerOf(comments[i].Body)
		if ok && m.Outcome != "" {
			return comments[i].Body, true
		}
	}
	return "", false
}

// sweepWorktrees убирает рабочие папки законченных задач.
//
// Идёт от папок, а не от задач, и это не perf-соображение: список задач
// в терминальном статусе в живом проекте растёт вечно, а папок — единицы.
// Заодно так убираются и папки задач, которые человек закрыл руками.
//
// Просеиваются папки списком проектов **своего** трекера: в хозяйстве раннера
// лежат клоны обоих полигонов, и проход одного трекера не вправе трогать
// работу другого.
func (o *Office) sweepWorktrees() error {
	entries, err := o.Workspaces.List()
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if _, mine := o.Projects[entry.Project]; !mine {
			continue
		}
		if entry.Missing {
			o.logf("%s: запись о рабочей папке есть, каталога нет — убирать руками (runner worktree rm --force)", entry.Key)
			continue
		}

		task, err := o.Tracker.Get(entry.Key)
		if err != nil {
			// Папка есть, задачи нет: её могли удалить из трекера. Не наша забота
			// и не повод бросать уборку остальных.
			o.logf("%s: задача не прочитана, папка остаётся: %v", entry.Key, err)
			continue
		}
		if !o.Workflow.IsTerminal(task.Status) {
			continue
		}
		// Грязную папку не сносим. Раньше снос стоял сразу за успешным пушем
		// и был им защищён; у прохода такой защиты нет, а Remove — это
		// `worktree remove --force`, то есть потеря незакоммиченной работы.
		if entry.Dirty > 0 {
			o.logf("%s: задача закончена, но в папке %d незакоммиченных путей — оставляю, "+
				"убирать руками (runner worktree rm --force)", entry.Key, entry.Dirty)
			continue
		}

		ws, err := o.Workspaces.TryLock(entry.Workspace)
		if errors.Is(err, workspace.ErrWorktreeBusy) {
			continue // в папке работают прямо сейчас
		}
		if err != nil {
			o.logf("%s: замок рабочей папки не взят, папка остаётся: %v", entry.Key, err)
			continue
		}
		removeErr := o.Workspaces.Remove(ws)
		if err := ws.Unlock(); err != nil {
			o.logf("%s: замок рабочей папки не снят: %v", entry.Key, err)
		}
		if removeErr != nil {
			o.logf("%s: рабочая папка не убрана: %v", entry.Key, removeErr)
			continue
		}
		o.logf("%s: рабочая папка убрана: задача дошла до конца", entry.Key)
	}
	return nil
}
