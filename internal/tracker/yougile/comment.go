package yougile

import (
	"cmp"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
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
func (t *Tracker) comments(key string) ([]tracker.Comment, error) {
	msgs, err := listAll[messageDTO](t, "/chats/"+url.PathEscape(key)+"/messages", nil)
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
// Пользователя, которого сервер больше не знает (удалён из компании), автором
// называет его id: учёткой офиса он не совпадёт ни с чем, то есть это слова
// человека, — и ронять из-за него чтение задачи незачем.
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
	default:
		email = user.Email
	}

	t.mu.Lock()
	t.users[id] = email
	t.mu.Unlock()
	return email, nil
}
