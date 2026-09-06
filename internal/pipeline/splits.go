package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
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
				return err
			}
			confirmed, attachmentID := tracker.SplitConfirmed(task.Comments, splitAnalystRole)
			if !confirmed {
				continue
			}
			if err := o.completeSplit(task, attachmentID); err != nil {
				return err
			}
		}
	}
	return nil
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
		return o.splitFailed(task, fmt.Sprintf("вложение %s не прочитано: %v", attachmentID, err))
	}

	byID, err := o.ensureChildren(task, children)
	if err != nil {
		return o.splitFailed(task, fmt.Sprintf("тикеты-дети не досозданы: %v", err))
	}
	if err := o.linkChildren(children, byID); err != nil {
		return o.splitFailed(task, fmt.Sprintf("связи depends_on не записаны: %v", err))
	}

	keys := make([]string, len(children))
	for i, child := range children {
		keys[i] = byID[child.ID]
	}
	if err := o.closeSplitParent(task, keys); err != nil {
		return o.splitFailed(task, fmt.Sprintf("родитель не закрыт: %v", err))
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

		ref, err := o.Tracker.CreateTask(task.Project, tracker.TaskInput{
			Summary: child.Title, Description: child.Description, Labels: []string{marker},
		})
		if err != nil {
			return nil, err
		}
		keys[child.ID] = ref.Key
	}
	return keys, nil
}

// linkChildren связывает уже существующих детей по depends_on. Отдельным
// подпроходом после того, как **все** дети существуют: ребёнок может
// зависеть от того, кто в split.children[] идёт позже него, и связывать
// раньше, чем существуют оба конца, нечем.
func (o *Office) linkChildren(children []runner.SplitChild, keys map[string]string) error {
	by := tracker.BySystem()
	for _, child := range children {
		for _, dep := range child.DependsOn {
			if err := o.Tracker.LinkDependsOn(keys[child.ID], keys[dep], by); err != nil {
				return err
			}
		}
	}
	return nil
}

// closeSplitParent сообщает о готовых детях и закрывает родителя — тем же
// терминальным статусом, что и prSkipped: задача, которой нечего сливать,
// заканчивает жизнь так же, как слитая.
func (o *Office) closeSplitParent(task tracker.Task, keys []string) error {
	runID, err := runner.NewRunID()
	if err != nil {
		return err
	}
	by, to := tracker.BySystem(), o.Workflow.PR.Merged
	if err := o.record(task.Key, by, tracker.Marker{
		RunID: runID, Role: splitAnalystRole, Event: tracker.EventSplitCreated, ConfigSHA: o.ConfigSHA,
	}, fmt.Sprintf("Разбита на: %s. Тикеты-дети созданы и связаны по depends_on автоматически, "+
		"задача уходит в %s.", strings.Join(keys, ", "), to)); err != nil {
		return err
	}
	o.logf("%s: разбита на %s, уходит в %s", task.Key, strings.Join(keys, ", "), to)
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
// у Archive/открытия PR). Следующий цикл Loop (или ручной
// runner complete-splits) попробует снова.
//
// Запись — только первая: Loop зовёт CompleteSplits каждый цикл (по
// умолчанию раз в две минуты), а застрявший тикет остаётся в Blocked
// и подбирается им снова и снова. Без дедупликации, тем же приёмом,
// что и у warnRunCost, одинаковая запись копилась бы в переписке без
// конца, пока человек не вмешается. Design.md decision #7 (без счётчика
// попыток и эскалации) этим не затрагивается — считается не число сбоев,
// а сам факт «уже сказано».
func (o *Office) splitFailed(task tracker.Task, text string) error {
	if tracker.HasEvent(task.Comments, tracker.EventSplitCreateFailed) {
		o.logf("%s: %s (уже сообщено, повторно не пишу)", task.Key, text)
		return nil
	}

	runID, err := runner.NewRunID()
	if err != nil {
		return err
	}
	if err := o.record(task.Key, tracker.BySystem(), tracker.Marker{
		RunID: runID, Role: splitAnalystRole, Event: tracker.EventSplitCreateFailed, ConfigSHA: o.ConfigSHA,
	}, text); err != nil {
		return err
	}
	o.logf("%s: %s", task.Key, text)
	return nil
}
