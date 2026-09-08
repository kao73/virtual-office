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
// Зависимость, которой нет в byKey, тоже считается незакрытой — но
// вместо нулевого значения byKey[key] в unmet попадает TaskRef{Key: key}:
// тот самый ключ, который искали, с пустым Status. Явный сигнал
// вызывающему «эту зависимость нечем подтвердить», а не тихий пропуск —
// и, в отличие от TaskRef{}, называющий, ЧЕГО не хватает (fix round 1,
// Finding 2: прежняя версия отдавала byKey[key] как есть, а его нулевое
// значение при !found теряло искомый ключ — лог видел «незакрытая
// зависимость», но не видел какая). Падать громко, не считать свободной
// зависимость, которую нечем подтвердить (docs/DESIGN.md, принцип видимых
// отказов; design.md decision #6).
//
// Отсутствие в byKey не означает «задачи не существует» (fix round 2,
// Finding 3): byKey строится из List(project, статусы графа этого
// проекта) — ключа там законно может не быть и потому, что задачу увели
// в статус вне графа (человек перевёл в Closed/Won't Do/свой статус), и
// потому, что зависимость указывает на задачу из другого проекта. Оба
// случая реальны на живой доске, не только «удалена». Лишний Get() ради
// того, чтобы отличить их от настоящего удаления, здесь того не стоит
// (тот же аргумент цены, что у Finding 2 выше) — DescribeUnmet ниже
// поэтому формулирует находку как «не найдена в статусах графа», а не
// как утверждение о несуществовании, которое эта функция проверить
// не может.
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

// DescribeUnmet — «OFF-2 (Review), OFF-3 (InProgress)» для лога claim() и
// для колонки ожидания в runner ls (cmd/runner/board.go, dependsColumn):
// имя и текущий статус каждой незакрытой зависимости. Экспортирована ради
// design.md decision #3 («одна реализация, не две») — раньше board.go
// держал свою копию этого форматирования, синхронизированную с этой
// только парой комментариев «расходиться им нельзя» (pr-converge round 1,
// Finding 1).
//
// Пропавшая зависимость приходит с непустым Key (UnmetDependencies отдаёт
// TaskRef{Key: key}, не нулевое значение — fix round 1, Finding 2) и
// печатается как «OFF-404 ()»: пустой статус, но настоящий ключ — это и
// есть диагностируемая форма. Строка ниже — защитный запасной путь на
// случай пустого Key: сегодня оба вызывающих (claim(), dependsColumn)
// получают его только если ref.DependsOn сам содержит пустую строку —
// испорченные исходные данные, не обычный путь. Формулировка — «не найдена
// в статусах графа», не «неизвестная задача»: последнее звучало бы
// утверждением о несуществовании, а byKey (см. доккомент UnmetDependencies)
// может не знать ключ и по другой причине — задача вне статусов графа
// этого проекта или в чужом проекте (fix round 2, Finding 3).
func DescribeUnmet(unmet []tracker.TaskRef) string {
	parts := make([]string, len(unmet))
	for i, dep := range unmet {
		key := dep.Key
		if key == "" {
			key = "не найдена в статусах графа"
		}
		parts[i] = fmt.Sprintf("%s (%s)", key, dep.Status)
	}
	return strings.Join(parts, ", ")
}
