package ledger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kao73/virtual-office/runner"
)

var day = time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)

func entry(task, role string, cost float64, at time.Time) Entry {
	return Entry{
		RunID: "прогон-" + task, Task: task, Role: role, Project: "OFF",
		Started: at, Usage: runner.Usage{CostUSD: cost, DurationMS: 1000, Turns: 3},
		Outcome: "done", ConfigSHA: "5bc6a3b0",
	}
}

func newLedger(t *testing.T) *Ledger {
	t.Helper()
	return New(filepath.Join(t.TempDir(), FileName))
}

func write(t *testing.T, l *Ledger, entries ...Entry) {
	t.Helper()
	for _, e := range entries {
		if err := l.Append(e); err != nil {
			t.Fatalf("строка не записана: %v", err)
		}
	}
}

// Реестр — журнал, а не база: строка на прогон, дописывается в конец и никогда
// не переписывается. Читается он весь и разбирается построчно.
func TestLedgerAppendsLinePerRun(t *testing.T) {
	l := newLedger(t)
	write(t, l, entry("OFF-1", "implementer", 0.25, day), entry("OFF-2", "reviewer", 0.10, day))

	raw, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatalf("реестр не прочитан: %v", err)
	}
	if lines := strings.Count(string(raw), "\n"); lines != 2 {
		t.Errorf("строк в реестре %d, ожидалось 2:\n%s", lines, raw)
	}

	total, err := l.Sum(Filter{})
	if err != nil {
		t.Fatalf("сводка не собрана: %v", err)
	}
	if total.Runs != 2 || total.CostUSD != 0.35 {
		t.Errorf("сводка %+v, ожидалось 2 прогона на $0.35", total)
	}
	if total.ByOutcome["done"] != 2 {
		t.Errorf("исходы посчитаны как %v", total.ByOutcome)
	}
}

// Реестра может не быть вовсе — на машине, где ещё не было ни одного прогона.
// Это пустая сводка, а не ошибка: иначе первый же `runner ledger` пугал бы
// человека сломанным хозяйством.
func TestLedgerSumsToZeroWhenMissing(t *testing.T) {
	total, err := New(filepath.Join(t.TempDir(), FileName)).Sum(Filter{})
	if err != nil {
		t.Fatalf("пустой реестр не прочитан: %v", err)
	}
	if total.Runs != 0 || total.CostUSD != 0 {
		t.Errorf("у несуществующего реестра нашлись прогоны: %+v", total)
	}
}

// Лимиты спрашивают у реестра разное: сколько стоила задача за всё время
// и сколько роль потратила за сутки.
func TestLedgerFiltersByTaskRoleAndTime(t *testing.T) {
	l := newLedger(t)
	write(t, l,
		entry("OFF-1", "implementer", 0.20, day.Add(-48*time.Hour)),
		entry("OFF-1", "reviewer", 0.10, day),
		entry("OFF-2", "implementer", 0.50, day),
	)

	cases := []struct {
		name   string
		filter Filter
		runs   int
		cost   float64
	}{
		{"задача за всё время", Filter{Task: "OFF-1"}, 2, 0.30},
		{"роль за всё время", Filter{Role: "implementer"}, 2, 0.70},
		{"роль за сутки", Filter{Role: "implementer", Since: day.Add(-24 * time.Hour)}, 1, 0.50},
		{"всё за сутки", Filter{Since: day.Add(-24 * time.Hour)}, 2, 0.60},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			total, err := l.Sum(tc.filter)
			if err != nil {
				t.Fatalf("сводка не собрана: %v", err)
			}
			if total.Runs != tc.runs || !closeEnough(total.CostUSD, tc.cost) {
				t.Errorf("сводка %+v, ожидалось %d прогонов на $%.2f", total, tc.runs, tc.cost)
			}
		})
	}
}

// Прогон, убитый на середине, цены не называет. Считать её нулём нельзя — так
// дорогая задача выглядела бы бесплатной, — поэтому такие прогоны в сводке видны
// отдельной строкой.
func TestLedgerCountsRunsWithUnknownCost(t *testing.T) {
	l := newLedger(t)
	silent := entry("OFF-1", "implementer", 0, day)
	silent.Usage = runner.Usage{}
	write(t, l, entry("OFF-1", "implementer", 0.20, day), silent)

	total, err := l.Sum(Filter{})
	if err != nil {
		t.Fatalf("сводка не собрана: %v", err)
	}
	if total.Runs != 2 || total.CostUSD != 0.20 {
		t.Errorf("сводка %+v, ожидалось 2 прогона на $0.20", total)
	}
	if total.Unknown != 1 {
		t.Errorf("прогонов без цены %d, ожидался 1", total.Unknown)
	}
}

// Реестр дописывают на живой машине, и последняя строка бывает оборванной:
// процесс убили посреди записи. Одна такая строка не вправе останавливать учёт
// целиком — но и молчать о ней нельзя: сумма после неё занижена, а на суммы
// смотрят лимиты.
func TestLedgerReportsBrokenLines(t *testing.T) {
	l := newLedger(t)
	write(t, l, entry("OFF-1", "implementer", 0.20, day))
	if err := os.WriteFile(l.Path, append(mustRead(t, l.Path), []byte(`{"run_id":"обрыв`+"\n")...), 0o644); err != nil {
		t.Fatalf("реестр не испорчен: %v", err)
	}

	total, err := l.Sum(Filter{})
	if err != nil {
		t.Fatalf("сводка не собрана: %v", err)
	}
	if total.Runs != 1 || total.CostUSD != 0.20 {
		t.Errorf("сводка %+v, ожидался один разобранный прогон", total)
	}
	if total.Broken != 1 {
		t.Errorf("нечитаемых строк %d, ожидалась 1", total.Broken)
	}
}

// Раннеров на машине бывает несколько, а реестр у них один. Строки не должны
// ни теряться, ни разрубать друг друга пополам.
func TestLedgerSurvivesConcurrentAppends(t *testing.T) {
	l := newLedger(t)

	const writers = 16
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := l.Append(entry(fmt.Sprintf("OFF-%d", i), "implementer", 0.10, day)); err != nil {
				t.Errorf("строка не записана: %v", err)
			}
		}()
	}
	wg.Wait()

	total, err := l.Sum(Filter{})
	if err != nil {
		t.Fatalf("сводка не собрана: %v", err)
	}
	if total.Runs != writers || total.Broken != 0 {
		t.Errorf("сводка %+v, ожидалось %d целых строк", total, writers)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("файл не прочитан: %v", err)
	}
	return raw
}

// Сложение дробей даёт хвост в последнем знаке; в деньгах он значения не имеет.
func closeEnough(got, want float64) bool { return got-want < 1e-9 && want-got < 1e-9 }
