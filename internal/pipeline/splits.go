package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/kao73/virtual-office/internal/runner"
	"github.com/kao73/virtual-office/internal/tracker"
)

// splitAnalystRole — роль, чьи split-предложения достраивает этот проход.
// Разбиение — решение только analyst'а (roles/analyst/role.md): у
// implementer/reviewer в графе для исхода split есть только формальный
// маршрут ради Workflow.Validate(), они его не эмитят никогда.
const splitAnalystRole = "analyst"

// splitChildMarker — метка на созданном тикете-ребёнке, связывающая его
// с родителем и с id из split.children[]. Одна строка, не две: JQL/
// slices.Contains проще, а родитель+id вместе уже однозначны.
func splitChildMarker(parentKey, childID string) string {
	return "split-child:" + parentKey + ":" + childID
}

// CompleteSplits — системный проход: досоздаёт и связывает тикеты-детей
// подтверждённых split-предложений аналитика, по образцу Reap.
//
// Второе подряд outcome:split от analyst на одном тикете — подтверждение,
// не первое предложение (tracker.SplitConfirmed). Раннер сам, без прогона
// агента, создаёт недостающих детей, связывает их по depends_on и
// переводит родителя в терминальный статус — тем же путём, каким prSkipped
// уже уводит задачу без pull request (нет кода, нет PR, но офис с задачей
// закончил).
func (o *Office) CompleteSplits(ctx context.Context) error {
	flow, err := o.Workflow.Role(splitAnalystRole)
	if err != nil {
		// Графа без analyst не бывает в реальном workflow.yaml, но
		// синтетические тестовые графы могут его не иметь — тогда
		// достраивать split-предложения некому, и это не повод падать.
		return nil
	}
	if !o.Workflow.PR.Set() {
		// Проверка здесь, а не в closeSplitParent (её прежнее место —
		// последний шаг completeSplit): свойство офиса целиком, не одного
		// тикета, и раньше каждый заход цикла раннера создавал детей, копировал
		// вложения и связывал их заново, чтобы только на последнем шаге
		// узнать, что закрывать родителя всё равно некуда (config.go,
		// checkPR — граф без блока pr легален). Только в лог, не записью
		// в тикет: свойство офиса целиком, тикетов, за которые можно было
		// бы зацепиться, здесь ещё нет — тем же приёмом молчаливого, но
		// повторяющегося предупреждения, что уже применяет skipProject.
		o.logf("complete-splits: в workflow.yaml нет блока pr — закрывать разбитые задачи некуда, пропускаю")
		return nil
	}

	var failures []error
	for _, project := range o.projects() {
		refs, err := o.Tracker.List(project, []string{flow.Blocked()})
		if o.skipProject(project, err) {
			continue
		}
		if err != nil {
			return err
		}
		for _, ref := range refs {
			task, err := o.Tracker.Get(ref.Key)
			if err != nil {
				// Тем же приёмом, что Reap разбирает ErrNotOwner (pipeline.go):
				// беда с одним тикетом не должна глушить обход остальных.
				// Следующий заход цикла раннера попробует эту задачу снова.
				o.logf("%s: не прочитана, пропускаю: %v", ref.Key, err)
				failures = append(failures, fmt.Errorf("%s: не прочитана: %w", ref.Key, err))
				continue
			}
			confirmed, attachmentID := tracker.SplitConfirmed(task.Comments, splitAnalystRole)
			if !confirmed {
				continue
			}
			if err := o.completeSplit(task, attachmentID); err != nil {
				// completeSplit сам оборачивает ожидаемые сбои шагов через
				// splitFailed (который возвращает nil), так что ошибка,
				// дошедшая сюда, — неожиданная (например, упала сама запись
				// о сбое). Доккомент completeSplit обещает не прерывать
				// обход остальных задач; без этой развилки любая такая
				// ошибка обрывала бы CompleteSplits целиком, по всем
				// оставшимся тикетам и проектам. Копится, а не молчит —
				// но только эта, мета-беда (не прочиталась задача, не
				// записалась сама запись о сбое): ОБЫЧНЫЙ шаг split,
				// упавший и УСПЕШНО записавший об этом splitFailed'ом,
				// возвращает nil и сюда не попадает вовсе — это его
				// штатный, рабочий путь (Design.md decision #7: без
				// счётчика попыток, беда живёт в переписке тикета, не
				// в коде возврата). Значит ручная runner complete-splits
				// вернёт 0 и тогда, когда все тикеты застряли, но каждый
				// корректно об этом написал у себя — узкое, осознанное
				// сужение того, что здесь ловится (внешнее ревью,
				// pr-converge раунд 2 предложил это, раунд 3 уточнил
				// границу).
				o.logf("%s: проход не завершён, пробую снова в следующем цикле: %v", task.Key, err)
				failures = append(failures, fmt.Errorf("%s: %w", task.Key, err))
				continue
			}
		}
	}
	return errors.Join(failures...)
}

