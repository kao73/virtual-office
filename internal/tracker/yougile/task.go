package yougile

import (
	"crypto/sha256"
	"encoding/hex"
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
	ID            string          `json:"id"`
	Title         string          `json:"title"`
	IDTaskProject string          `json:"idTaskProject"` // человекочитаемый номер задачи в проекте, «ID-7»
	Description   string          `json:"description"`
	ColumnID      string          `json:"columnId"`
	Timestamp     float64         `json:"timestamp"` // мс создания
	Archived      bool            `json:"archived"`
	Deleted       bool            `json:"deleted"`
	APIData       json.RawMessage `json:"apiData"`
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

// load — getRaw и toTask одним шагом: задача как есть, в модели раннера и
// её apiData.
func (t *Tracker) load(key string) (taskDTO, tracker.Task, apiData, error) {
	raw, err := t.getRaw(key)
	if err != nil {
		return taskDTO{}, tracker.Task{}, apiData{}, err
	}
	task, data, err := t.toTask(raw)
	if err != nil {
		return taskDTO{}, tracker.Task{}, apiData{}, err
	}
	return raw, task, data, nil
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
		Key: raw.ID, Project: t.cfg.Key, Summary: raw.Title, Description: raw.Description,
		Status: status, Labels: slices.Clone(data.Labels), DependsOn: slices.Clone(data.DependsOn), Attempts: data.Attempts, HumanFlag: data.HumanWait,
	}
	if data.Lease != nil {
		task.Owner, task.RunID, task.LeaseUntil = data.Lease.Owner, data.Lease.RunID, data.Lease.LeaseUntil
	}
	return task, data, nil
}

// Get — задача целиком: переписка чата и вложения из описания и чата.
func (t *Tracker) Get(key string) (tracker.Task, error) {
	raw, task, _, err := t.load(key)
	if err != nil {
		return tracker.Task{}, err
	}
	msgs, err := t.chat(key)
	if err != nil {
		return tracker.Task{}, err
	}
	comments, err := t.comments(msgs)
	if err != nil {
		return tracker.Task{}, err
	}
	task.Comments = comments
	task.Attachments = attachmentRefs(fileLinks(raw.Description, msgs))
	return task, nil
}

// putTask — PUT /tasks/{key}: только переданные поля задачи.
func (t *Tracker) putTask(key string, body map[string]any) error {
	return t.call(http.MethodPut, "/tasks/"+url.PathEscape(key), nil, body, nil)
}

// owned читает задачу (без переписки) и проверяет право актора её менять
// общим tracker.CheckOwner: разъехавшись с jira и mock, правило дало бы гонку.
func (t *Tracker) owned(key string, by tracker.Actor) (tracker.Task, apiData, error) {
	_, task, data, err := t.load(key)
	if err != nil {
		return tracker.Task{}, apiData{}, err
	}
	if err := tracker.CheckOwner(task, by, t.now()); err != nil {
		return tracker.Task{}, apiData{}, err
	}
	return task, data, nil
}

// mutateAPIData — прочитать apiData целиком, поменять, записать целиком.
// Колонку не трогает.
func (t *Tracker) mutateAPIData(key string, by tracker.Actor, change func(*apiData)) error {
	_, data, err := t.owned(key, by)
	if err != nil {
		return err
	}
	change(&data)
	return t.putTask(key, map[string]any{"apiData": data.encode()})
}

// CreateTask заводит задачу в колонке CreateStatus. Метки (у YouGile своих нет)
// ложатся в apiData.labels — по ним ищет FindByMarker.
//
// idempotencyKey выводится из содержимого, а не случаен: повтор после
// неоднозначного отказа отдаёт тот же ключ, и YouGile возвращает уже
// созданную задачу. Это страховка поверх основного механизма — проверки
// FindByMarker перед созданием (pipeline.ensureChildren). Метки входят в хэш,
// чтобы два ребёнка split с одинаковым текстом не слились в одну задачу.
func (t *Tracker) CreateTask(project string, input tracker.TaskInput) (tracker.TaskRef, error) {
	if err := t.checkProject(project); err != nil {
		return tracker.TaskRef{}, err
	}
	if t.cfg.CreateStatus == "" {
		return tracker.TaskRef{}, errors.New("yougile: create_status не задан — новой задаче некуда лечь")
	}
	column, err := t.columnFor(t.cfg.CreateStatus)
	if err != nil {
		return tracker.TaskRef{}, err
	}

	description := joinDescription(input.Description, input.DescriptionAppend)
	labels := slices.Sorted(slices.Values(input.Labels))
	body := map[string]any{
		"title":          input.Summary,
		"description":    description,
		"columnId":       column,
		"apiData":        apiData{Labels: slices.Clone(input.Labels)}.encode(),
		"idempotencyKey": idempotencyKey(append([]string{project, input.Summary, description}, labels...)...),
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := t.call(http.MethodPost, "/tasks", nil, body, &created); err != nil {
		return tracker.TaskRef{}, err
	}
	if created.ID == "" {
		return tracker.TaskRef{}, errors.New("yougile: POST /tasks ответил без id — задача могла создаться, повтор найдёт её по idempotencyKey")
	}

	_, task, _, err := t.load(created.ID)
	if err != nil {
		return tracker.TaskRef{}, err
	}
	return task.Ref(), nil
}

// idempotencyKey — SHA-256 частей с NUL после каждой: ("ab","c") ≠ ("a","bc").
func idempotencyKey(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// joinDescription — то же правило, что у jira и mock: разделитель только
// когда есть что разделять.
func joinDescription(description, appendix string) string {
	switch {
	case appendix == "":
		return description
	case description == "":
		return appendix
	default:
		return description + "\n\n" + appendix
	}
}
