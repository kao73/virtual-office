package claude

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/kao73/virtual-office/runner"
)

// rateLimitEventType — событие о пределах подписки в потоке `--output-format stream-json`.
const rateLimitEventType = "rate_limit_event"

// limitStates — перечисление поставщика в переводе на нейтральное.
//
// Значения не выдуманы: `allowed` снят с живых логов (он стоит во всех сорока
// прогонах архива), остальные два — из самого CLI, где перечисление задано
// целиком. Незнакомого значения здесь нет намеренно: список принадлежит
// поставщику, и пополнить его он может без нас — тогда состояние останется
// неразобранным, и раннер об этом скажет.
var limitStates = map[string]runner.LimitState{
	"allowed":         runner.LimitAllowed,
	"allowed_warning": runner.LimitNearing,
	"rejected":        runner.LimitReached,
}

// rateLimitEvent — событие о пределах. Важно ровно одно поле: `status` внутри
// `rate_limit_info`.
//
// Рядом с ним лежит `overageStatus`, и он говорит о другом — о доплате сверх
// подписки, выключенной на уровне организации. В логе успешного прогона он
// равен "rejected", поэтому спутать их значит объявить отвергнутым каждый прогон.
type rateLimitEvent struct {
	Type string `json:"type"`
	Info struct {
		Status   string `json:"status"`
		ResetsAt int64  `json:"resetsAt"`
		Window   string `json:"rateLimitType"`
	} `json:"rate_limit_info"`
}

// ParseLimit вытаскивает состояние окна поставщика из лога прогона.
//
// Ошибок не возвращает по той же причине, что и ParseUsage: лог — свидетельство
// о прогоне, а не контракт. Нет события — состояние неизвестно, и это законный
// ответ: событие приходит только после первого ответа API.
//
// Берётся последнее событие: правдой считается то, что ближе к концу прогона.
func ParseLimit(r io.Reader) runner.Limit {
	var limit runner.Limit

	for lines := bufio.NewReader(r); ; {
		line, err := readLine(lines)
		if event, found := parseRateLimitEvent(line); found {
			limit = runner.Limit{
				State:    limitStates[event.Info.Status],
				Window:   event.Info.Window,
				ResetsAt: resetTime(event.Info.ResetsAt),
			}
			// Название состояния сохраняется всегда, но показывается только
			// тогда, когда раннер его не понял: понятое он пересказывает сам.
			if limit.State == runner.LimitUnknown {
				limit.Provider = event.Info.Status
			}
		}
		if err != nil {
			return limit
		}
	}
}

// parseRateLimitEvent разбирает строку лога, если это событие о пределах.
// Дешёвая проверка на подстроку идёт первой по той же причине, что и у итогового
// события: разбирать в JSON каждую строку лога ради её типа незачем.
func parseRateLimitEvent(line string) (rateLimitEvent, bool) {
	if !strings.Contains(line, `"`+rateLimitEventType+`"`) {
		return rateLimitEvent{}, false
	}
	var event rateLimitEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil || event.Type != rateLimitEventType {
		return rateLimitEvent{}, false
	}
	return event, true
}

// resetTime переводит секунды эпохи в время. Ноль означает, что поставщик
// о сбросе не сказал, — нулевое время честнее 1970 года.
func resetTime(epoch int64) time.Time {
	if epoch == 0 {
		return time.Time{}
	}
	return time.Unix(epoch, 0)
}
