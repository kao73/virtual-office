package yougile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/kao73/virtual-office/internal/tracker"
)

// Ключи apiData, которыми владеет адаптер. yougile-dependencies-attachments
// добавит сюда depends_on и манифест вложений — теми же соседями верхнего уровня.
const (
	keyLease     = "lease"
	keyAttempts  = "attempts"
	keyHumanWait = "human_wait"
	keyLabels    = "labels"
)

// leaseData — аренда: пишется и снимается целиком.
type leaseData struct {
	Owner      string    `json:"owner"`
	RunID      string    `json:"run_id"`
	LeaseUntil time.Time `json:"lease_until"`
}

// apiData — наш взгляд на apiData задачи. attempts и human_wait живут вне
// lease: Release их не трогает, как jira не трогает attempts и метку человека.
//
// Инвариант каждой записи: apiData читается целиком, меняется и пишется
// целиком. extra держит ключи, которых адаптер не знает, — без него запись
// молча стирала бы чужие данные.
type apiData struct {
	Lease     *leaseData
	Attempts  int
	HumanWait bool
	Labels    []string
	extra     map[string]json.RawMessage
}

// decodeAPIData разбирает apiData. Пусто и null — нулевое значение: задача,
// которую офис ни разу не трогал, свободна.
func decodeAPIData(raw json.RawMessage) (apiData, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return apiData{}, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil {
		return apiData{}, fmt.Errorf("apiData не JSON-объект: %w", err)
	}

	var d apiData
	targets := map[string]any{keyLease: &d.Lease, keyAttempts: &d.Attempts, keyHumanWait: &d.HumanWait, keyLabels: &d.Labels}
	for key, target := range targets {
		value, ok := fields[key]
		if !ok {
			continue
		}
		delete(fields, key)
		if err := json.Unmarshal(value, target); err != nil {
			return apiData{}, fmt.Errorf("apiData.%s: %w", key, err)
		}
	}
	if len(fields) > 0 {
		d.extra = fields
	}
	return d, nil
}

// encode — объект целиком для PUT: чужие ключи как были, свои — все и явно.
// lease пишется null'ом, а не пропускается: если сервер сливает apiData,
// а не заменяет, пропущенный ключ оставил бы аренду висеть.
func (d apiData) encode() map[string]any {
	out := make(map[string]any, len(d.extra)+4)
	for key, value := range d.extra {
		out[key] = value
	}
	out[keyLease] = d.Lease // nil *leaseData сериализуется в null
	out[keyAttempts] = d.Attempts
	out[keyHumanWait] = d.HumanWait
	labels := d.Labels
	if labels == nil {
		labels = []string{}
	}
	out[keyLabels] = labels
	return out
}

// Claim — захват: записать аренду (и рабочую колонку) одним PUT, перечитать,
// сверить владельца.
//
// CAS в YouGile нет, как и в JIRA. Сверка закрывает половину гонки — ту, где
// нашу запись затёрли после нас: мы честно проигрываем. Зеркальную — оба
// прочли задачу свободной до чьей-либо записи — не закрывает ничто; см.
// доккомент jira.Claim. Живую чужую аренду захват не перезаписывает никогда.
func (t *Tracker) Claim(req tracker.ClaimRequest) error {
	if req.RunID == "" {
		return errors.New("yougile: захват без run_id — сверять владельца будет не с чем")
	}
	var working string
	if req.WorkingStatus != "" {
		id, err := t.columnFor(req.WorkingStatus)
		if err != nil {
			return err
		}
		working = id
	}

	raw, err := t.getRaw(req.Key)
	if err != nil {
		return err
	}
	task, data, err := t.toTask(raw)
	if err != nil {
		return err
	}
	switch {
	case task.Status != req.ExpectStatus:
		return fmt.Errorf("%w: %s в статусе %q, а захват шёл из %q",
			tracker.ErrClaimLost, req.Key, task.Status, req.ExpectStatus)
	case task.LeaseAlive(t.Now()):
		return fmt.Errorf("%w: %s арендована прогоном %s до %s",
			tracker.ErrClaimLost, req.Key, task.RunID, task.LeaseUntil.Format(time.RFC3339))
	}

	data.Lease = &leaseData{Owner: req.Owner, RunID: req.RunID, LeaseUntil: req.LeaseUntil}
	body := map[string]any{"apiData": data.encode()}
	if working != "" && req.WorkingStatus != task.Status {
		body["columnId"] = working
	}
	if err := t.putTask(req.Key, body); err != nil {
		return err
	}

	freshRaw, err := t.getRaw(req.Key)
	if err != nil {
		return err
	}
	fresh, _, err := t.toTask(freshRaw)
	if err != nil {
		return err
	}
	if fresh.RunID != req.RunID {
		return fmt.Errorf("%w: после захвата %s владеет %s", tracker.ErrClaimLost, req.Key, fresh.RunID)
	}
	return nil
}