// completeSplit достраивает одно подтверждённое split-предложение: читает
// вложение, доводит до конца создание детей и связей, закрывает родителя.
//
// Ошибка любого шага, включая закрытие родителя в closeSplitParent, уходит
// системной записью в тикет (splitFailed) и не прерывает обход остальных
// задач в CompleteSplits — тем же приёмом, что Reap не роняет весь проход
// из-за одной беды.
func (o *Office) completeSplit(task tracker.Task, attachmentID string) error {
	children, err := o.splitChildren(task, attachmentID)
	if err != nil {
		return o.splitFailed(task, fmt.Sprintf("вложение %s не прочитано", attachmentID), err)
	}

	byID, err := o.ensureChildren(task, children)
	if err != nil {
		return o.splitFailed(task, "тикеты-дети не досозданы", err)
	}
	if err := o.ensureChildAttachments(task, byID); err != nil {
		return o.splitFailed(task, "вложения родителя не скопированы", err)
	}
	if err := o.linkChildren(children, byID); err != nil {
		return o.splitFailed(task, "связи depends_on не записаны", err)
	}

	keys := make([]string, len(children))
	for i, child := range children {
		keys[i] = byID[child.ID]
	}
	if err := o.closeSplitParent(task, keys); err != nil {
		return o.splitFailed(task, "родитель не закрыт", err)
	}
	return nil
}

// splitChildren скачивает вложение подтверждённого split и разбирает его
// в исходный список подзадач.
//
// Разбор заканчивается той же проверкой графа, что agentio.Result.Validate
// применяет к результату сразу после прогона агента (validateSplitChildren,
// findSplitCycle) — вложение лежит в трекере само по себе между записью
// и этим чтением, и человек, поправивший его руками, или порча хранилища
// может внести то, чего агент не писал: пустой или повторённый id, ссылку
// на несуществующий, цикл. Без повторной проверки такое дошло бы до
// LinkDependsOn пустым ключом или циклом связей.
func (o *Office) splitChildren(task tracker.Task, attachmentID string) ([]runner.SplitChild, error) {
	data, err := o.Tracker.GetAttachment(task.Key, attachmentID)
	if err != nil {
		return nil, err
	}
	var split runner.Split
	if err := json.Unmarshal(data, &split); err != nil {
		return nil, fmt.Errorf("вложение не разобрано: %w", err)
	}
	if err := split.Validate(); err != nil {
		return nil, fmt.Errorf("вложение не прошло проверку: %w", err)
	}
	return split.Children, nil
}

// ensureChildren заводит недостающих детей и отвечает ключом каждого по
// id из split.children[]. Идемпотентно: перед каждым созданием — опрос
// трекера по метке, а не хранимый флаг, так прерванная на середине пачка
// чинится следующим тиком сама, без дублей.
func (o *Office) ensureChildren(task tracker.Task, children []runner.SplitChild) (map[string]string, error) {
	keys := make(map[string]string, len(children))
	for _, child := range children {
		marker := splitChildMarker(task.Key, child.ID)
		found, err := o.Tracker.FindByMarker(task.Project, marker)
		if err != nil {
			return nil, err
		}
		if len(found) > 1 {
			// Не должно случаться на одной метке (FindByMarker ищет по
			// уникальной паре родитель+id), но раз найдено — берём первого
			// и говорим об этом вслух: тихо взятый первый прятал бы
			// коллизию, которую стоит увидеть человеку, а не гадать о ней.
			o.logf("%s: по метке %q найдено %d задач вместо одной, беру первую (%s)",
				task.Key, marker, len(found), found[0].Key)
		}
		if len(found) > 0 {
			keys[child.ID] = found[0].Key
			continue
		}

		description, parentVerbatim := childDescription(task, child)
		ref, err := o.Tracker.CreateTask(task.Project, tracker.TaskInput{
			Summary: child.Title, Description: description, DescriptionAppend: parentVerbatim, Labels: []string{marker},
		})
		if err != nil {
			return nil, err
		}
		keys[child.ID] = ref.Key
	}
	return keys, nil
}

