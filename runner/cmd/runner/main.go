// Command runner — детерминированная обвязка вокруг агента: берёт задачу
// из трекера, готовит рабочую папку, зовёт агента, разбирает исход и двигает
// задачу по графу.
//
// Никакого LLM внутри: всё, что раннер «решает», решается по workflow.yaml
// и result.json (DESIGN.md §2.1).
//
// Сейчас реализована только подкоманда mock — файловый трекер для ручных
// сценариев. tick, loop и reap появятся на шаге 4.
package main

import (
	"fmt"
	"os"

	"github.com/kao73/virtual-office/tracker/mock"
)

const usage = `runner — обвязка вокруг агента

  runner mock <add|ls|show|comment> …   файловый трекер для ручных сценариев

Хозяйство раннера — ${OFFICE_HOME:-~/.office}.`

func main() {
	if err := execute(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "runner:", err)
		os.Exit(2)
	}
}

func execute(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("нужна подкоманда\n\n%s", usage)
	}

	switch args[0] {
	case "mock":
		tracker, err := mock.Default()
		if err != nil {
			return err
		}
		return mockCommand(tracker, args[1:], os.Stdout)
	default:
		return fmt.Errorf("неизвестная подкоманда %q\n\n%s", args[0], usage)
	}
}
