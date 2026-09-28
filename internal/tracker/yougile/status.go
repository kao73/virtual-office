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

	"github.com/kao73/virtual-office/internal/tracker"
)

type boardDTO struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	Deleted   bool   `json:"deleted"`
}

type columnDTO struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	BoardID string `json:"boardId"`
	Deleted bool   `json:"deleted"`
}

// columnInfo — колонка проекта, кэшированная на Open.
type columnInfo struct {
	ID, Title, BoardID string
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
	var columns []columnInfo
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
			columns = append(columns, columnInfo{ID: c.ID, Title: c.Title, BoardID: c.BoardID})
		}
	}

	known := make(map[string]bool, len(columns))
	for _, c := range columns {
		known[c.ID] = true
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

// tasksInColumn — живые задачи колонки, фильтр по columnId — на сервере.
// Архивные и удалённые отбрасываются: архивная карточка в колонке Done не
// должна снова стать кандидатом. Сверка columnId — страховка на случай,
// если сервер фильтр проигнорирует.
func (t *Tracker) tasksInColumn(columnID string) ([]taskDTO, error) {
	all, err := listAll[taskDTO](t, "/task-list", url.Values{"columnId": {columnID}})
	if err != nil {
		return nil, err
	}
	live := all[:0]
	for _, task := range all {
		if task.Deleted || task.Archived || task.ColumnID != columnID {
			continue
		}
		live = append(live, task)
	}
	return live, nil
}

// collect — задачи названных колонок, от старых к новым (FIFO: приоритета
// в YouGile нет, design.md Decisions), отфильтрованные keep.
func (t *Tracker) collect(columnIDs []string, keep func(tracker.Task) bool) ([]tracker.TaskRef, error) {
	var raws []taskDTO
	for _, id := range columnIDs {
		tasks, err := t.tasksInColumn(id)
		if err != nil {
			return nil, err
		}
		raws = append(raws, tasks...)
	}
	slices.SortStableFunc(raws, func(a, b taskDTO) int {
		return cmp.Or(cmp.Compare(a.Timestamp, b.Timestamp), cmp.Compare(a.ID, b.ID))
	})

	refs := make([]tracker.TaskRef, 0, len(raws))
	for _, raw := range raws {
		task, _, err := t.toTask(raw)
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
	return t.collect([]string{id}, func(task tracker.Task) bool { return !task.LeaseAlive(t.Now()) })
}

// List — задачи названных статусов как есть, с живой арендой тоже: этот
// список смотрит человек, и «кто работает сейчас» — первое, что он ищет.
func (t *Tracker) List(project string, statuses []string) ([]tracker.TaskRef, error) {
	if err := t.checkProject(project); err != nil {
		return nil, err
	}
	if len(statuses) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(statuses))
	for _, status := range statuses {
		id, err := t.columnFor(status)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return t.collect(ids, func(tracker.Task) bool { return true })
}
