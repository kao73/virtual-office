package runagent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kao73/virtual-office/internal/runner"
)

// Расход прогона читается из его лога — единственного места, где агент о нём
// сказал. Разбирает язык агента адаптер, здесь проверяется только то, что лог
// вообще открывают.
func TestUsageOfReadsRunLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.log")
	line := `{"type":"result","subtype":"success","num_turns":7,"total_cost_usd":0.5,"duration_ms":1234}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
		t.Fatalf("лог не записан: %v", err)
	}

	usage := usageOf(path)
	if usage.CostUSD != 0.5 || usage.DurationMS != 1234 || usage.Turns != 7 {
		t.Errorf("расход разобран как %+v", usage)
	}
}

// Лога может не быть вовсе: бэкенд не встал, каталог снесли, прогон умер до
// первой строки. Учёт от этого не ломается — он просто не знает цены.
func TestUsageOfSurvivesMissingLog(t *testing.T) {
	if usage := usageOf(filepath.Join(t.TempDir(), "нет-такого.log")); usage.Known() {
		t.Errorf("у прогона без лога нашлась стоимость: %+v", usage)
	}
}

// Состояние окна поставщика читается оттуда же, откуда расход, — из лога
// прогона. Разбирает его адаптер, здесь проверяется только то, что лог открывают
// и за этим тоже.
func TestLimitOfReadsRunLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.log")
	line := `{"type":"rate_limit_event","rate_limit_info":{"status":"rejected",` +
		`"resetsAt":1787008800,"rateLimitType":"five_hour"}}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
		t.Fatalf("лог не записан: %v", err)
	}

	if limit := limitOf(path); limit.State != runner.LimitReached {
		t.Errorf("состояние окна разобрано как %+v", limit)
	}
}

// Лога может не быть вовсе — тогда о пределах не известно ничего, и это
// законный ответ, а не повод падать.
func TestLimitOfSurvivesMissingLog(t *testing.T) {
	if limit := limitOf(filepath.Join(t.TempDir(), "нет-такого.log")); limit.State != runner.LimitUnknown {
		t.Errorf("у прогона без лога нашлось состояние окна: %+v", limit)
	}
}
