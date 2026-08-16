// Command runner — детерминированная обвязка вокруг агента: берёт задачу
// из трекера, готовит рабочую папку, зовёт агента, разбирает исход и двигает
// задачу по графу.
//
// Никакого LLM внутри: всё, что раннер «решает», решается по workflow.yaml
// и result.json (DESIGN.md §2.1).
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/kao73/virtual-office/tracker/mock"
)

const usage = `runner — обвязка вокруг агента

  runner tick [--role R]        один цикл: взять не больше одной задачи и вернуть в граф
  runner loop [--every 2m]      то же по расписанию, пока не остановят
  runner reap                   вернуть задачи с истёкшей арендой
  runner mock <add|ls|show|comment> …   файловый трекер для ручных сценариев

Общие флаги: --tracker (mock), --backend (sbx или local).
Хозяйство раннера — ${OFFICE_HOME:-~/.office}.`

// Коды возврата те же, что у run-agent: 0 — цикл прошёл, в том числе когда
// работы не нашлось; 2 — инфраструктурная беда, дальше без человека никак.
const exitInfra = 2

func main() {
	if err := execute(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "runner:", err)
		os.Exit(exitInfra)
	}
}

func execute(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("нужна подкоманда\n\n%s", usage)
	}

	switch args[0] {
	case "tick":
		return tickCommand(args[1:], os.Stdout)
	case "loop":
		return loopCommand(args[1:], os.Stdout)
	case "reap":
		return reapCommand(args[1:], os.Stdout)
	case "mock":
		tasks, err := mock.Default()
		if err != nil {
			return err
		}
		return mockCommand(tasks, args[1:], os.Stdout)
	default:
		return fmt.Errorf("неизвестная подкоманда %q\n\n%s", args[0], usage)
	}
}

// signalContext отменяется по SIGINT или SIGTERM — так cron, launchd и systemd
// останавливают цикл, не убивая идущий прогон посреди работы.
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
}
