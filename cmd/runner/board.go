package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/kao73/virtual-office/internal/pipeline"
	"github.com/kao73/virtual-office/internal/tracker"
)

// boardCommand печатает плоский список: задачи всех проектов во всех статусах графа.
//
// Это не очередь: задачи с живой арендой из неё не выбрасываются, потому что
// «кто работает прямо сейчас» — первое, что человек ищет глазами. Переписку
// команда не тянет вовсе — она стоит запроса на задачу, а показать её негде.
func boardCommand(args []string, out io.Writer) error {
	fs := flags("ls")
	project := fs.String("project", "", "показывать только этот проект")

	o, err := office(fs, args, out)
	if err != nil {
		return err
	}

	projects := o.Projects.Keys()
	if *project != "" {
		if _, known := o.Projects[*project]; !known {
			return fmt.Errorf("проект %q не описан в %s и %s либо заведён под другой трекер",
				*project, tracker.ProjectsFile, tracker.ProjectsLocalFile)
		}
		projects = []string{*project}
	}
	return printBoard(o.Tracker, projects, o.Workflow.Statuses, o.Workflow.IsTerminal, time.Now(), out)
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

		byKey := make(map[string]tracker.TaskRef, len(refs))
		for _, ref := range refs {
			byKey[ref.Key] = ref
		}

		for _, ref := range refs {
			unmet := pipeline.UnmetDependencies(ref, byKey, terminal)
			fmt.Fprintf(out, "%-10s %-12s %-24s попыток:%d %-16s %s%-10s %s\n",
				ref.Key, ref.Status, lease(ref, now), ref.Attempts, waiting(ref), dependsColumn(unmet), age(ref.Updated, now), ref.Summary)
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

// dependsColumn — «ждёт: OFF-1 (Ready) » рядом с задачей, у которой есть
// незакрытые зависимости; пусто — зависимостей нет или все терминальны.
// Несёт собственную заполняющую пробельность: формат строки выше не
// держит отдельного места для этой колонки между waiting и age, только
// сама колонка и её хвостовой пробел, когда она непуста.
func dependsColumn(unmet []tracker.TaskRef) string {
	if len(unmet) == 0 {
		return ""
	}
	parts := make([]string, len(unmet))
	for i, dep := range unmet {
		key := dep.Key
		if key == "" {
			key = "неизвестная задача"
		}
		parts[i] = fmt.Sprintf("%s (%s)", key, dep.Status)
	}
	return fmt.Sprintf("ждёт: %s ", strings.Join(parts, ", "))
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