// childDescription — описание ребёнка плюс исходная постановка родителя
// целиком, дословно, а не в пересказе.
//
// analyst у ребёнка не видит трекер (roles/analyst/role.md) и не может сам
// прочитать родителя, если тот не Done, — split.children[].description
// пишется до исследования, наспех, и то, что в него не попало (общая рамка
// задания, «вне рамок», требование из другого среза, которое касается и
// этого), для ребёнка пропадает безвозвратно. Живой прогон EXP-15→EXP-16..20
// (docs/notes/analyst-task-splitting.md) это показал: «вне рамок» и сквозные
// ограничения родителя ни в одном из пяти детей не встретились. Дословная
// копия, а не отдельная просьба к аналитику пересказать точнее — потому что
// для технической постановки пересказ рискует незаметно подменить точную
// деталь (схему, версию, формулировку) похожей, но другой.
//
// Копия — от task.Description, каким он был у **непосредственного**
// родителя на момент создания. Если родитель сам когда-то был ребёнком
// другого разбиения, его Description уже несёт унаследованный текст своего
// предка — новый уровень просто наращивает цепочку на одну копию, без
// отдельного понятия «корень».
//
// Возвращает две части, а не одну строку: parentVerbatim идёт в
// TaskInput.DescriptionAppend, а не в Description. task.Description мог
// быть прочитан из трекера уже в чужой разметке (JIRA хранит его как wiki,
// не markdown, — toTask отдаёт как есть), и если приписать его к
// Description, общий конвертер прогонит уже-переведённый текст повторно и
// исказит его (ссылки, упоминания, списки). Заголовок и пояснение — наша
// собственная, свежая проза, ей конвертация нужна, поэтому они остаются
// в description.
func childDescription(task tracker.Task, child runner.SplitChild) (description, parentVerbatim string) {
	parent := strings.TrimSpace(task.Description)
	if parent == "" {
		return child.Description, ""
	}
	var b strings.Builder
	b.WriteString(strings.TrimSpace(child.Description))
	b.WriteString("\n\n## Исходная постановка целиком (контекст)\n\n")
	b.WriteString("Эта задача — часть постановки, разбитой аналитиком на несколько тикетов. ")
	b.WriteString("Ниже — весь исходный текст: в нём могут быть детали и ограничения, ")
	b.WriteString("которые касаются именно этой части, но не попали в описание выше.")
	return b.String(), parent
}

