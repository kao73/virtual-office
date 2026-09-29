package yougile

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/kao73/virtual-office/internal/tracker"
)

type boardDTO struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	Deleted   bool   `json:"deleted"`
}

type columnDTO struct {
	ID      string `json:"id"`
	BoardID string `json:"boardId"`
	Deleted bool   `json:"deleted"`
}

// loadColumns читает доски и колонки проекта один раз и сверяет с ними
// ColumnIDs. Ничего не создаёт: колонки заводит человек, а опечатка в id
// должна ронять Open, а не тихо давать пустую очередь.
func (t *Tracker) loadColumns() error {
	if err := t.call(http.MethodGet, "/projects/"+url.PathEscape(t.cfg.ProjectID), nil, nil, nil); err != nil {
		if errors.Is(err, tracker.ErrNotFound) {
			return fmt.Errorf("%w: проект YouGile %q", tracker.ErrNoProject, t.cfg.ProjectID)
		}
		return err
	}

	boards, err := listAll[boardDTO](t, "/boards", url.Values{"projectId": {t.cfg.ProjectID}})
	if err != nil {
		return err
	}
	var columns []string
	for _, board := range boards {
		if board.Deleted || board.ProjectID != t.cfg.ProjectID {
			continue
		}
		list, err := listAll[columnDTO](t, "/columns", url.Values{"boardId": {board.ID}})
		if err != nil {
			return err
		}
		for _, c := range list {
			if c.Deleted || c.BoardID != board.ID {
				continue
			}
			columns = append(columns, c.ID)
		}
	}

	known := make(map[string]bool, len(columns))
	for _, id := range columns {
		known[id] = true
	}
	var missing []string
	for _, status := range slices.Sorted(maps.Keys(t.cfg.ColumnIDs)) {
		if id := t.cfg.ColumnIDs[status]; !known[id] {
			missing = append(missing, status+"="+id)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("yougile: в проекте %q нет колонок %s — заведи их на доске, адаптер колонок не создаёт",
			t.cfg.ProjectID, strings.Join(missing, ", "))
	}
	t.columns = columns
	return nil
}

// columnFor — колонка статуса графа; незнакомый статус — ошибка конфигурации.
func (t *Tracker) columnFor(status string) (string, error) {
	id, ok := t.cfg.ColumnIDs[status]
	if !ok {
		return "", fmt.Errorf("yougile: статусу %q не сопоставлена колонка", status)
	}
	return id, nil
}

// tasksInColumn — задачи колонки, фильтр по columnId — на сервере.
// Удалённые отбрасываются всегда. Архивные — если не просили withArchived:
// архивная карточка в колонке Done не должна снова стать кандидатом, но List
// её отдаёт — иначе гейт зависимостей не увидел бы, что заархивированная
// зависимость закрыта (design doc §2). Сверка columnId — страховка на случай,
// если сервер фильтр проигнорирует.
func (t *Tracker) tasksInColumn(columnID string, withArchived bool) ([]taskDTO, error) {
	all, err := listAll[taskDTO](t, "/task-list", url.Values{"columnId": {columnID}})
	if err != nil {
		return nil, err
	}
	live := all[:0]
	for _, task := range all {
		if task.Deleted || (task.Archived && !withArchived) || task.ColumnID != columnID {
			continue
		}
		live = append(live, task)
	}
	return live, nil
}

// byCreation — от старых к новым; равное время создания разводит id, чтобы
// порядок не зависел от сервера.
func byCreation(a, b taskDTO) int {
	return cmp.Or(cmp.Compare(a.Timestamp, b.Timestamp), cmp.Compare(a.ID, b.ID))
}

// collect — задачи названных колонок, от старых к новым (FIFO: приоритета
// в YouGile нет, design.md Decisions), отфильтрованные keep.
// Карточка с нечитаемыми данными офиса (ErrOfficeData) пропускается с записью в Logf.
// withArchived — как у tasksInColumn: только для List.
func (t *Tracker) collect(columnIDs []string, withArchived bool, keep func(tracker.Task) bool) ([]tracker.TaskRef, error) {
	var raws []taskDTO
	for _, id := range columnIDs {
		tasks, err := t.tasksInColumn(id, withArchived)
		if err != nil {
			return nil, err
		}
		raws = append(raws, tasks...)
	}
	slices.SortStableFunc(raws, byCreation)

	refs := make([]tracker.TaskRef, 0, len(raws))
	for _, raw := range raws {
		task, _, err := t.toTask(raw)
		if errors.Is(err, ErrOfficeData) {
			// Одна карточка не должна останавливать очередь колонки и reaper.
			t.Logf("yougile: задача %s пропущена: %v", raw.ID, err)
			continue
		}
		if err != nil {
			return nil, err
		}
		if keep(task) {
			refs = append(refs, task.Ref())
		}
	}
	return refs, nil
}

// ListReady — кандидаты статуса: без живой аренды, от старых к новым.
func (t *Tracker) ListReady(project, status string) ([]tracker.TaskRef, error) {
	if err := t.checkProject(project); err != nil {
		return nil, err
	}
	id, err := t.columnFor(status)
	if err != nil {
		return nil, err
	}
	return t.collect([]string{id}, false, func(task tracker.Task) bool { return !task.LeaseAlive(t.Now()) })
}

// List — задачи названных статусов как есть, с живой арендой тоже: этот
// список смотрит человек, и «кто работает сейчас» — первое, что он ищет.
// Архивные карточки — тоже, со статусом их колонки: по List гейт захвата
// (pipeline.UnmetDependencies) решает, закрыта ли зависимость, и архивная
// зависимость в терминальной колонке иначе держала бы зависимую задачу
// вечно. Удалённых нет.
func (t *Tracker) List(project string, statuses []string) ([]tracker.TaskRef, error) {
	if err := t.checkProject(project); err != nil {
		return nil, err
	}
	if len(statuses) == 0 {
		return nil, nil
	}
	// Повторённый статус не должен удваивать задачи: у jira это делает JQL
	// «status in (…)», здесь — сами.
	ids := make([]string, 0, len(statuses))
	for _, status := range slices.Compact(slices.Sorted(slices.Values(statuses))) {
		id, err := t.columnFor(status)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return t.collect(ids, true, func(tracker.Task) bool { return true })
}

// configuredColumns — колонки всех статусов графа, в стабильном порядке.
func (t *Tracker) configuredColumns() []string {
	return slices.Sorted(maps.Values(t.cfg.ColumnIDs))
}

// ListExpired — задачи с истёкшей арендой в любом статусе графа: сырьё для
// reaper. Колонки вне графа не смотрит — их задачи Get всё равно не прочтёт.
func (t *Tracker) ListExpired(project string, now time.Time) ([]tracker.TaskRef, error) {
	if err := t.checkProject(project); err != nil {
		return nil, err
	}
	return t.collect(t.configuredColumns(), false, func(task tracker.Task) bool {
		return task.RunID != "" && !task.LeaseAlive(now)
	})
}

// Transition переводит задачу в колонку статуса. Одно поле, одна запись:
// второго состояния, которое надо держать в согласии, нет. apiData не трогает.
func (t *Tracker) Transition(key string, by tracker.Actor, toStatus string) error {
	column, err := t.columnFor(toStatus)
	if err != nil {
		return err
	}
	if _, _, err := t.owned(key, by); err != nil {
		return err
	}
	return t.putTask(key, map[string]any{"columnId": column})
}
