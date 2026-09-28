package yougile

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"

	"github.com/kao73/virtual-office/internal/tracker"
)

// ErrUnmappedColumn — задача стоит в колонке, которой не сопоставлен ни один
// статус графа: человек завёл колонку вне графа или утащил туда карточку.
// Статус не выдумывается — ошибка громкая.
var ErrUnmappedColumn = errors.New("yougile: задача в колонке вне карты статусов")

// taskDTO — задача в ответе API: то, что читает адаптер.
type taskDTO struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	ColumnID    string          `json:"columnId"`
	Timestamp   float64         `json:"timestamp"` // мс создания
	Archived    bool            `json:"archived"`
	Deleted     bool            `json:"deleted"`
	APIData     json.RawMessage `json:"apiData"`
}

// getRaw — задача без переписки: одного запроса хватает и проверке владения,
// и захвату; чат нужен только Get.
func (t *Tracker) getRaw(key string) (taskDTO, error) {
	var raw taskDTO
	if err := t.call(http.MethodGet, "/tasks/"+url.PathEscape(key), nil, nil, &raw); err != nil {
		return taskDTO{}, err
	}
	if raw.Deleted {
		return taskDTO{}, fmt.Errorf("%w: задача YouGile %s удалена", tracker.ErrNotFound, key)
	}
	return raw, nil
}

// toTask переводит задачу API в модель раннера. apiData возвращается рядом —
// его перепишут и отправят обратно целиком те, кто мутирует задачу.
func (t *Tracker) toTask(raw taskDTO) (tracker.Task, apiData, error) {
	data, err := decodeAPIData(raw.APIData)
	if err != nil {
		return tracker.Task{}, apiData{}, fmt.Errorf("задача YouGile %s: %w", raw.ID, err)
	}
	status, ok := t.columnStatus[raw.ColumnID]
	if !ok {
		return tracker.Task{}, apiData{}, fmt.Errorf("%w: задача %s стоит в колонке %q, которой не сопоставлен ни один статус",
			ErrUnmappedColumn, raw.ID, raw.ColumnID)
	}
	task := tracker.Task{
		Key: raw.ID, Project: t.cfg.ProjectID, Summary: raw.Title, Description: raw.Description,
		Status: status, Labels: slices.Clone(data.Labels), Attempts: data.Attempts, HumanFlag: data.HumanWait,
	}
	if data.Lease != nil {
		task.Owner, task.RunID, task.LeaseUntil = data.Lease.Owner, data.Lease.RunID, data.Lease.LeaseUntil
	}
	return task, data, nil
}

// Get — задача целиком, включая всю переписку чата задачи.
func (t *Tracker) Get(key string) (tracker.Task, error) {
	raw, err := t.getRaw(key)
	if err != nil {
		return tracker.Task{}, err
	}
	task, _, err := t.toTask(raw)
	if err != nil {
		return tracker.Task{}, err
	}
	comments, err := t.comments(key)
	if err != nil {
		return tracker.Task{}, err
	}
	task.Comments = comments
	return task, nil
}
