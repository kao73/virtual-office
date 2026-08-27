package claude

import (
	"bufio"
	"io"
	"strings"

	"github.com/kao73/virtual-office/internal/runner"
)

// endReasons — словарь конца прогона у Claude Code в переводе на нейтральное.
//
// Значения сняты с живых логов, а не вычитаны: по 116 прогонам архива этапов
// 3–4 встретились ровно три — `completed` (112), `api_error` (2) и `max_turns`
// (2). Незнакомого значения в переводе нет намеренно, как и у окна поставщика:
// словарь принадлежит поставщику, пополнить его он может без нас, и тогда
// раннер обязан сказать «не понял», а не выдать чужое слово за успех.
var endReasons = map[string]runner.EndReason{
	"completed": runner.EndCompleted,
	"max_turns": runner.EndMaxTurns,
	"api_error": runner.EndError,
}

// maxEndDetail — сколько итоговой строки агента уносить в тикет.
//
// Строка эта человеческая и бывает любой длины, а стоять ей в записи раннера
// рядом с объяснением, а не вместо него. Предел — компромисс, и он **режет**:
// у четырёх прогонов архива, не дошедших до результата, первые строки итога
// вышли в 48, 79, 107 и 340 рун, и последняя обрывается многоточием. Так
// и задумано — отказ агента бывает многословным, а тикет читает человек;
// полный текст всегда лежит в логе прогона.
//
// Число выбрано по этому же замеру, а не на глаз: три образца из четырёх входят
// целиком, у четвёртого видно первые 200 рун из 340. Правя его, сперва померьте
// заново — этот комментарий трижды врал именно потому, что длины не мерили.
const maxEndDetail = 200

// ParseEnding вытаскивает из лога прогона то, что агент сказал о его конце.
//
// Ошибок не возвращает по той же причине, что ParseUsage и ParseLimit: лог —
// свидетельство о прогоне, а не контракт. Нет итогового события — конец
// неизвестен, и это законный ответ: прогон, убитый на середине, о своём конце
// не отчитывается.
//
// Ответ этот — подсказка, а не приговор. Решает раннер по следу работы: агент,
// сказавший `api_error`, мог не сделать ни шага, а мог сломаться на двадцать
// пятом — цена у этих случаев разная, и по событию они неразличимы.
//
// Берётся последнее итоговое событие: их в логе ровно одно, но встретив два,
// правдой считаем то, что ближе к концу прогона.
func ParseEnding(r io.Reader) runner.Ending {
	var ending runner.Ending

	for lines := bufio.NewReader(r); ; {
		line, err := readLine(lines)
		// Хвост без перевода строки — обычное дело у убитого прогона:
		// разобрать его всё равно надо, а уже потом останавливаться.
		if event, found := parseResultEvent(line); found {
			ending = endingOf(event)
		}
		if err != nil {
			return ending
		}
	}
}

// endingOf переводит итоговое событие в наблюдение раннера.
func endingOf(event resultEvent) runner.Ending {
	ending := runner.Ending{
		Provider: event.TerminalReason,
		Detail:   trimDetail(event.Text),
	}

	switch reason, known := endReasons[event.TerminalReason]; {
	case known:
		ending.Reason = reason
	case event.IsError:
		// Незнакомое слово при явной беде — всё-таки беда. Гадать, какая
		// именно, раннер не станет: след работы разберётся, а слово останется
		// в Provider, чтобы человек увидел, чего мы не знаем.
		ending.Reason = runner.EndError
	}
	// У знакомого успеха объяснять нечего: итоговая строка удачного прогона —
	// это его отчёт, и тащить его в тикет второй раз незачем.
	if ending.Reason == runner.EndCompleted {
		ending.Detail = ""
	}
	return ending
}

// trimDetail оставляет от итоговой строки агента первую строку и не длиннее
// maxEndDetail: в тикет она едет рядом с объяснением раннера, а не вместо него.
func trimDetail(text string) string {
	detail, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	detail = strings.TrimSpace(detail)
	if len(detail) <= maxEndDetail {
		return detail
	}
	// Режем по рунам, а не по байтам: строка человеческая и бывает русской.
	runes := []rune(detail)
	if len(runes) <= maxEndDetail {
		return detail
	}
	return strings.TrimSpace(string(runes[:maxEndDetail])) + "…"
}