// ensureChildAttachments докатывает человеческие вложения родителя
// (humanAttachments — служебные вроде runner.SplitAttachmentName сюда не
// входят) на каждого ребёнка. Тот же принцип идемпотентности, что
// ensureChildren применяет к самому факту существования тикета, — но
// сверяется с трекером заново на КАЖДОМ проходе CompleteSplits, а не
// только при первом создании: прогон, прерванный между «ребёнок создан»
// и «вложения скопированы», без этого навсегда оставил бы ребёнка без
// унаследованных вложений — повторный проход нашёл бы его уже
// существующим (по метке) и не вернулся бы к вложениям вовсе.
//
// Сверка — по имени вложения. Двух одноимённых вложений у родителя это
// не различит (докатится только одно) — узкий случай, которым ради
// простоты пренебрегаем: назначение здесь — не потерять контекст
// безвозвратно, не продублировать с абсолютной точностью.
//
// Скачивание — только для того, чего кому-то из детей и правда не хватает
// (сверяется по именам их уже имеющихся вложений раньше, чем по байтам
// родителя). Застрявший тикет (splitFailed на более позднем шаге, а этот
// уже прошёл) подбирается каждым заходом цикла раннера заново, и без этой развилки
// каждый такой цикл заново качал бы с родителя всё вложения ради самой
// сверки, которая в итоге ничего не докатывает.
//
// Обход детей — по отсортированным ключам, не по map: без этого порядок
// менялся бы между вызовами, и текст сбоя (splitFailed), назвав ошибку
// от случайного ребёнка на каждом проходе, никогда не совпадал бы сам
// с собой — даже когда причина ровно та же (внешнее ревью, pr-converge
// раунд 2).
func (o *Office) ensureChildAttachments(task tracker.Task, keys map[string]string) error {
	parentAttachments := humanAttachments(task)
	if len(parentAttachments) == 0 {
		return nil
	}

	childKeys := make([]string, 0, len(keys))
	for _, childKey := range keys {
		childKeys = append(childKeys, childKey)
	}
	slices.Sort(childKeys)

	children := make(map[string]tracker.Task, len(childKeys))
	needed := make(map[string]bool, len(parentAttachments)) // имя вложения → не хватает хотя бы одному ребёнку
	for _, childKey := range childKeys {
		child, err := o.Tracker.Get(childKey)
		if err != nil {
			return err
		}
		children[childKey] = child
		has := make(map[string]bool, len(child.Attachments))
		for _, a := range child.Attachments {
			has[a.Name] = true
		}
		for _, parentAttachment := range parentAttachments {
			if !has[parentAttachment.Name] {
				needed[parentAttachment.Name] = true
			}
		}
	}
	if len(needed) == 0 {
		return nil
	}

	// Байты родителя скачиваются один раз на нужное имя, не на каждого
	// ребёнка и не на каждое вложение с этим именем: на JIRA GetAttachment
	// — два HTTP-запроса (метаданные + содержимое), и без кеша разбиение
	// на 5 детей с 3 вложениями стоило бы 30 запросов ради трёх файлов.
	// Одноимённых вложений родителя с разными id всё равно докатится
	// только одно (has ниже сверяется по имени) — скачивать второе было
	// бы запросом впустую.
	data := make(map[string][]byte, len(needed))
	downloaded := make(map[string]bool, len(needed))
	for _, parentAttachment := range parentAttachments {
		if !needed[parentAttachment.Name] || downloaded[parentAttachment.Name] {
			continue
		}
		bytes, err := o.Tracker.GetAttachment(task.Key, parentAttachment.ID)
		if err != nil {
			return err
		}
		data[parentAttachment.ID] = bytes
		downloaded[parentAttachment.Name] = true
	}

	by := tracker.BySystem()
	for _, childKey := range childKeys {
		child := children[childKey]
		has := make(map[string]bool, len(child.Attachments))
		for _, a := range child.Attachments {
			has[a.Name] = true
		}
		for _, parentAttachment := range parentAttachments {
			if has[parentAttachment.Name] {
				continue
			}
			if _, err := o.Tracker.AddAttachment(childKey, by, parentAttachment.Name, data[parentAttachment.ID]); err != nil {
				return err
			}
			has[parentAttachment.Name] = true
		}
	}
	return nil
}

// linkChildren связывает уже существующих детей по depends_on. Отдельным
// подпроходом после того, как **все** дети существуют: ребёнок может
// зависеть от того, кто в split.children[] идёт позже него, и связывать
// раньше, чем существуют оба конца, нечем.
//
// Ленивый: перед LinkDependsOn проверяет Get(key).DependsOn — теперь,
// когда чтение надёжно и на JIRA тоже (split-dependency-gate,
// internal/tracker/jira/jira.go toTask/searchFields). Дело не в цене
// (fix round 2, Finding 10): на JIRA сам этот Get() тянет ещё и всю
// переписку тикета, постранично, и при одной зависимости обходится
// не дешевле, а то и дороже единственного POST /issueLink, который он
// иногда позволяет пропустить — округление скорее в минус, чем в плюс.
// Настоящая причина — не полагаться на недокументированное серверное
// дедуплицирование одинаковых POST /issueLink: раньше застрявший на
// закрытии тикет слал их заново на каждый заход цикла раннера, рассчитывая, что
// JIRA сама тихо проигнорирует повтор. Выгода этой проверки растёт вместе
// с числом зависимостей у ребёнка и с числом повторов на застрявшем
// тикете — а не с количеством сбережённых байт на первом же цикле.
func (o *Office) linkChildren(children []runner.SplitChild, keys map[string]string) error {
	by := tracker.BySystem()
	for _, child := range children {
		if len(child.DependsOn) == 0 {
			continue
		}
		existing, err := o.Tracker.Get(keys[child.ID])
		if err != nil {
			return err
		}
		for _, dep := range child.DependsOn {
			depKey := keys[dep]
			if slices.Contains(existing.DependsOn, depKey) {
				continue
			}
			if err := o.Tracker.LinkDependsOn(keys[child.ID], depKey, by); err != nil {
				return err
			}
		}
	}
	return nil
}

