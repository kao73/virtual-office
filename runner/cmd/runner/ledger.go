package main

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/kao73/virtual-office/ledger"
)

// ledgerCommand печатает сводку расхода по реестру прогонов.
//
// Реестр локален для машины: `runner ledger` отвечает на вопрос «во что обошёлся
// офис **здесь**». Два раннера на один проект дадут две сводки, и складывать
// их придётся человеку.
func ledgerCommand(args []string, out io.Writer) error {
	fs := flags("ledger")
	since := fs.Duration("since", 0, "считать только прогоны за последнее время, например 24h")
	role := fs.String("role", "", "считать только прогоны этой роли")
	task := fs.String("task", "", "считать только прогоны этой задачи")
	if err := fs.Parse(args); err != nil {
		return err
	}

	runs, err := ledger.Default()
	if err != nil {
		return err
	}

	filter := ledger.Filter{Role: *role, Task: *task}
	if *since > 0 {
		filter.Since = time.Now().Add(-*since)
	}
	return printLedger(runs, filter, out)
}

// printLedger печатает сводку: сколько прогонов, на сколько денег, по чём
// в среднем и чем кончились.
func printLedger(runs *ledger.Ledger, filter ledger.Filter, out io.Writer) error {
	total, err := runs.Sum(filter)
	if err != nil {
		return err
	}

	if total.Runs == 0 {
		fmt.Fprintf(out, "прогонов нет (%s)\n", describe(filter, runs.Path))
		return nil
	}

	fmt.Fprintf(out, "%s\n", describe(filter, runs.Path))
	fmt.Fprintf(out, "прогонов: %d, сумма $%.4f, в среднем $%.4f\n", total.Runs, total.CostUSD, total.Average())
	fmt.Fprintf(out, "исходы: %s\n", outcomes(total.ByOutcome))

	// Прогоны, не дошедшие до результата, — отдельной строкой, а не среди исходов:
	// исход у них синтетический, и в общем ряду они читались бы как провалы агента.
	// По этой же строке подбирается max_turns роли: сколько прогонов резалось
	// пределом, видно здесь, а не вслепую.
	if len(total.ByTermination) > 0 {
		fmt.Fprintf(out, "без результата: %s\n", outcomes(total.ByTermination))
	}

	// Обе оговорки означают одно: сумма занижена. Молчать о них нельзя — человек
	// поверит, что офис стоил меньше, чем стоил, а на те же суммы смотрят пределы.
	if total.Unknown > 0 {
		fmt.Fprintf(out, "прогонов без цены: %d (не оставили итога — сумма занижена)\n", total.Unknown)
	}
	if total.Broken > 0 {
		fmt.Fprintf(out, "строк не разобрано: %d (сумма занижена)\n", total.Broken)
	}
	return nil
}

// describe — по чему считали. Печатается всегда, в том числе над пустой сводкой:
// «прогонов нет» без условий отправило бы искать не там.
func describe(f ledger.Filter, path string) string {
	parts := []string{path}
	if f.Role != "" {
		parts = append(parts, "роль "+f.Role)
	}
	if f.Task != "" {
		parts = append(parts, "задача "+f.Task)
	}
	if !f.Since.IsZero() {
		parts = append(parts, "с "+f.Since.Local().Format(time.DateTime))
	}
	return strings.Join(parts, ", ")
}

// outcomes — исходы по алфавиту: два запуска должны печатать одно и то же.
func outcomes(byOutcome map[string]int) string {
	parts := make([]string, 0, len(byOutcome))
	for _, name := range slices.Sorted(maps.Keys(byOutcome)) {
		parts = append(parts, fmt.Sprintf("%s %d", name, byOutcome[name]))
	}
	return strings.Join(parts, ", ")
}
