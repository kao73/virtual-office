package claude

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/kao73/virtual-office/runner"
)

// resultEventType — тип итогового события в потоке `--output-format stream-json`.
// Оно приходит последним и несёт стоимость, длительность и число шагов.
const resultEventType = "result"

// resultEvent — итоговое событие. Полей в нём куда больше, но раннеру нужны эти:
// остальное — устройство конкретного агента, и наверх оно не идёт.
type resultEvent struct {
	Type       string  `json:"type"`
	CostUSD    float64 `json:"total_cost_usd"`
	DurationMS int     `json:"duration_ms"`
	Turns      int     `json:"num_turns"`
}

// ParseUsage вытаскивает расход прогона из его лога.
//
// Ошибок не возвращает вовсе, и это по существу: лог — свидетельство о прогоне,
// а не контракт. Оборванная строка, мусор от бэкенда, отсутствие итогового
// события — всё это значит одно и то же: расход неизвестен. Ронять из-за этого
// прогон, который уже состоялся, было бы обменом сделанной работы на строчку
// в учёте.
//
// Берётся последнее итоговое событие: их в логе ровно одно, но встретив два,
// правдой считаем то, что ближе к концу прогона.
func ParseUsage(r io.Reader) runner.Usage {
	var usage runner.Usage

	for lines := bufio.NewReader(r); ; {
		line, err := readLine(lines)
		// Хвост без перевода строки — обычное дело у убитого прогона:
		// разобрать его всё равно надо, а уже потом останавливаться.
		if event, found := parseResultEvent(line); found {
			usage = runner.Usage{CostUSD: event.CostUSD, DurationMS: event.DurationMS, Turns: event.Turns}
		}
		if err != nil {
			return usage
		}
	}
}

// parseResultEvent разбирает строку лога, если это итоговое событие.
//
// Дешёвая проверка на подстроку идёт первой не ради скорости, а ради памяти:
// событие с телом файла весит мегабайты, и разбирать каждое такое в JSON, чтобы
// узнать его тип, незачем. Совпадение подстроки ничего не доказывает — агент
// может обсуждать формат в своей же реплике, — поэтому тип всё равно сверяется
// после разбора.
func parseResultEvent(line string) (resultEvent, bool) {
	if !strings.Contains(line, `"`+resultEventType+`"`) {
		return resultEvent{}, false
	}
	var event resultEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil || event.Type != resultEventType {
		return resultEvent{}, false
	}
	return event, true
}

// readLine читает строку целиком, какой бы длинной она ни была.
//
// Готового сканера здесь нет намеренно: у него предел строки 64 КиБ, а событие
// с содержимым файла легко больше. Сканер на таком логе останавливается, и всё,
// что после длинной строки, — включая итог прогона — пропадает молча.
func readLine(r *bufio.Reader) (string, error) {
	var b strings.Builder
	for {
		chunk, more, err := r.ReadLine()
		b.Write(chunk)
		switch {
		case err != nil:
			// io.EOF — конец лога, а не беда: строка перед ним могла быть целой.
			if errors.Is(err, io.EOF) {
				return b.String(), io.EOF
			}
			return b.String(), err
		case !more:
			return b.String(), nil
		}
	}
}
