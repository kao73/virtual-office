// Package pipeline — deps.go: общий помощник, отвечающий на вопрос «чего
// ждёт эта задача», для гейта Office.claim() и для видимости в runner ls
// (cmd/runner/board.go). Одна реализация, не две — design.md decision #3,
// docs/superpowers/specs/2026-09-07-split-dependency-gate-design.md §2.
package pipeline

import (
	"fmt"
	"strings"

	"github.com/kao73/virtual-office/internal/tracker"
)

// UnmetDependencies — зависимости ref, чей статус ещё не терминален, по
// данным уже загруженного среза задач проекта (byKey, обычно из
// List(project, statuses): Office.projectByKey строит его для claim(),
// cmd/runner/board.go — для printBoard, оба одним и тем же вызовом,
// который они и так уже делают).
//
// Зависимость, которой нет в byKey (задача удалена или никогда не
// существовала), тоже считается незакрытой — её нулевое значение,
// TaskRef{} (Key == ""), возвращается как есть, а не подменяется:
// явный сигнал вызывающему «эту зависимость нечем подтвердить», а не
// тихий пропуск. Падать громко, не считать свободной зависимость,
// которую нечем подтвердить (docs/DESIGN.md, принцип видимых отказов;
// design.md decision #6).
func UnmetDependencies(ref tracker.TaskRef, byKey map[string]tracker.TaskRef, terminal func(string) bool) []tracker.TaskRef {
	var unmet []tracker.TaskRef
	for _, key := range ref.DependsOn {
		dep, found := byKey[key]
		if !found || !terminal(dep.Status) {
			unmet = append(unmet, dep)
		}
	}
	return unmet
}

// describeUnmet — «OFF-2 (Review), OFF-3 (InProgress)» для лога claim():
// имя и текущий статус каждой незакрытой зависимости. Пустой Key
// (UnmetDependencies отдаёт его для зависимости, которой нет в byKey)
// показывается отдельной пометкой — молчать о том, что зависимость
// вообще пропала, было бы хуже, чем показать её без статуса.
func describeUnmet(unmet []tracker.TaskRef) string {
	parts := make([]string, len(unmet))
	for i, dep := range unmet {
		key := dep.Key
		if key == "" {
			key = "неизвестная задача"
		}
		parts[i] = fmt.Sprintf("%s (%s)", key, dep.Status)
	}
	return strings.Join(parts, ", ")
}
