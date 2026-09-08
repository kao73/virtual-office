package pipeline

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kao73/virtual-office/internal/forge"
	"github.com/kao73/virtual-office/internal/runner"
	"github.com/kao73/virtual-office/internal/tracker"
	"github.com/kao73/virtual-office/internal/workspace"
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

	merge, err := o.Workspaces.MergeCheck(repo, project.Branch(task.Key), project.PRBranch())
	if err != nil {
		// Слияемость не посчитана — это беда обвязки, а не ответ о задаче.
		// Двигать задачу нельзя: следующий проход попробует снова.
		o.logf("%s: слияние не проверено, задача остаётся на месте: %v", task.Key, err)
		return nil
	}
	if merge.Empty() {
		return o.prAnomaly(task, fmt.Sprintf(
			"Сливать нечего: в ветке %s нет коммитов, которых не было бы в %s. "+
				"Открывать pull request не из чего. Работа либо не дошла до ветки, либо уже в основной. "+
				"Это аномалия, а не провал прогона: попытка не потрачена.",
			project.Branch(task.Key), project.PRBranch()))
	}
	advanced, err := o.Workspaces.BaseAdvanced(repo, project.Branch(task.Key), project.PRBranch())
	if err != nil {
		o.logf("%s: продвижение базы не проверено, задача остаётся на месте: %v", task.Key, err)
		return nil
	}
	if merge.Conflict || advanced {
		return o.prConflict(task, project, "", merge.Conflict)
	}

	// Архивирование — свойство изменения, а не forge: без него он не собран
	// только для того, чтобы открыть pull request, а не для того, чтобы
	// зафиксировать переход Comet Native в docs/comet/archive/**. Раньше эта
	// ветка возвращалась через prSkipped выше архивирования — на обоих
	// текущих полигонах forge вообще не настроен, и Archive был недостижим
	// целиком.
	if project.Forge == "" {
		if ok, err := o.archiveIfReady(task, project); err != nil {
			return err
		} else if !ok {
			return nil
		}
		return o.prSkipped(task, project)
	}

	impl, known := o.Forges[project.Forge]
	if !known {
		o.logf("%s: forge %q не собран, задача остаётся на месте", task.Key, project.Forge)
		return nil
	}

	// prBody — раньше archiveIfReady, а не после: она читает brief.md из
	// docs/comet/changes/<name>/ на только что запушенном HEAD, а
	// archiveIfReady переименовывает этот каталог в docs/comet/archive/**
	// и пушит переименование. В обратном порядке независимое ревью нашло,
	// что prBody промахивается по обоим корням (новому — переименован,
	// legacy — его никогда не было для Comet-нативной задачи) и молча
	// подставляет сырой текст тикета вместо brief'а — ровно для тех задач,
	// что дошли до архивирования.
	title, body, err := o.prBody(task, repo, project)
	if err != nil {
		return err
	}

	if ok, err := o.archiveIfReady(task, project); err != nil {
		return err
	} else if !ok {
		// Рабочая папка занята прямо сейчас — pull request подождёт
		// следующего прохода, а не откроется без архивирования.
		return nil
	}

	url, err := impl.OpenPR(task.Project, project.Branch(task.Key), project.PRBranch(), title, body)
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
		return o.prMerged(task, project, url)
	case forge.Closed:
		return o.prAnomaly(task, fmt.Sprintf(
			"Pull request %s закрыт без слияния. Работу не взяли — дальше решать человеку: "+
				"ответьте здесь, и офис откроет pull request заново; если работа не годится, "+
				"переведите задачу в очередь разработчика руками.", url))
	}

	// PR открыт. База с тех пор могла уехать вперёд — текстовым конфликтом
	// или без него.
	merge, err := o.Workspaces.MergeCheck(repo, project.Branch(task.Key), project.PRBranch())
	if err != nil {
		o.logf("%s: слияние не проверено, задача остаётся на месте: %v", task.Key, err)
		return nil
	}
	advanced, err := o.Workspaces.BaseAdvanced(repo, project.Branch(task.Key), project.PRBranch())
	if err != nil {
		o.logf("%s: продвижение базы не проверено, задача остаётся на месте: %v", task.Key, err)
		return nil
	}
	if merge.Conflict || advanced {
		return o.prConflict(task, project, url, merge.Conflict)
	}
	if !project.AutoMerge.Enabled {
		return nil // как сегодня: гейт чист, ждём человека
	}
	return o.attemptMerge(task, project, url)
}

