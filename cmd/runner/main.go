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

	"github.com/kao73/virtual-office/internal/tracker/mock"
)

const usage = `runner — обвязка вокруг агента

  runner tick [--role R]        один цикл: взять не больше одной задачи и вернуть в граф
  runner loop [--every 2m]      то же по расписанию, пока не остановят
  runner reap                   вернуть задачи с истёкшей арендой
  runner complete-splits        достроить и связать тикеты-детей подтверждённого split
  runner ls [--project P]       доска: где какая задача, кто её взял и сколько висит
  runner ledger [--since 24h]   расход: сколько прогонов и на сколько денег
  runner worktree <ls|rm> …     рабочие папки задач: что лежит и как убрать
  runner init                   завести ${OFFICE_HOME} и положить образцы projects.local.example.yaml и tracker.example.yaml
  runner version                что установлено: личность раннера и каталог его офиса
  runner mock <add|ls|show|comment> …   файловый трекер для ручных сценариев

Общий флаг: --backend (sbx или local). Трекеры берутся из проектов: по офису на каждый.
Хозяйство раннера — ${OFFICE_HOME:-~/.office}; офис — из поставки бинарника,
распакованной в ${OFFICE_HOME}/office/<версия>/. OFFICE_CONFIG_ROOT переключает
на клон репозитория (так работают обёртки bin/*).`

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
	case "complete-splits":
		return completeSplitsCommand(args[1:], os.Stdout)
	case "ls":
		return boardCommand(args[1:], os.Stdout)
	case "ledger":
		return ledgerCommand(args[1:], os.Stdout)
	case "worktree":
		return worktreeCommand(args[1:], os.Stdout)
	case "init":
		return initCommand(args[1:], os.Stdout)
	case "version":
		return versionCommand(args[1:], os.Stdout)
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
// останавливают цикл. Тот же контекст доходит до процесса агента: идущий
// прогон прерывается, задачу вернёт reap (см. loopCommand).
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
}
