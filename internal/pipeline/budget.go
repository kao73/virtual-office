package pipeline

import (
	"errors"
	"fmt"
	"time"

	"github.com/kao73/virtual-office/internal/budget"
	"github.com/kao73/virtual-office/internal/ledger"
	"github.com/kao73/virtual-office/internal/runner"
	"github.com/kao73/virtual-office/internal/tracker"
)

// Учёт и ограничение — разные вещи, и разведены они здесь по той же границе,
// что в пакетах ledger и budget: реестр ведётся всегда и ничего не решает,
// пределы — необязательная политика над ним.
//
// Проверки стоят в трёх разных местах, и места эти выбраны по смыслу, а не по
// удобству. Дневной предел роли — в начале тика: он про роль, а не про задачу.
// Предел задачи — до Ensure: иначе на пропускаемую задачу заводится клон проекта
// и рабочая папка. Предел прогона — после прогона: раньше его цена неизвестна,
// а прервать прогон по цене нельзя вовсе — к моменту, когда она известна, работа
// сделана и оплачена. Жёсткая граница у прогона своя, и она не в деньгах:
// limits.max_turns в роли.

// account записывает прогон в реестр.
//
// Неудача записи не роняет прогон: работа сделана и опубликована, а потерянная
// строка учёта — беда бухгалтерии, а не задачи. Молчать о ней всё же нельзя:
// на суммах стоят пределы, и незаметно занизившийся расход хуже отсутствующего.
func (o *Office) account(e ledger.Entry) {
	if !e.Known() {
		o.logf("%s: цена прогона %s неизвестна — агент не оставил итога", e.Task, short(e.RunID))
	}
	if o.Ledger == nil {
		return
	}
	if err := o.Ledger.Append(e); err != nil {
		o.logf("%s: прогон %s не записан в реестр: %v", e.Task, short(e.RunID), err)
	}
}

// spent — сколько потрачено по реестру.
//
// Ошибка чтения здесь возвращается наверх и останавливает цикл, а не пропускается
// в лог. Спрашивают об этом только тогда, когда предел настроен, — а предел,
// тихо переставший срабатывать из-за нечитаемого файла, хуже отсутствующего:
// человек уверен, что расход ограничен.
func (o *Office) spent(f ledger.Filter) (float64, error) {
	if o.Ledger == nil {
		return 0, nil
	}
	total, err := o.Ledger.Sum(f)
	if err != nil {
		return 0, fmt.Errorf("предел расхода не проверен: %w", err)
	}
	if total.Broken > 0 && o.sayOnce("реестр-испорчен") {
		o.logf("реестр %s: %d строк не разобрано, сумма занижена", o.Ledger.Path, total.Broken)
	}
	return total.CostUSD, nil
}

// roleOverspent — исчерпала ли роль дневной бюджет. Отвечает, брать ли работу.
//
// В тикет здесь не пишут ничего, и это по смыслу: дневной расход — свойство
// раннера, а не задачи. Задача ни в чём не виновата и лежит нетронутой в своей
// очереди; человеку об этом говорит лог.
func (o *Office) roleOverspent(roleName string) (bool, error) {
	limit := o.Budgets.PerRoleDaily
	if !limit.Set() {
		return false, nil
	}

	since := startOfDay(o.now())
	spent, err := o.spent(ledger.Filter{Role: roleName, Since: since, ExcludeEval: true})
	if err != nil || !limit.Exceeded(spent) {
		return false, err
	}

	// Раз в день, а не раз в цикл: цикл идёт раз в две минуты, и до конца суток
	// одна и та же строка легла бы в лог сотни раз.
	if o.sayOnce(fmt.Sprintf("дневной-бюджет-%s-%s", roleName, since.Format(time.DateOnly))) {
		o.logf("%s: дневной расход роли $%.4f при пределе $%.2f (%s, режим %s)%s",
			roleName, spent, limit.USD, budget.File, limit.OnExceed, stopsWork(limit))
	}
	return limit.Stops(), nil
}

