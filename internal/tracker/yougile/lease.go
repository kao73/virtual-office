package yougile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
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
