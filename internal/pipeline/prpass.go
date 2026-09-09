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
	switch {
	case errors.Is(err, workspace.ErrBaseMissing):
		// Опечатка в auto_merge.target_branch (или ветку ещё не завели) —
		// не пройдёт сама, в отличие от прочих ошибок MergeCheck ниже.
		return o.mergeBlocked(task, categoryMissingBranch, err.Error())
	case err != nil:
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
	// Кто сливает — свойство проекта, а не офиса: обещать человеку его же
	// работу там, где офис сделает её сам, значит врать в тикете (auto_merge
	// у проекта включён явно, и человек, читающий запись, вправе знать, ждут
	// его или нет).
	fate := fmt.Sprintf("Сливает человек — офис за него этого не делает. "+
		"Слияние он увидит сам и переведёт задачу в %s; закрытый без слияния PR вернётся разговором.",
		o.Workflow.PR.Merged)
	if project.AutoMerge.Enabled {
		fate = fmt.Sprintf("Сливает офис сам (auto_merge), как только гейт чист: ни конфликта, "+
			"ни продвинувшейся базы. Ждать от человека нечего — после слияния задача уйдёт в %s; "+
			"закрытый без слияния PR вернётся разговором.", o.Workflow.PR.Merged)
	}
	if err := o.record(task.Key, tracker.BySystem(), tracker.Marker{
		RunID: runID, Role: o.Workflow.PR.Role, Event: tracker.EventPROpened, ConfigSHA: o.ConfigSHA,
	}, fmt.Sprintf("Pull request открыт: %s\n\n%s", url, fate)); err != nil {
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

	// Без SameRepo намеренно: url тот же непроверенный адрес из переписки,
	// что и перед Merge ниже, но здесь только читают. Тот же принятый риск,
	// что у неё (forge.SameRepo, internal/forge/forge.go) — распространяется
	// и на этот вызов, не только на сам Merge.
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
	switch {
	case errors.Is(err, workspace.ErrBaseMissing):
		return o.mergeBlocked(task, categoryMissingBranch, err.Error())
	case err != nil:
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
	return o.attemptMerge(task, project, impl, url)
}

// prConflict возвращает задачу в работу: и текстовый конфликт, и продвинувшаяся
// без конфликта база — это работа, а не провал.
//
// Попытка не тратится и pull request не закрывается: задача вернётся сюда после
// разбора, и тот же PR подхватит её — в переписке последней остаётся запись
// об открытии.
//
// Круг этот не бесконечен. Подряд limits.max_pr_returns возвратов — и задача
// уходит к человеку: если база не даёт ветке устояться (или forge не даёт слить
// — счёт общий, см. tracker.PRReturns), дело не в задаче, и гонять по ней роли
// дальше значит жечь прогоны впустую.
func (o *Office) prConflict(task tracker.Task, project tracker.Project, url string, textConflict bool) error {
	runID, err := runner.NewRunID()
	if err != nil {
		return err
	}
	to := o.Workflow.PR.Conflict
	by := tracker.BySystem()
	// Возвраты считаются до записи о нынешнем: он в этот счёт и войдёт.
	returns := tracker.PRReturns(task.Comments, o.Workflow.PR.Role) + 1
	exhausted := returns >= o.Workflow.Limits.MaxPRReturns

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
	// Возвращение в работу обещается, только если оно и вправду будет:
	// на пределе задача уходит к человеку, и «возвращается в Ready» соседней
	// строкой с «дальше разбираться человеку» было бы ложью — тем же счётом
	// к правде, что и fate выше.
	tail := fmt.Sprintf("Задача возвращается в %s: слить базу и разрешить, если есть что, — "+
		"это работа, а не провал, и счётчик попыток не тронут. База уже принесена "+
		"в клон, сеть для слияния не нужна. %s", to, fate)
	if exhausted {
		tail = fmt.Sprintf("В %s задача на этот раз не поедет: это %d-й возврат подряд, и это предел. "+
			"Попытка, как и прежде, не потрачена. %s", to, returns, fate)
	}
	if err := o.record(task.Key, by, tracker.Marker{
		RunID: runID, Role: o.Workflow.PR.Role, Event: tracker.EventMergeConflict, ConfigSHA: o.ConfigSHA,
	}, reason+" "+tail); err != nil {
		return err
	}

	if exhausted {
		o.logf("%s: PR-проход не сходится подряд %d раз (последнее — %s), задача уходит к человеку",
			task.Key, returns, kind)
		// Не prAnomaly и не event:pr-closed: pull request не закрыт (та же
		// причина и тот же приём, что у mergeRefused, — см. её). Маркер свой
		// и вне prEvents: семейству состояния PR он не лжёт.
		if err := o.record(task.Key, by, tracker.Marker{
			RunID: runID, Role: o.Workflow.PR.Role, Event: tracker.EventPRReturnsExhausted, ConfigSHA: o.ConfigSHA,
		}, fmt.Sprintf("PR-проход не сходится %d раз подряд (limits.max_pr_returns): либо база "+
			"не даёт ветке устояться, либо forge не даёт слить, — счёт у этих бед общий, потому "+
			"что чередованием одно от другого не отличить. Дальше разбираться человеку: уберите "+
			"причину и ответьте здесь, и офис попробует снова.", returns)); err != nil {
			return err
		}
		if err := o.move(task, by, o.Workflow.PR.Closed); err != nil {
			return err
		}
		return o.Tracker.SetHumanFlag(task.Key, by, true)
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
//
// impl приходит от followPR, а не резолвится здесь заново: тот уже сходил
// в o.Forges[project.Forge] и вернулся бы раньше, найдя его несобранным
// (тот же ключ, то же условие) — второй такой же lookup здесь был бы не
// дополнительной защитой, а мёртвым кодом с недостижимой веткой.
func (o *Office) attemptMerge(task tracker.Task, project tracker.Project, impl forge.Forge, url string) error {
	// Адрес взят из комментария тикета, а не получен от forge только что:
	// комментарий могли поправить руками или он пришёл из чужого офиса (см.
	// advancePR). Пока проход этим адресом только читал, находка стоила одного
	// лишнего запроса; слияние по нему — это правка чужого репозитория правами
	// токена офиса, и её надо не делать вовсе. Не отказ и не конфликт: счётчики
	// тут ни при чём, задача просто остаётся на месте.
	if !forge.SameRepo(project.RepoURL, url) {
		return o.mergeBlocked(task, categoryForeignRepo, fmt.Sprintf(
			"Адрес pull request %s называет не репозиторий проекта (%s) — слияния не будет. "+
				"Комментарий с event:pr-opened могли поправить руками или он пришёл из чужого "+
				"офиса: проверьте адрес и, если он неверный, поправьте комментарий.",
			url, project.RepoURL))
	}
	switch err := impl.Merge(url); {
	case err == nil:
		return o.prMerged(task, project, url)
	case errors.Is(err, forge.ErrRefused):
		return o.mergeRefused(task, project, url, err)
	case errors.Is(err, forge.ErrNotReady):
		return o.mergePending(task, project, url, err)
	default:
		// Сбой связи (сеть, 5xx, гонка 409 на самом PUT — см. forge.GitHub.Merge).
		// Локально всё ещё чисто — следующий тик попробует снова, без счёта:
		// это беда обвязки или мимолётная гонка, не про саму задачу.
		o.logf("%s: слияние не выполнено, задача остаётся на месте: %v", task.Key, err)
		return nil
	}
}

// Категории EventMergeUnavailable — стабильные ярлыки первой строки записи,
// без динамических деталей (адреса, имени ветки): tracker.EventCategories
// сравнивает их дословно по всей истории, а детали (какой адрес, какая
// ветка) остаются в тексте после ярлыка и в сравнение не идут — тем же
// приёмом, что categoryFooBar в internal/pipeline/splits.go.
const (
	categoryForeignRepo   = "Адрес pull request называет чужой репозиторий"
	categoryMissingBranch = "Базовая ветка auto_merge не существует в репозитории"
)

// mergeBlocked отмечает, что слияние сейчас невозможно по причине, которую
// самой задаче не решить (адрес PR называет чужой репозиторий, или
// auto_merge.target_branch называет несуществующую ветку). Задача остаётся
// на месте, счётчики (max_merge_refusals, max_pr_returns, max_merge_pending)
// не трогаются: это не работа implementer'а и не отказ forge, а структурная
// проблема конфигурации или переписки.
//
// Запись пишется по одному разу на category, не на каждый тик: attemptMerge
// повторяет причину на каждом проходе, пока её не уберут, а без дедупликации
// тикет затопило бы одинаковыми записями каждые несколько минут. По
// category, а не по последней записи (tracker.EventCategories, не
// самодельное сравнение с последней) — иначе две разные причины, случившиеся
// один за другим, потеряли бы друг друга: вторая переписала бы собой первую,
// а третья, которая на самом деле повторяет первую, снова показалась бы
// новой. Найдено внешним ревью PR: раньше это молча оседало только в логе
// раннера, который человек не читает, — задача могла зависнуть навсегда без
// единого следа в тикете (round 1), а первая версия дедупликации сравнивала
// только с последней записью, теряя вторую причину при чередовании (round 2).
func (o *Office) mergeBlocked(task tracker.Task, category, detail string) error {
	if tracker.EventCategories(task.Comments, tracker.EventMergeUnavailable)[category] {
		o.logf("%s: %s (эта причина уже звучала, повторно не пишу)", task.Key, category)
		return nil
	}
	runID, err := runner.NewRunID()
	if err != nil {
		return err
	}
	text := category + "\n" + detail
	if err := o.record(task.Key, tracker.BySystem(), tracker.Marker{
		RunID: runID, Role: o.Workflow.PR.Role, Event: tracker.EventMergeUnavailable, ConfigSHA: o.ConfigSHA,
	}, text); err != nil {
		return err
	}
	o.logf("%s: %s", task.Key, text)
	return nil
}

// mergePending разбирается с тем, что GitHub сам ещё не решил, годится ли
// pull request к слиянию (forge.ErrNotReady) — обязательные проверки или
// ревью не завершены. Большинство таких серий сами собой кончаются за
// несколько тиков (обычный CI), но не все: упавшая обязательная проверка
// или недостающее обязательное ревью выглядят для GitHub так же и сами
// не пройдут никогда. Предел терпеливее, чем у mergeRefused
// (limits.max_merge_pending, а не max_merge_refusals) ровно за счёт того,
// что оба случая неразличимы заранее.
func (o *Office) mergePending(task tracker.Task, project tracker.Project, url string, pendingErr error) error {
	runID, err := runner.NewRunID()
	if err != nil {
		return err
	}
	by := tracker.BySystem()
	pending := tracker.MergePending(task.Comments, o.Workflow.PR.Role) + 1

	if err := o.record(task.Key, by, tracker.Marker{
		RunID: runID, Role: o.Workflow.PR.Role, Event: tracker.EventMergePending, ConfigSHA: o.ConfigSHA,
	}, fmt.Sprintf("%v Гейт (нет конфликта, база %s не продвинулась) чист — офис попробует слияние "+
		"снова на следующем тике; если причина не в CI, а в чём-то, что само не пройдёт "+
		"(упавшая проверка, недостающее ревью), после %d таких попыток подряд офис позовёт человека.",
		pendingErr, project.PRBranch(), o.Workflow.Limits.MaxMergePending)); err != nil {
		return err
	}

	if pending >= o.Workflow.Limits.MaxMergePending {
		o.logf("%s: слияние не готово подряд %d раз, задача уходит к человеку", task.Key, pending)
		if err := o.record(task.Key, by, tracker.Marker{
			RunID: runID, Role: o.Workflow.PR.Role, Event: tracker.EventMergePendingExhausted, ConfigSHA: o.ConfigSHA,
		}, fmt.Sprintf("Слияние не становится готовым %d тиков подряд (limits.max_merge_pending). "+
			"Pull request %s остаётся открытым — похоже, дело не в CI: разберитесь (упавшая "+
			"обязательная проверка, недостающее обязательное ревью) и ответьте здесь, и офис "+
			"попробует слияние снова.", pending, url)); err != nil {
			return err
		}
		if err := o.move(task, by, o.Workflow.PR.Closed); err != nil {
			return err
		}
		return o.Tracker.SetHumanFlag(task.Key, by, true)
	}

	o.logf("%s: слияние пока не готово, задача остаётся в очереди прохода: %v", task.Key, pendingErr)
	return nil
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
	// Второй счётчик — общий с возвратами по продвинувшейся базе: чередование
	// «отказ → база уехала → отказ» обрывает серию отказов на каждом шаге,
	// и один только refusals своего предела не достиг бы никогда (tracker.PRReturns).
	returns := tracker.PRReturns(task.Comments, o.Workflow.PR.Role) + 1

	if err := o.record(task.Key, by, tracker.Marker{
		RunID: runID, Role: o.Workflow.PR.Role, Event: tracker.EventMergeRefused, ConfigSHA: o.ConfigSHA,
	}, fmt.Sprintf("Forge отказал в слиянии pull request %s, хотя локально гейт чист (нет конфликта, "+
		"база %s не продвинулась): %v. Разбираться с этим — не работа implementer'а: смотреть надо "+
		"на правило forge, о котором офис не знает (например, branch protection).",
		url, project.PRBranch(), mergeErr)); err != nil {
		return err
	}

	switch {
	case refusals >= o.Workflow.Limits.MaxMergeRefusals:
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

	case returns >= o.Workflow.Limits.MaxPRReturns:
		o.logf("%s: PR-проход не сходится подряд %d раз (чередование отказа и продвижения базы), "+
			"задача уходит к человеку", task.Key, returns)
		if err := o.record(task.Key, by, tracker.Marker{
			RunID: runID, Role: o.Workflow.PR.Role, Event: tracker.EventPRReturnsExhausted, ConfigSHA: o.ConfigSHA,
		}, fmt.Sprintf("PR-проход не сходится %d раз подряд (limits.max_pr_returns), чередуя отказ forge "+
			"и продвижение базы: ни одна из двух серий по отдельности предела не достигает, а задача "+
			"так и не сдвигается. Pull request %s остаётся открытым — дальше разбираться человеку: "+
			"уберите причину и ответьте здесь, и офис попробует снова.", returns, url)); err != nil {
			return err
		}
		if err := o.move(task, by, o.Workflow.PR.Closed); err != nil {
			return err
		}
		return o.Tracker.SetHumanFlag(task.Key, by, true)

	default:
		o.logf("%s: forge отказал в слиянии, задача остаётся в очереди прохода: %v", task.Key, mergeErr)
	}
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
	// Подпись говорит правду про этот проект, а не про офис вообще: на проекте
	// с auto_merge слить PR офис собирается сам, и звать за этим человека
	// значит звать его зря.
	merger := "сливает человек"
	if project.AutoMerge.Enabled {
		merger = "сливает офис сам, когда гейт чист (auto_merge)"
	}
	body += fmt.Sprintf("\n\n---\nЗадача: %s. Pull request открыт офисом; %s.", task.Key, merger)
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
