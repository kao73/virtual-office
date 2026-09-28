package yougile

import (
	"cmp"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kao73/virtual-office/internal/tracker"
)

// messageDTO — сообщение чата задачи. id — одновременно время создания в мс.
type messageDTO struct {
	ID         float64 `json:"id"`
	FromUserID string  `json:"fromUserId"`
	Text       string  `json:"text"`
	Deleted    bool    `json:"deleted"`
}

// comments — переписка задачи от старых к новым. Чат задачи в YouGile
// адресуется id самой задачи. Порядок сервера не обещан — сортируем сами.
//
// Системные сообщения (перенос карточки, смена исполнителя) — не реплики:
// попади они сюда, tracker.HumanReply принял бы их за ответ человека. API
// по умолчанию их не отдаёт; includeSystem=false — явно, чтобы не зависеть
// от умолчания.
func (t *Tracker) comments(key string) ([]tracker.Comment, error) {
	msgs, err := listAll[messageDTO](t, "/chats/"+url.PathEscape(key)+"/messages",
		url.Values{"includeSystem": {"false"}})
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(msgs, func(a, b messageDTO) int { return cmp.Compare(a.ID, b.ID) })

	comments := make([]tracker.Comment, 0, len(msgs))
	for _, m := range msgs {
		if m.Deleted {
			continue
		}
		author, err := t.userEmail(m.FromUserID)
		if err != nil {
			return nil, err
		}
		ms := int64(m.ID)
		comments = append(comments, tracker.Comment{
			ID: strconv.FormatInt(ms, 10), Author: author, Created: time.UnixMilli(ms).UTC(), Body: m.Text,
		})
	}
	return comments, nil
}

// userEmail — email автора по id, с кэшем на весь Tracker: авторов в переписке
// немного, а rate limit — 50 запросов в минуту на компанию.
//
// Пользователя, которого сервер больше не знает (удалён из компании) или
// который не назвал email, автором называет его id: учёткой офиса он не
// совпадёт ни с чем, то есть это слова человека, — и ронять из-за него
// чтение задачи незачем.
func (t *Tracker) userEmail(id string) (string, error) {
	t.mu.Lock()
	email, ok := t.users[id]
	t.mu.Unlock()
	if ok {
		return email, nil
	}

	var user userDTO
	err := t.call(http.MethodGet, "/users/"+url.PathEscape(id), nil, nil, &user)
	switch {
	case errors.Is(err, tracker.ErrNotFound):
		email = id
	case err != nil:
		return "", fmt.Errorf("автор комментария %s не определён: %w", id, err)
	case user.Email == "":
		email = id
	default:
		email = user.Email
	}

	t.mu.Lock()
	t.users[id] = email
	t.mu.Unlock()
	return email, nil
}

// Comment пишет в чат задачи. Раннер читает text, поэтому он уходит дословно
// (строка-маркер и раздел «Вопросы» обязаны доехать буква в букву). textHtml
// API требует обязательно — это тот же текст, экранированный, с <br> вместо
// переводов строк, чтобы человек в интерфейсе видел то же самое.
func (t *Tracker) Comment(key string, by tracker.Actor, body string) error {
	if _, _, err := t.owned(key, by); err != nil {
		return err
	}
	return t.call(http.MethodPost, "/chats/"+url.PathEscape(key)+"/messages", nil,
		map[string]any{"text": body, "textHtml": messageHTML(body), "label": ""}, nil)
}

// messageHTML — текст комментария как безопасный HTML.
func messageHTML(body string) string {
	return strings.Join(strings.Split(html.EscapeString(body), "\n"), "<br>")
}

// FindByMarker — задачи проекта с меткой marker в apiData.labels. Источник
// идемпотентности пакетного создания (pipeline.ensureChildren).
//
// Полнотекстового поиска у API нет, так что обходим колонки проекта —
// не только графа: ребёнок, которого человек утащил за пределы графа, иначе
// выглядел бы «не найденным» и был бы создан заново. Такая находка — громкая
// ошибка ErrUnmappedColumn: статус для неё не выдумываем.
//
// Обход не полный, и это принято (tasks.md, Build review notes): колонки
// берутся из снимка на Open — заведённых позже он не видит, — а архивные
// задачи отсеивает tasksInColumn. Такого ребёнка страхует только
// idempotencyKey в CreateTask.
func (t *Tracker) FindByMarker(project, marker string) ([]tracker.TaskRef, error) {
	if err := t.checkProject(project); err != nil {
		return nil, err
	}
	var found []taskDTO
	for _, column := range t.columns {
		tasks, err := t.tasksInColumn(column)
		if err != nil {
			return nil, err
		}
		for _, raw := range tasks {
			data, err := decodeAPIData(raw.APIData)
			if err != nil {
				return nil, fmt.Errorf("задача YouGile %s: %w", raw.ID, err)
			}
			if slices.Contains(data.Labels, marker) {
				found = append(found, raw)
			}
		}
	}
	slices.SortStableFunc(found, byCreation)

	refs := make([]tracker.TaskRef, 0, len(found))
	for _, raw := range found {
		task, _, err := t.toTask(raw)
		if err != nil {
			return nil, err
		}
		refs = append(refs, task.Ref())
	}
	return refs, nil
}
