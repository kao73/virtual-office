package claude

import (
	"strings"
	"testing"
	"time"

	"github.com/kao73/virtual-office/internal/runner"
)

// allowedEvent — строка из настоящего лога прогона, слово в слово.
//
// В ней два поля со словом «rejected»: `status` говорит об этом прогоне,
// а `overageStatus` — о доплате сверх подписки, которая в организации выключена.
// Прогон при этом прошёл до конца. Перепутать их — значит объявить отвергнутым
// каждый прогон офиса: событие есть во всех сорока логах архива, и во всех оно
// говорит `status: allowed`.
const allowedEvent = `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed",` +
	`"resetsAt":1787008800,"rateLimitType":"five_hour","overageStatus":"rejected",` +
	`"overageDisabledReason":"org_level_disabled","isUsingOverage":false},` +
	`"uuid":"728ef2b9-e3e0-49d1-9959-bdcb18b997b7","session_id":"1d5f4c18"}`

// Обычный прогон: окно открыто, и сказать человеку нечего.
func TestParseLimitReadsOpenWindow(t *testing.T) {
	limit := ParseLimit(strings.NewReader(allowedEvent + "\n"))

	if limit.State != runner.LimitAllowed {
		t.Errorf("состояние %q, ожидалось %q — в логе status:allowed", limit.State, runner.LimitAllowed)
	}
	if limit.Window != "five_hour" {
		t.Errorf("окно %q, ожидалось five_hour", limit.Window)
	}
	if want := time.Unix(1787008800, 0); !limit.ResetsAt.Equal(want) {
		t.Errorf("сброс %s, ожидался %s", limit.ResetsAt, want)
	}
	if notice := limit.Notice(); notice != "" {
		t.Errorf("открытое окно вызвало строку в логе: %s", notice)
	}
}

// Отвергнутый прогон — то, ради чего разбор и заводится: агент не начинал
// работу, и человеку это надо назвать словами.
func TestParseLimitReadsExhaustedWindow(t *testing.T) {
	line := `{"type":"rate_limit_event","rate_limit_info":{"status":"rejected",` +
		`"resetsAt":1787008800,"rateLimitType":"five_hour","overageStatus":"rejected"}}`

	limit := ParseLimit(strings.NewReader(line + "\n"))

	if limit.State != runner.LimitReached {
		t.Fatalf("состояние %q, ожидалось %q", limit.State, runner.LimitReached)
	}
	notice := limit.Notice()
	for _, want := range []string{"five_hour", "не провал агента"} {
		if !strings.Contains(notice, want) {
			t.Errorf("в строке нет %q: %s", want, notice)
		}
	}
}

// Окно на исходе — предупреждение, а не отказ: прогон прошёл, но следующему
// места может не хватить.
func TestParseLimitReadsNearingWindow(t *testing.T) {
	line := `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed_warning",` +
		`"resetsAt":1787008800,"rateLimitType":"seven_day"}}`

	limit := ParseLimit(strings.NewReader(line + "\n"))

	if limit.State != runner.LimitNearing {
		t.Fatalf("состояние %q, ожидалось %q", limit.State, runner.LimitNearing)
	}
	if !strings.Contains(limit.Notice(), "seven_day") {
		t.Errorf("в строке нет имени окна: %s", limit.Notice())
	}
}

// Незнакомое состояние не проглатывается: перечисление принадлежит поставщику,
// он вправе его пополнить, и раннер, промолчавший о новом значении, сообщил бы
// человеку, что всё в порядке, ничего об этом не зная.
func TestParseLimitShowsUnknownState(t *testing.T) {
	line := `{"type":"rate_limit_event","rate_limit_info":{"status":"throttled","rateLimitType":"five_hour"}}`

	limit := ParseLimit(strings.NewReader(line + "\n"))

	if limit.State != runner.LimitUnknown {
		t.Errorf("незнакомое состояние выдано за понятое: %q", limit.State)
	}
	if !strings.Contains(limit.Notice(), "throttled") {
		t.Errorf("в строке нет того, что сказал поставщик: %s", limit.Notice())
	}
}

// Лог без события о пределах — обычное дело: событие приходит только после
// первого ответа API, и прогон, не доживший до него, о пределах не скажет.
// Молчание тогда законно.
func TestParseLimitSilentWithoutEvent(t *testing.T) {
	log := `{"type":"system","subtype":"init"}` + "\n" +
		`{"type":"result","subtype":"success","total_cost_usd":0.21}` + "\n"

	limit := ParseLimit(strings.NewReader(log))

	if limit.State != runner.LimitUnknown || limit.Notice() != "" {
		t.Errorf("из лога без события вычитано состояние %q: %s", limit.State, limit.Notice())
	}
}

// Событий в логе может быть несколько: правдой считается последнее — то,
// что ближе к концу прогона.
func TestParseLimitTakesLastEvent(t *testing.T) {
	last := `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed_warning",` +
		`"resetsAt":1787008800,"rateLimitType":"five_hour"}}`

	limit := ParseLimit(strings.NewReader(allowedEvent + "\n" + last + "\n"))

	if limit.State != runner.LimitNearing {
		t.Errorf("состояние %q, ожидалось %q — правдой считается последнее событие", limit.State, runner.LimitNearing)
	}
}