// taskOverspent — исчерпала ли задача свой бюджет. Отвечает, пропустить ли её.
//
// Режим warn задачу не трогает: работа продолжается, а человек узнаёт о цене
// записью в тикете — один раз за жизнь задачи, чтобы дорогой тикет не зарос
// одинаковыми предупреждениями. Режим stop останавливает работу до её начала
// и отдаёт задачу человеку: сам раннер поднять предел не может и обходить его
// не станет.
func (o *Office) taskOverspent(ref tracker.TaskRef, roleName string, flow tracker.RoleFlow) (bool, error) {
	limit := o.Budgets.PerTask
	if !limit.Set() {
		return false, nil
	}

	spent, err := o.spent(ledger.Filter{Task: ref.Key})
	if err != nil || !limit.Exceeded(spent) {
		return false, err
	}

	task, err := o.Tracker.Get(ref.Key)
	if err != nil {
		return false, err
	}

	o.logf("%s: задача стоила $%.4f при пределе $%.2f (%s, режим %s)%s",
		ref.Key, spent, limit.USD, budget.File, limit.OnExceed, stopsWork(limit))

	if !limit.Stops() {
		if tracker.HasEvent(task.Comments, tracker.EventBudgetExceeded) {
			return false, nil
		}
		_, err := o.budgetNotice(task, roleName, tracker.EventBudgetExceeded, fmt.Sprintf(
			"Задача уже стоила $%.4f при пределе $%.2f на задачу (%s, per_task, режим warn). "+
				"Работа продолжается — это предупреждение, а не остановка. Цена каждого прогона "+
				"названа в его отчёте выше.", spent, limit.USD, budget.File))
		return false, err
	}

	switch said, err := o.budgetNotice(task, roleName, tracker.EventBudgetExhausted, fmt.Sprintf(
		"Задача стоила $%.4f при пределе $%.2f на задачу (%s, per_task, режим stop). "+
			"В работу она больше не берётся и ждёт человека.\n\n"+
			"Ответ на этот вопрос вернёт её в очередь, но в тот же предел она упрётся снова: "+
			"поднимите per_task или переведите его в режим warn — сам предел раннер не обходит.",
		spent, limit.USD, budget.File)); {
	case err != nil:
		return true, err
	case !said:
		// Задачу перехватили — двигать её мы больше не вправе. Пропускаем:
		// о пределе скажет тот, кто возьмётся за неё следующим.
		return true, nil
	}

	by := tracker.BySystem()
	if err := o.move(task, by, flow.Blocked()); err != nil {
		return true, err
	}
	return true, o.Tracker.SetHumanFlag(task.Key, by, true)
}

// budgetNotice пишет системную запись о пределе и отвечает, удалось ли.
//
// Системную — потому что аренды у задачи в этот момент нет и быть не должно:
// проверка стоит до захвата, и правило владения требует от системного актора
// её отсутствия. Задачу могли захватить между выборкой и записью — это не беда
// офиса, а обычная гонка, и трогать чужую задачу дальше мы не вправе.
func (o *Office) budgetNotice(task tracker.Task, roleName, event, text string) (bool, error) {
	runID, err := runner.NewRunID()
	if err != nil {
		return false, err
	}
	err = o.notice(task.Key, tracker.Marker{
		RunID: runID, Role: roleName, Event: event, ConfigSHA: o.Identity,
	}, text)
	if errors.Is(err, tracker.ErrNotOwner) {
		o.logf("%s: о пределе расхода сказать не вышло (%v), задачу взял кто-то другой", task.Key, err)
		return false, nil
	}
	return err == nil, err
}

// warnRunCost говорит о прогоне, обошедшемся дороже предела.
//
// Только говорит: прерывать нечего, работа уже сделана и оплачена. Запись идёт
// от лица прогона — аренда ещё жива, и системной она быть не может.
//
// Один раз на задачу: дорогие прогоны на дорогой задаче идут подряд, и запись
// на каждый превратила бы тикет в ленту бухгалтерии. Остальные видны в логе
// и в реестре.
func (o *Office) warnRunCost(task tracker.Task, runID, roleName string, usage runner.Usage) error {
	limit := o.Budgets.PerRun
	if !limit.Set() || !usage.Known() || !limit.Exceeded(usage.CostUSD) {
		return nil
	}

	o.logf("%s: прогон %s стоил $%.4f при пределе $%.2f на прогон (%s, режим %s)",
		task.Key, short(runID), usage.CostUSD, limit.USD, budget.File, limit.OnExceed)
	if tracker.HasEvent(task.Comments, tracker.EventRunBudgetExceeded) {
		return nil
	}

	return o.record(task.Key, tracker.ByRun(runID), tracker.Marker{
		RunID: runID, Role: roleName, Event: tracker.EventRunBudgetExceeded, ConfigSHA: o.Identity,
	}, fmt.Sprintf("Прогон run:%s стоил $%.4f при пределе $%.2f на прогон (%s, per_run). "+
		"Прервать его было нечем: цена известна, когда работа уже сделана, — жёсткая граница "+
		"прогона задаётся числом шагов (limits.max_turns в role.yaml). "+
		"Это первое такое предупреждение по задаче; остальные — в логе раннера и в реестре прогонов.",
		short(runID), usage.CostUSD, limit.USD, budget.File))
}

// stopsWork — хвост строки лога: чем предел кончился для работы.
func stopsWork(limit budget.Limit) string {
	if limit.Stops() {
		return ", работа остановлена"
	}
	return ", работа продолжается"
}

// startOfDay — начало календарных суток по местному времени машины.
//
// Именно местных: «за сутки» человек считает по своим часам, а не по UTC.
// Раннеры в разных поясах посчитают свои сутки по-своему — но реестр у каждого
// свой, так что складывать их всё равно не с чем.
func startOfDay(now time.Time) time.Time {
	year, month, day := now.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, now.Location())
}
