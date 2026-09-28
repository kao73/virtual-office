package yougile

import (
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
