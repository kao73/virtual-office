package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kao73/virtual-office/ledger"
	"github.com/kao73/virtual-office/runner"
)

var ledgerNow = time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)

func filled(t *testing.T) *ledger.Ledger {
	t.Helper()
	runs := ledger.New(filepath.Join(t.TempDir(), ledger.FileName))

	add := func(role, outcome string, cost float64, at time.Time) {
		if err := runs.Append(ledger.Entry{
			RunID: "прогон", Task: "OFF-1", Role: role, Project: "OFF", Started: at,
			Usage: runner.Usage{CostUSD: cost, DurationMS: 20000, Turns: 5}, Outcome: outcome,
		}); err != nil {
			t.Fatalf("строка не записана: %v", err)
		}
	}
	add("implementer", "done", 0.30, ledgerNow)
	add("implementer", "failed", 0.10, ledgerNow)
	add("reviewer", "done", 0.20, ledgerNow)
	add("implementer", "done", 5.00, ledgerNow.Add(-48*time.Hour))
	return runs
}

// Сводка отвечает на вопрос «во что обошёлся офис»: сколько прогонов, на сколько
// денег, по чём в среднем и чем кончились.
func TestLedgerSummary(t *testing.T) {
	var out bytes.Buffer
	if err := printLedger(filled(t), ledger.Filter{}, &out); err != nil {
		t.Fatalf("сводка не напечатана: %v", err)
	}

	for _, want := range []string{"4", "$5.6000", "$1.4000", "done 3", "failed 1"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("в сводке нет %q:\n%s", want, out.String())
		}
	}
}

// Отбор по роли и по времени — то, ради чего сводку и зовут: «сколько эта роль
// потратила за сутки».
func TestLedgerSummaryFilters(t *testing.T) {
	var out bytes.Buffer
	filter := ledger.Filter{Role: "implementer", Since: ledgerNow.Add(-24 * time.Hour)}
	if err := printLedger(filled(t), filter, &out); err != nil {
		t.Fatalf("сводка не напечатана: %v", err)
	}

	if !strings.Contains(out.String(), "$0.4000") {
		t.Errorf("отбор не сработал:\n%s", out.String())
	}
	if strings.Contains(out.String(), "reviewer") {
		t.Errorf("в сводку попала чужая роль:\n%s", out.String())
	}
}

// Пустой реестр — законное состояние машины, на которой ещё не работали.
// Молчать о нём нельзя: человек не должен гадать, нет прогонов или сломан вывод.
func TestLedgerSummarySaysWhenEmpty(t *testing.T) {
	var out bytes.Buffer
	runs := ledger.New(filepath.Join(t.TempDir(), ledger.FileName))
	if err := printLedger(runs, ledger.Filter{}, &out); err != nil {
		t.Fatalf("сводка не напечатана: %v", err)
	}
	if !strings.Contains(out.String(), "прогонов нет") {
		t.Errorf("о пустом реестре не сказано:\n%s", out.String())
	}
}

// Сумма без прогонов, не назвавших цены, занижена — и человек обязан это видеть,
// иначе он поверит, что офис стоил меньше, чем стоил.
func TestLedgerSummaryNamesUnknownAndBroken(t *testing.T) {
	runs := ledger.New(filepath.Join(t.TempDir(), ledger.FileName))
	if err := runs.Append(ledger.Entry{RunID: "тихий", Role: "implementer", Started: ledgerNow, Outcome: "failed"}); err != nil {
		t.Fatalf("строка не записана: %v", err)
	}
	broken, err := os.OpenFile(runs.Path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("реестр не открыт: %v", err)
	}
	if _, err := broken.WriteString(`{"run_id":"обрыв` + "\n"); err != nil {
		t.Fatalf("реестр не испорчен: %v", err)
	}
	broken.Close()

	var out bytes.Buffer
	if err := printLedger(runs, ledger.Filter{}, &out); err != nil {
		t.Fatalf("сводка не напечатана: %v", err)
	}
	if !strings.Contains(out.String(), "без цены") {
		t.Errorf("о прогонах без цены не сказано:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "не разобрано") {
		t.Errorf("о нечитаемых строках не сказано:\n%s", out.String())
	}
}
