package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/kao73/virtual-office/internal/tracker"
	"github.com/kao73/virtual-office/internal/tracker/mock"
)

// mockUsage — подсказка по подкоманде. Ею пользуется человек, гоняющий сценарии
// руками, поэтому важнее полнота, чем краткость.
const mockUsage = `runner mock — файловый трекер для ручных сценариев

  runner mock add <KEY> --summary <тема> [--description <текст>] [--status <статус>]
                        [--project <ключ>] [--labels a,b]
  runner mock ls [--project <ключ>] [--status <статус>]
  runner mock show <KEY>
  runner mock move <KEY> <статус>
  runner mock comment <KEY> [--author <учётка>] <текст>

Хранилище — ${OFFICE_HOME:-~/.office}/mock.`

// mockCommand исполняет подкоманду mock. Вывод идёт в out, чтобы команду можно
// было проверить тестом, а не глазами.
func mockCommand(tr *mock.Tracker, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(mockUsage)
	}

	switch args[0] {
	case "add":
		return mockAdd(tr, args[1:], out)
	case "ls":
		return mockList(tr, args[1:], out)
	case "show":
		return mockShow(tr, args[1:], out)
	case "move":
		return mockMove(tr, args[1:], out)
	case "comment":
		return mockComment(tr, args[1:], out)
	default:
		return fmt.Errorf("неизвестная подкоманда %q\n\n%s", args[0], mockUsage)
	}
}

func mockAdd(tr *mock.Tracker, args []string, out io.Writer) error {
	fs := flags("add")
	summary := fs.String("summary", "", "тема задачи")
	description := fs.String("description", "", "постановка")
	status := fs.String("status", "Ready", "статус")
	project := fs.String("project", "", "ключ проекта; по умолчанию — часть ключа задачи до дефиса")
	labels := fs.String("labels", "", "метки через запятую")

	key, err := parseKeyFlags(fs, args)
	if err != nil {
		return err
	}
	if *summary == "" {
		return errors.New("нужна --summary: задача без темы не читается ни человеком, ни агентом")
	}
	if *project == "" {
		*project, _, _ = strings.Cut(key, "-")
	}

	task := tracker.Task{
		Key: key, Project: *project, Summary: *summary,
		Description: *description, Status: *status,
	}
	if *labels != "" {
		task.Labels = strings.Split(*labels, ",")
	}
	if err := tr.Add(task); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s заведена в %s\n", key, *status)
	return nil
}

func mockList(tr *mock.Tracker, args []string, out io.Writer) error {
	fs := flags("ls")
	project := fs.String("project", "", "показывать только этот проект")
	status := fs.String("status", "", "показывать только этот статус")
	if err := fs.Parse(args); err != nil {
		return err
	}

	keys, err := tr.Keys()
	if err != nil {
		return err
	}

	now := time.Now()
	shown := 0
	for _, key := range keys {
		task, err := tr.Get(key)
		if err != nil {
			return err
		}
		if (*project != "" && task.Project != *project) || (*status != "" && task.Status != *status) {
			continue
		}

		lease := "—"
		if task.LeaseAlive(now) {
			lease = fmt.Sprintf("%s до %s", task.Owner, task.LeaseUntil.Local().Format("15:04:05"))
		} else if task.RunID != "" {
			lease = "истекла"
		}
		flag := ""
		if task.HumanFlag {
			flag = " [ждёт человека]"
		}
		fmt.Fprintf(out, "%-10s %-12s попыток:%d  аренда:%-22s %s%s\n",
			task.Key, task.Status, task.Attempts, lease, task.Summary, flag)
		shown++
	}
	if shown == 0 {
		fmt.Fprintln(out, "задач нет")
	}
	return nil
}

func mockShow(tr *mock.Tracker, args []string, out io.Writer) error {
	fs := flags("show")
	key, err := parseKeyFlags(fs, args)
	if err != nil {
		return err
	}

	task, err := tr.Get(key)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "%s  %s\n%s\n", task.Key, task.Status, task.Summary)
	if task.Description != "" {
		fmt.Fprintf(out, "\n%s\n", task.Description)
	}
	fmt.Fprintf(out, "\nпроект:   %s\nпопыток:  %d\nждёт человека: %v\n", task.Project, task.Attempts, task.HumanFlag)
	if len(task.Labels) > 0 {
		fmt.Fprintf(out, "метки:    %s\n", strings.Join(task.Labels, ", "))
	}
	if task.RunID != "" {
		fmt.Fprintf(out, "аренда:   %s (%s) до %s\n", task.Owner, task.RunID, task.LeaseUntil.Local().Format(time.RFC3339))
	}

	if len(task.Comments) > 0 {
		fmt.Fprintf(out, "\n== комментарии (%d) ==\n", len(task.Comments))
	}
	for _, c := range task.Comments {
		fmt.Fprintf(out, "\n--- %s, %s, %s\n%s\n", c.ID, c.Author, c.Created.Local().Format(time.RFC3339), c.Body)
	}
	return nil
}

// mockMove — человеческий перевод задачи: то, что в JIRA делается мышкой.
// Без него на файловом трекере нечем сказать «берите в работу», а это первый шаг
// любого сценария: задача попадает в очередь роли рукой человека, а не сама.
func mockMove(tr *mock.Tracker, args []string, out io.Writer) error {
	fs := flags("move")
	key, err := parseKeyFlags(fs, args)
	if err != nil {
		return err
	}
	status := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if status == "" {
		return errors.New("нужен статус вторым аргументом: runner mock move <KEY> <статус>")
	}
	if err := tr.Move(key, status); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s переведена в %s\n", key, status)
	return nil
}

func mockComment(tr *mock.Tracker, args []string, out io.Writer) error {
	fs := flags("comment")
	// По умолчанию пишет человек: голос офиса в трекер кладёт раннер, а не оператор.
	author := fs.String("author", "human", "учётка автора")

	key, err := parseKeyFlags(fs, args)
	if err != nil {
		return err
	}
	body := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if body == "" {
		return errors.New("нечего писать: текст комментария идёт после ключа задачи")
	}
	if err := tr.AddComment(key, *author, body); err != nil {
		return err
	}
	fmt.Fprintf(out, "комментарий добавлен к %s от %s\n", key, *author)
	return nil
}

// flags — набор флагов, который не печатает ничего сам: сообщения об ошибках
// собирает вызывающий, и в тесте они видны целиком.
func flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// parseKeyFlags разбирает аргументы, ожидая ключ задачи первым позиционным.
// Флаги идут после ключа: `mock add OFF-1 --summary ...`.
func parseKeyFlags(fs *flag.FlagSet, args []string) (string, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return "", fmt.Errorf("нужен ключ задачи первым аргументом\n\n%s", mockUsage)
	}
	if err := fs.Parse(args[1:]); err != nil {
		return "", err
	}
	return args[0], nil
}
