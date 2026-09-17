package main

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/kao73/virtual-office/internal/tracker"
	"github.com/kao73/virtual-office/internal/workspace"
)

const worktreeUsage = `runner worktree — рабочие папки задач

  runner worktree ls              что лежит и сколько занимает
  runner worktree rm <KEY> [--force]   удалить рабочую папку задачи

Ветка удаление переживает: она живёт в bare-клоне проекта. Без --force команда
не тронет папку с незакоммиченной работой и папку задачи с живой арендой.`

// worktreeCommand исполняет подкоманду worktree.
func worktreeCommand(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(worktreeUsage)
	}

	switch args[0] {
	case "ls":
		return worktreeList(out)
	case "rm":
		return worktreeRemove(args[1:], out)
	default:
		return fmt.Errorf("неизвестная подкоманда %q\n\n%s", args[0], worktreeUsage)
	}
}

// worktreeList показывает, что накопилось.
//
// Автоматическая уборка есть — её делает системный проход, — но покрывает она
// один случай: задачу, дошедшую до терминального статуса. Всё остальное копится
// и убирается руками: незаконченная работа, папка с незакоммиченным (её проход
// не трогает намеренно) и папка задачи, которую человек увёл из конвейера.
// Для этого человеку нужна не политика, а картина.
func worktreeList(out io.Writer) error {
	workspaces, err := workspace.Default()
	if err != nil {
		return err
	}
	entries, err := workspaces.List()
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Fprintln(out, "рабочих папок нет")
		return nil
	}

	var total int64
	for _, entry := range entries {
		total += entry.Size
		fmt.Fprintf(out, "%-12s %-20s %8s  %s\n",
			entry.Key, entry.Branch, size(entry.Size), state(entry))
	}
	fmt.Fprintf(out, "\nвсего %d %s, %s\n", len(entries), plural(len(entries)), size(total))
	return nil
}

// plural склоняет «папку» по числу: строка эта попадается человеку на глаза
// каждый раз, и «1 папок» в ней выглядит небрежностью.
func plural(n int) string {
	switch last, tens := n%10, n%100; {
	case tens >= 11 && tens <= 14:
		return "папок"
	case last == 1:
		return "папка"
	case last >= 2 && last <= 4:
		return "папки"
	default:
		return "папок"
	}
}

// worktreeRemove удаляет рабочую папку задачи.
func worktreeRemove(args []string, out io.Writer) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return fmt.Errorf("нужен ключ задачи первым аргументом\n\n%s", worktreeUsage)
	}
	key := args[0]

	fs := flags("worktree rm")
	force := fs.Bool("force", false, "снести, несмотря на незакоммиченное и живую аренду")

	// Трекер нужен ровно за одним: узнать, не работает ли сейчас над задачей
	// агент. Всё остальное команда знает из git и с диска. Флаги разбирает
	// newOffices — он же их и объявляет, поэтому раньше него парсить нечего.
	all, err := newOffices(fs, args[1:], out)
	if err != nil {
		return err
	}
	return removeWorktree(all, key, *force, time.Now(), out)
}

// removeWorktree — само удаление, отдельно от сборки офисов ради теста.
//
// Папка знает свой проект, проект — трекер, трекер — офис: про аренду
// спрашивается тот трекер, в котором задача живёт, а не первый попавшийся.
func removeWorktree(all *offices, key string, force bool, now time.Time, out io.Writer) error {
	entries, err := all.workspaces.List()
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.Key != key {
			continue
		}

		no, err := all.byProject(entry.Project)
		if err != nil {
			return err
		}
		task, err := no.Tracker.Get(key)
		if err != nil && !errors.Is(err, tracker.ErrNotFound) {
			return err
		}
		if err := removable(entry, task, now, force); err != nil {
			return err
		}
		if err := all.workspaces.Remove(entry.Workspace); err != nil {
			return err
		}
		fmt.Fprintf(out, "%s: рабочая папка удалена, ветка %s осталась в клоне\n", key, entry.Branch)
		return nil
	}
	return fmt.Errorf("рабочей папки задачи %s нет; что есть — покажет `runner worktree ls`", key)
}

// removable решает, можно ли сносить рабочую папку.
//
// Ветка удаление переживает — она в bare-клоне, — поэтому оговорки ровно две,
// и обе про то, что пережить его не может:
//
//   - незакоммиченное: другого места у него нет;
//   - живая аренда: в этой папке прямо сейчас работает агент, она смонтирована
//     в его песочницу.
func removable(entry workspace.Entry, task tracker.Task, now time.Time, force bool) error {
	if force {
		return nil
	}
	if entry.Dirty > 0 {
		return fmt.Errorf("в %s незакоммичено: %d путей. Другого места у этой работы нет — "+
			"закоммить её или сноси с --force", entry.Key, entry.Dirty)
	}
	if task.LeaseAlive(now) {
		return fmt.Errorf("над %s работает прогон %s (аренда до %s): папка смонтирована в его песочницу. "+
			"Дождись конца прогона или сноси с --force",
			entry.Key, short(task.RunID), task.LeaseUntil.Format(time.RFC3339))
	}
	return nil
}

// state — человеческое описание того, чем папка занята.
func state(entry workspace.Entry) string {
	switch {
	case entry.Missing:
		return "каталог потерян, запись жива"
	case entry.Dirty > 0:
		return fmt.Sprintf("незакоммичено: %d", entry.Dirty)
	default:
		return "чисто"
	}
}

// size — размер в человеческих единицах: точные байты тут никому не нужны.
func size(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	value, units := float64(bytes)/unit, []string{"KiB", "MiB", "GiB", "TiB"}
	for _, name := range units {
		if value < unit || name == units[len(units)-1] {
			return fmt.Sprintf("%.1f %s", value, name)
		}
		value /= unit
	}
	return ""
}

// short — первые восемь символов идентификатора, как в маркерах.
func short(id string) string {
	runes := []rune(id)
	if len(runes) <= 8 {
		return id
	}
	return string(runes[:8])
}