// closeSplitParent сообщает о готовых детях и закрывает родителя — тем же
// терминальным статусом, что и prSkipped: задача, которой нечего сливать,
// заканчивает жизнь так же, как слитая.
//
// PR.Set() здесь не проверяется — единственный вызывающий, CompleteSplits,
// уже отказался раньше первой мутации, если блока pr нет вовсе.
//
// Запись «Разбита на: …» — только при первом успехе (HasEvent), тем же
// приёмом, что splitFailed применяет к своей записи о сбое: если
// SetHumanFlag/move ниже упадут, повторный проход не должен постить этот
// комментарий заново на каждом заходе цикла раннера.
func (o *Office) closeSplitParent(task tracker.Task, keys []string) error {
	by, to := tracker.BySystem(), o.Workflow.PR.Merged

	if !tracker.HasEvent(task.Comments, tracker.EventSplitCreated) {
		runID, err := runner.NewRunID()
		if err != nil {
			return err
		}
		if err := o.record(task.Key, by, tracker.Marker{
			RunID: runID, Role: splitAnalystRole, Event: tracker.EventSplitCreated, ConfigSHA: o.Identity,
		}, fmt.Sprintf("Разбита на: %s. Тикеты-дети созданы и связаны по depends_on автоматически, "+
			"задача уходит в %s.", strings.Join(keys, ", "), to)); err != nil {
			return err
		}
		o.logf("%s: разбита на %s, уходит в %s", task.Key, strings.Join(keys, ", "), to)
	}

	// finish() поднял HumanFlag, отправляя подтверждённый split в Blocked
	// (workflow.yaml). Снять его надо здесь же, как unblock() снимает его
	// перед своим move: иначе закрытый тикет остаётся с меткой «ждёт
	// человека» на живой доске, хотя ждать уже нечего — HumanReplies его
	// больше не подберёт (задача не в human-статусе), но метка вводит в
	// заблуждение того, кто смотрит на доску глазами.
	if err := o.Tracker.SetHumanFlag(task.Key, by, false); err != nil {
		return err
	}
	return o.move(task, by, to)
}

// splitFailed пишет системную запись о неудавшейся попытке достроить
// split. Родитель остаётся в Blocked, а не уходит к человеку: идемпотентный
// опрос трекера делает повтор безопасным всегда, и это не прогон агента —
// тратить attempts или звать человека здесь не за что (тот же довод, что
// у Archive/открытия PR). Следующий заход цикла раннера (или ручной
// runner complete-splits) попробует снова.
//
// category — стабильный, детерминированный на повторе одной и той же
// причины ярлык шага (вызывающие в completeSplit дают ровно пять таких
// ярлыков — и внутри одного ярлыка разные причины неразличимы: например,
// «вложения родителя не скопированы» из-за 403 на GetAttachment и из-за
// ErrNotOwner на AddAttachment запишутся одной и той же записью — цена,
// которую эта дедупликация платит за грубость категорий, принята
// сознательно). err — свободный текст вокруг него, для человека. Запись —
// только если причина новая: цикл раннера (cmd/runner, cycle) зовёт CompleteSplits каждый заход (по
// умолчанию раз в две минуты), а застрявший тикет остаётся в Blocked
// и подбирается им снова и снова. Дедупликация — по category, первой
// строке записи, сверенной со множеством ВСЕХ уже записанных категорий
// (tracker.EventCategories), а не только с последней и не по всему
// тексту целиком (внешнее ревью, pr-converge: раунд 1 предложил
// сравнивать текст, раунд 2 нашёл, что полный текст ненадёжен — на JIRA
// он приезжает через wiki() без обратного перевода, и свободный текст
// ошибки, несущий тело ответа сервера, легко получает то, что wiki()
// экранирует; раунд 3 нашёл, что сравнение с последней записью не ловит
// чередование двух причин между циклами). category — простая проза без
// wiki-разметки и не участвует в этой судьбе; err — участвует, поэтому
// в сравнение не идёт. Design.md decision #7 (без счётчика попыток и
// эскалации) этим не затрагивается — считается не число сбоев, а сам
// факт «об этой причине уже сказано».
func (o *Office) splitFailed(task tracker.Task, category string, cause error) error {
	text := category
	if cause != nil {
		text += "\n" + cause.Error()
	}
	if tracker.EventCategories(task.Comments, tracker.EventSplitCreateFailed)[category] {
		o.logf("%s: %s (эта причина уже звучала, повторно не пишу)", task.Key, category)
		return nil
	}

	runID, err := runner.NewRunID()
	if err != nil {
		return err
	}
	if err := o.record(task.Key, tracker.BySystem(), tracker.Marker{
		RunID: runID, Role: splitAnalystRole, Event: tracker.EventSplitCreateFailed, ConfigSHA: o.Identity,
	}, text); err != nil {
		return err
	}
	o.logf("%s: %s", task.Key, text)
	return nil
}
