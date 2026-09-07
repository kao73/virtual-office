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
// существовала), тоже считается незакрытой — но вместо нулевого
// значения byKey[key] в unmet попадает TaskRef{Key: key}: тот самый
// ключ, который искали, с пустым Status. Явный сигнал вызывающему «эту
// зависимость нечем подтвердить», а не тихий пропуск — и, в отличие от
// TaskRef{}, называющий, ЧЕГО не хватает (fix round 1, Finding 2:
// прежняя версия отдавала byKey[key] как есть, а его нулевое значение
// при !found теряло искомый ключ — лог видел «незакрытая зависимость»,
// но не видел какая). Падать громко, не считать свободной зависимость,
// которую нечем подтвердить (docs/DESIGN.md, принцип видимых отказов;
// design.md decision #6).
func UnmetDependencies(ref tracker.TaskRef, byKey map[string]tracker.TaskRef, terminal func(string) bool) []tracker.TaskRef {
	var unmet []tracker.TaskRef
	for _, key := range ref.DependsOn {
		dep, found := byKey[key]
		if !found {
			unmet = append(unmet, tracker.TaskRef{Key: key})
			continue
		}
		if !terminal(dep.Status) {
			unmet = append(unmet, dep)
		}
	}
	return unmet
}

// describeUnmet — «OFF-2 (Review), OFF-3 (InProgress)» для лога claim():
// имя и текущий статус каждой незакрытой зависимости. Пропавшая
// зависимость теперь приходит с непустым Key (UnmetDependencies отдаёт
// TaskRef{Key: key}, не нулевое значение — fix round 1, Finding 2) и
// печатается как «OFF-404 ()»: пустой статус, но настоящий ключ — это и
// есть диагностируемая форма. Пометка «неизвестная задача» ниже —
// защитный запасной путь на случай пустого Key от какого-то другого
// вызывающего; сегодняшний единственный вызывающий (claim()) её больше
// не задействует.
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
