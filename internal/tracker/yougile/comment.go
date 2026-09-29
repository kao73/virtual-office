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

// chat — сообщения чата задачи от старых к новым, без удалённых. Чат задачи
// в YouGile адресуется id самой задачи. Порядок сервера не обещан — сортируем
// сами. Авторов не разрешает: вложениям (GetAttachment) они не нужны.
//
// Системные сообщения (перенос карточки, смена исполнителя) — не реплики:
// попади они сюда, tracker.HumanReply принял бы их за ответ человека. API
// по умолчанию их не отдаёт; includeSystem=false — явно, чтобы не зависеть
// от умолчания.
func (t *Tracker) chat(key string) ([]messageDTO, error) {
	msgs, err := listAll[messageDTO](t, "/chats/"+url.PathEscape(key)+"/messages",
		url.Values{"includeSystem": {"false"}})
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(msgs, func(a, b messageDTO) int { return cmp.Compare(a.ID, b.ID) })
	live := msgs[:0]
	for _, m := range msgs {
		if !m.Deleted {
			live = append(live, m)
		}
	}
	return live, nil
}

// comments — переписка в модели раннера, с email'ами авторов. Сообщение-файл
// остаётся репликой (ответ человека файлом — тоже ответ), но телом
// «[вложение: имя]», а не служебной строкой /root/#file:….
func (t *Tracker) comments(msgs []messageDTO) ([]tracker.Comment, error) {
	comments := make([]tracker.Comment, 0, len(msgs))
	for _, m := range msgs {
		author, err := t.userEmail(m.FromUserID)
		if err != nil {
			return nil, err
		}
		body := m.Text
		if link, ok := chatFileLink(m.Text); ok {
			body = "[вложение: " + link.Name + "]"
		}
		ms := int64(m.ID)
		comments = append(comments, tracker.Comment{
			ID: strconv.FormatInt(ms, 10), Author: author, Created: time.UnixMilli(ms).UTC(), Body: body,
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
	return t.postChat(key, body, messageHTML(body))
}

// postChat — сообщение в чат задачи. Право на запись проверяет вызывающий.
func (t *Tracker) postChat(key, text, textHTML string) error {
	return t.call(http.MethodPost, "/chats/"+url.PathEscape(key)+"/messages", nil,
		map[string]any{"text": text, "textHtml": textHTML, "label": ""}, nil)
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
// Карточка, у которой apiData верхнего уровня не объект, пропускается с
// записью в Logf: virtual_office в ней нет. Битый или новый virtual_office —
// громкая ErrOfficeData.
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
		tasks, err := t.tasksInColumn(column, false)
		if err != nil {
			return nil, err
		}
		for _, raw := range tasks {
			data, err := decodeAPIData(raw.APIData)
			if errors.Is(err, errAPIDataNotObject) {
				// Чужие данные без virtual_office: метки там нет, а падать из-за
				// них на весь проект незачем. Битый же virtual_office — громко:
				// пропусти мы своего испорченного ребёнка, ensureChildren завёл
				// бы дубль.
				t.logf("yougile: задача %s пропущена: %v", raw.ID, err)
				continue
			}
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
