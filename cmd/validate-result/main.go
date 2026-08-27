// Command validate-result — детерминированные проверки прогона, которые агент
// зовёт сам: ограждения на событии Stop.
//
// Режима два. Без подкоманды проверяется файл результата по контракту
// «раннер ↔ агент». С подкомандой `guard <имя>` исполняется ограждение роли:
// параметры прогона оно берёт из окружения (переменные `OFFICE_*`), потому что
// аргументы задаёт адаптер, а адаптеров будет больше одного.
//
// Кодов выхода ровно два: 0 — прошло, 2 — любая беда. Двойка не случайна:
// Claude Code считает блокирующей только её, а всякий другой код — неблокирующей
// ошибкой, после которой агент спокойно завершается. Поэтому и «файла нет»,
// и «неверный вызов» — тоже 2.
//
// Разбор и проверки здесь не свои: это тот же код, которым раннер судит прогон
// после его конца. Второй парсер разъехался бы с первым — так уже было, когда
// ограждение проверяло лишь наличие поля outcome.
package main

import (
	"fmt"
	"os"

	"github.com/kao73/virtual-office/internal/guard"
	"github.com/kao73/virtual-office/internal/runner"
)

// guardCmd — подкоманда ограждения роли.
const guardCmd = "guard"

func main() {
	args := os.Args[1:]

	if len(args) > 0 && args[0] == guardCmd {
		if len(args) != 2 {
			die("validate-result " + guardCmd + ": нужно ровно одно имя ограждения")
		}
		if err := guard.Check(args[1], guard.EnvFromOS()); err != nil {
			die(err.Error())
		}
		return
	}

	if len(args) != 1 {
		die("validate-result: нужен ровно один аргумент — путь к файлу результата, " +
			"либо `" + guardCmd + " <имя>` для ограждения роли")
	}
	if _, err := runner.ReadResultFile(args[0]); err != nil {
		die(err.Error() + "\n" + runner.ResultAdvice)
	}
}

// die пишет причину в stderr — оттуда её читает агент — и блокирует завершение.
func die(reason string) {
	fmt.Fprintln(os.Stderr, reason)
	os.Exit(2)
}
