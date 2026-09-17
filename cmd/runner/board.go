package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/kao73/virtual-office/internal/pipeline"
	"github.com/kao73/virtual-office/internal/tracker"
)

// boardCommand печатает плоский список: задачи всех проектов во всех статусах
// графа, по трекеру за раз.
//
// Это не очередь: задачи с живой арендой из неё не выбрасываются, потому что
// «кто работает прямо сейчас» — первое, что человек ищет глазами. Переписку
// команда не тянет вовсе — она стоит запроса на задачу, а показать её негде.
func boardCommand(args []string, out io.Writer) error {
	fs := flags("ls")
	project := fs.String("project", "", "показывать только этот проект")

	all, err := newOffices(fs, args, out)
	if err != nil {
		return err
	}
	return printBoards(all, *project, time.Now(), out)
}

// printBoards — доска каждого офиса под его именем (заголовок ставит each,
// и только когда офисов больше одного); с проектом — только его офис.
// Раскладку конфигурации печатает конструктор, один раз на все офисы.
func printBoards(all *offices, project string, now time.Time, out io.Writer) error {
	if project != "" {
		no, err := all.byProject(project)
		if err != nil {
			return err
		}
		return printBoard(no.Tracker, []string{project}, no.Workflow.Statuses, no.Workflow.IsTerminal, now, out)
	}
	return all.each(context.Background(), func(no namedOffice) error {
		return printBoard(no.Tracker, no.Projects.Keys(), no.Workflow.Statuses, no.Workflow.IsTerminal, now, out)
	})
}

// printBoard печатает доску проектов: по запросу на проект, без переписки.
//
// terminal решает, что считать «зависимость уже разрешена» — то же
// понятие графа, что использует гейт claim() (internal/pipeline.
// UnmetDependencies), а не отдельное здесь понятие: расхождение между
// тем, что видит ls, и тем, что реально блокирует claim(), было бы хуже,
// чем лишний параметр.
func printBoard(tasks tracker.Tracker, projects, statuses []string, terminal func(string) bool, now time.Time, out io.Writer) error {
	shown := 0
	for _, project := range projects {
		refs, err := tasks.List(project, statuses)
		if notice, skip := tracker.SkipUnknownProject(project, err); skip {
			fmt.Fprintln(out, notice)
			continue
		}
		if err != nil {
			return err
		}

		byKey := pipeline.ByKey(refs)

		for _, ref := range refs {
			unmet := pipeline.UnmetDependencies(ref, byKey, terminal)
			// dependsColumn — хвостом строки, после summary, а не вставкой
			// между waiting и age (fix round 2, Finding 6): переменная
			// ширина в середине строки сдвигала бы вправо все колонки
			// после себя на любой заблокированной задаче, и вся доска
			// теряла построчное выравнивание, не только одна ячейка.
			line := fmt.Sprintf("%-10s %-12s %-24s попыток:%d %-16s %-10s %s",
				ref.Key, ref.Status, lease(ref, now), ref.Attempts, waiting(ref), age(ref.Updated, now), ref.Summary)
			if depends := dependsColumn(unmet); depends != "" {
				// Разделитель — не просто пробел (pr-converge round 1,
				// Finding 12): summary — свободный текст без своей границы
				// справа, и «...часть ждёт: OFF-1» читается как продолжение
				// заголовка, а не отдельная колонка.
				line += " | " + depends
			}
			fmt.Fprintln(out, line)
			shown++
		}
	}
	if shown == 0 {
		// Со списком проектов, а не просто «пусто»: задача чужого проекта на доску
		// не попадает вовсе, и молчаливое «задач нет» отправило бы искать не там.
		fmt.Fprintf(out, "задач нет (проекты: %s)\n", strings.Join(projects, ", "))
	}
	return nil
}

// dependsColumn — «ждёт: OFF-1 (Ready)» для задачи, у которой есть
// незакрытые зависимости; пусто — зависимостей нет или все терминальны.
// Печатается хвостом строки printBoard, после summary (fix round 2,
// Finding 6): переменная ширина здесь не сдвигает ни одну из колонок
// фиксированной ширины левее себя, в отличие от прежнего места — между
// waiting и age, где она сдвигала весь остаток строки на любой
// заблокированной задаче. Без собственного обрамляющего пробела — вызывающий
// (printBoard) сам решает, ставить ли разделитель перед непустым значением.
//
// Формат самой находки — pipeline.DescribeUnmet, а не своя копия (pr-converge
// round 1, Finding 1): раньше здесь было отдельное форматирование,
// синхронизированное с internal/pipeline/deps.go только парой комментариев
// «расходиться им нельзя» — вопреки собственному decision #3 этого PR
// («одна реализация, не две»).
func dependsColumn(unmet []tracker.TaskRef) string {
	if len(unmet) == 0 {
		return ""
	}
	return fmt.Sprintf("ждёт: %s", pipeline.DescribeUnmet(unmet))
}

// lease — кто держит задачу. Истёкшая аренда показывается отдельно от её
// отсутствия: это не «свободна», а «прогон не дожил», и увидеть разницу важно.
func lease(ref tracker.TaskRef, now time.Time) string {
	switch {
	case ref.LeaseAlive(now):
		return fmt.Sprintf("%s до %s", ref.Owner, ref.LeaseUntil.Local().Format("15:04:05"))
	case ref.RunID != "":
		return "аренда истекла"
	default:
		return "—"
	}
}

func waiting(ref tracker.TaskRef) string {
	if ref.HumanFlag {
		return "[ждёт человека]"
	}
	return ""
}

// age — сколько задачу не трогали, огрублённо. Человеку здесь нужен
// порядок величины: «висит третий день» решает, а «висит 62 часа 14 минут» — нет.
func age(updated, now time.Time) string {
	if updated.IsZero() {
		return "—"
	}
	switch since := now.Sub(updated); {
	case since < time.Minute:
		return "только что"
	case since < time.Hour:
		return fmt.Sprintf("%dм", int(since.Minutes()))
	case since < 24*time.Hour:
		return fmt.Sprintf("%dч", int(since.Hours()))
	default:
		return fmt.Sprintf("%dд", int(since.Hours()/24))
	}
}