// prConflict возвращает задачу в работу: и текстовый конфликт, и продвинувшаяся
// без конфликта база — это работа, а не провал.
//
// Попытка не тратится и pull request не закрывается: задача вернётся сюда после
// разбора, и тот же PR подхватит её — в переписке последней остаётся запись
// об открытии.
func (o *Office) prConflict(task tracker.Task, project tracker.Project, url string, textConflict bool) error {
	runID, err := runner.NewRunID()
	if err != nil {
		return err
	}
	to := o.Workflow.PR.Conflict
	by := tracker.BySystem()

	kind := "конфликт"
	reason := fmt.Sprintf("Ветка %s не сливается с %s.", project.Branch(task.Key), project.PRBranch())
	if !textConflict {
		kind = "продвижение базы"
		reason = fmt.Sprintf("База %s продвинулась вперёд с тех пор, как ветка %s была создана — "+
			"конфликта нет, но контекст мог устареть.", project.PRBranch(), project.Branch(task.Key))
	}

	// Про открытый pull request говорится, только если он есть. Проблема бывает
	// найдена и до открытия — тогда обещать, что «PR подхватит задачу», значит
	// врать: подхватывать нечему. Поймано живой проверкой на GitHub.
	fate := "Pull request откроется, когда работа вернётся сюда."
	if url != "" {
		fate = fmt.Sprintf("Pull request %s остаётся открытым и подхватит задачу, когда она вернётся.", url)
	}
	if err := o.record(task.Key, by, tracker.Marker{
		RunID: runID, Role: o.Workflow.PR.Role, Event: tracker.EventMergeConflict, ConfigSHA: o.ConfigSHA,
	}, fmt.Sprintf("%s Задача возвращается в %s: слить базу и разрешить, если есть что, — "+
		"это работа, а не провал, и счётчик попыток не тронут. База уже принесена "+
		"в клон, сеть для слияния не нужна. %s",
		reason, to, fate)); err != nil {
		return err
	}
	o.logf("%s: %s, задача возвращается в %s", task.Key, kind, to)
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

// prMerged закрывает жизнь задачи: работа слита в ветку, куда метил PR-проход.
func (o *Office) prMerged(task tracker.Task, project tracker.Project, url string) error {
	runID, err := runner.NewRunID()
	if err != nil {
		return err
	}
	to := o.Workflow.PR.Merged
	by := tracker.BySystem()
	if err := o.record(task.Key, by, tracker.Marker{
		RunID: runID, Role: o.Workflow.PR.Role, Event: tracker.EventMerged, ConfigSHA: o.ConfigSHA,
	}, fmt.Sprintf("Pull request %s слит в %s. Задача уходит в %s, рабочая папка больше не нужна "+
		"и будет убрана.", url, project.PRBranch(), to)); err != nil {
		return err
	}
	o.logf("%s: pull request слит, задача уходит в %s", task.Key, to)
	return o.move(task, by, to)
}

// attemptMerge сливает pull request сам, когда гейт чист и проект явно
// доверил офису слияние (auto_merge.enabled). Никакого окна ожидания сверх
// гейта нет: как только он пройден, попытка идёт на этом же тике.
func (o *Office) attemptMerge(task tracker.Task, project tracker.Project, url string) error {
	impl, known := o.Forges[project.Forge]
	if !known {
		o.logf("%s: forge %q не собран, задача остаётся на месте", task.Key, project.Forge)
		return nil
	}
	switch err := impl.Merge(url); {
	case err == nil:
		return o.prMerged(task, project, url)
	case errors.Is(err, forge.ErrRefused):
		return o.mergeRefused(task, project, url, err)
	default:
		// Сбой связи. Локально всё ещё чисто — следующий тик попробует снова.
		o.logf("%s: слияние не выполнено, задача остаётся на месте: %v", task.Key, err)
		return nil
	}
}

// mergeRefused разбирается с окончательным отказом forge мержить pull
// request, который локально выглядит чистым (гейт прошёл MergeCheck и
// BaseAdvanced).
//
// Это не работа implementer'а: локально мержить нечего, он честно отчитается
// done, и цикл повторится вслепую. Счётчик — по образцу max_push_failures:
// считается по маркерам в переписке, не полем трекера.
func (o *Office) mergeRefused(task tracker.Task, project tracker.Project, url string, mergeErr error) error {
	runID, err := runner.NewRunID()
	if err != nil {
		return err
	}
	by := tracker.BySystem()
	refusals := tracker.MergeRefusals(task.Comments, o.Workflow.PR.Role) + 1

	if err := o.record(task.Key, by, tracker.Marker{
		RunID: runID, Role: o.Workflow.PR.Role, Event: tracker.EventMergeRefused, ConfigSHA: o.ConfigSHA,
	}, fmt.Sprintf("Forge отказал в слиянии pull request %s, хотя локально гейт чист (нет конфликта, "+
		"база %s не продвинулась): %v. Разбираться с этим — не работа implementer'а: смотреть надо "+
		"на правило forge, о котором офис не знает (например, branch protection).",
		url, project.PRBranch(), mergeErr)); err != nil {
		return err
	}

	if refusals >= o.Workflow.Limits.MaxMergeRefusals {
		o.logf("%s: forge отказывает в мерже подряд %d раз, задача уходит к человеку", task.Key, refusals)
		// Не prAnomaly: PR не закрыт, он по-прежнему открыт и просто не мержится.
		// prAnomaly пишет event:pr-closed — а это ложь семейству pr-opened/pr-closed
		// (prEvents, marker.go), по которому advancePR решает, звать openPR или
		// followPR. Соврав, что PR закрыт, следующий возврат из Blocked повёл бы
		// через openPR — попытку открыть второй PR на уже открытую ветку, отказ
		// GitHub-а (422/ErrRefused) и новый уход в Blocked без единой попытки
		// слияния. Эскалация здесь — по образцу pushFailed: отдельная запись-предел,
		// без вмешательства в семейство состояния PR.
		if err := o.record(task.Key, by, tracker.Marker{
			RunID: runID, Role: o.Workflow.PR.Role, Event: tracker.EventMergeRefusalsExhausted, ConfigSHA: o.ConfigSHA,
		}, fmt.Sprintf("Forge отказывает в слиянии %d раз подряд (limits.max_merge_refusals) при локально "+
			"чистом состоянии. Pull request %s остаётся открытым — разбираться с правилом forge (например, "+
			"branch protection) нужно человеку; после исправления ответьте здесь, и офис попробует слияние снова.",
			refusals, url)); err != nil {
			return err
		}
		if err := o.move(task, by, o.Workflow.PR.Closed); err != nil {
			return err
		}
		return o.Tracker.SetHumanFlag(task.Key, by, true)
	}
	o.logf("%s: forge отказал в слиянии, задача остаётся в очереди прохода: %v", task.Key, mergeErr)
	return nil
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

	// .comet/current-change.json на ветке называет изменение точно, если
	// аналитик его завёл — угаданное по task-key имя может разойтись с тем,
	// что аналитик реально выбрал (живой случай: задача demo-3, изменение
	// stats-median). Тот же приём, что и archiveIfReady в archive.go, но
	// через Show — у прохода без рабочей папки файла на диске нет.
	changeDir := runner.CometChangeDirRel(task.Key)
	if data, found, err := o.Workspaces.Show(repo, project.Branch(task.Key), runner.CometCurrentChangeFile); err != nil {
		return "", "", err
	} else if found {
		if name := runner.ParseCurrentChangeName([]byte(data)); name != "" {
			changeDir = runner.CometChangeDirForName(name)
		}
	}

	brief, found, err := o.Workspaces.Show(repo, project.Branch(task.Key),
		filepath.Join(changeDir, runner.FileBrief))
	if err != nil {
		return "", "", err
	}
	if !found {
		// Новый корень Comet Native пуст — задача либо старше этого перехода
		// (analyst вёл её через прежний docs/changes/<KEY>), либо пришла мимо
		// аналитика вовсе, либо уже архивирована (следующая проверка). Второй,
		// старый корень остаётся источником, пока первый не подтвердил свою
		// пустоту, а не наоборот.
		brief, found, err = o.Workspaces.Show(repo, project.Branch(task.Key),
			filepath.Join(runner.ChangeDirRel(task.Key), runner.FileBrief))
		if err != nil {
			return "", "", err
		}
	}
	if !found {
		// Изменение уже архивировано — не этим проходом (тот читает brief
		// раньше своего archiveIfReady, см. её вызывающего), а каким-то из
		// прошлых: сеть при OpenPR, ещё не собранный forge или человеческий
		// ErrRefused вернули задачу в очередь уже после того, как archiveIfReady
		// переименовала docs/comet/changes/<name> в docs/comet/archive/<дата>-
		// <name>. Без этой проверки повторный проход промахивался бы мимо
		// обоих корней выше и откатывался на сырой текст тикета — независимое
		// ревью нашло эту дыру уже после первого фикса той же дыры для
		// однопроходного случая. Дата в имени каталога заранее не предсказуема
		// (см. cometArchiveDestGlob в archive.go) — ищем по суффиксу имени.
		name := filepath.Base(changeDir)
		entries, lerr := o.Workspaces.ListDir(repo, project.Branch(task.Key), filepath.Join(cometArchiveScope, "archive"))
		if lerr != nil {
			return "", "", lerr
		}
		// Полный якорь по дате, не HasSuffix("-"+name): независимое ревью
		// (раунд 2) нашло, что простой суффикс совпал бы и с чужим архивом,
		// чьё имя случайно оканчивается тем же хвостом (например, name
		// "median" и чужой каталог "2026-08-31-stats-median" — оба
		// оканчиваются на "-median"). Раунд 3 поправил: cometArchiveDestGlob
		// в archive.go той же слабостью тоже страдал (её "*-"+name — тот же
		// класс коллизии на уровне filepath.Glob) — исправлено там же тем
		// же приёмом (цифровая маска даты), а не «применяется к заведомо
		// своему каталогу», как ошибочно утверждала предыдущая версия этого
		// комментария.
		archiveDirPattern := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}-` + regexp.QuoteMeta(name) + `$`)
		for _, entry := range entries {
			if !archiveDirPattern.MatchString(entry) {
				continue
			}
			brief, found, err = o.Workspaces.Show(repo, project.Branch(task.Key),
				filepath.Join(cometArchiveScope, "archive", entry, runner.FileBrief))
			if err != nil {
				return "", "", err
			}
			break
		}
	}
	if !found {
		// Ни в одном из корней постановки нет — задача пришла мимо аналитика.
		// Тогда телом идёт сам тикет целиком, вместе с темой: в заголовке она
		// есть, но тело pull request читают и отдельно от него.
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
