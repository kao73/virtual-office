package yougile

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// roundTrip — encode и обратно через настоящий JSON, как это пройдёт по проводу.
func roundTrip(t *testing.T, d apiData) map[string]any {
	t.Helper()
	raw, err := json.Marshal(d.encode())
	if err != nil {
		t.Fatalf("encode не сериализуется: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("encode дал не объект: %v", err)
	}
	return out
}

func TestDecodeAPIDataEmptyIsZero(t *testing.T) {
	for _, raw := range []string{"", "null", "  ", "{}"} {
		d, err := decodeAPIData(json.RawMessage(raw))
		if err != nil {
			t.Errorf("%q: %v", raw, err)
		}
		if d.Lease != nil || d.Attempts != 0 || d.HumanWait || d.Labels != nil || d.extra != nil {
			t.Errorf("%q дал не нулевое значение: %+v", raw, d)
		}
	}
}

func TestDecodeAPIDataReadsOwnKeys(t *testing.T) {
	until := time.Date(2026, 9, 28, 12, 30, 0, 123456789, time.UTC)
	raw := `{"lease":{"owner":"implementer","run_id":"run-1","lease_until":"` + until.Format(time.RFC3339Nano) +
		`"},"attempts":2,"human_wait":true,"labels":["split:VO-1:a"]}`
	d, err := decodeAPIData(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	if d.Lease == nil || d.Lease.Owner != "implementer" || d.Lease.RunID != "run-1" || !d.Lease.LeaseUntil.Equal(until) {
		t.Errorf("аренда: %+v", d.Lease)
	}
	if d.Attempts != 2 || !d.HumanWait || !reflect.DeepEqual(d.Labels, []string{"split:VO-1:a"}) {
		t.Errorf("поля: %+v", d)
	}
}

// Review Focus #2: ключи, записанные не нами, переживают любую нашу запись.
func TestAPIDataKeepsForeignKeys(t *testing.T) {
	raw := `{"crm":{"deal":42,"tags":["a","b"]},"depends_on":["task-9"],"attempts":1}`
	d, err := decodeAPIData(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	d.Attempts = 5
	out := roundTrip(t, d)
	if !reflect.DeepEqual(out["crm"], map[string]any{"deal": float64(42), "tags": []any{"a", "b"}}) {
		t.Errorf("чужой ключ crm искажён: %#v", out["crm"])
	}
	if !reflect.DeepEqual(out["depends_on"], []any{"task-9"}) {
		t.Errorf("чужой ключ depends_on искажён: %#v", out["depends_on"])
	}
	if out["attempts"] != float64(5) {
		t.Errorf("attempts = %#v", out["attempts"])
	}
}

// Review Focus #3: свои ключи пишутся всегда, аренда — явным null, иначе
// при слиянии apiData на сервере снятая аренда не снялась бы.
func TestEncodeAlwaysWritesOwnKeys(t *testing.T) {
	out := roundTrip(t, apiData{})
	for _, key := range []string{keyLease, keyAttempts, keyHumanWait, keyLabels} {
		if _, ok := out[key]; !ok {
			t.Errorf("ключ %q не записан: %#v", key, out)
		}
	}
	if out[keyLease] != nil {
		t.Errorf("свободная аренда записана как %#v, ожидался null", out[keyLease])
	}
	if !reflect.DeepEqual(out[keyLabels], []any{}) {
		t.Errorf("пустые метки записаны как %#v, ожидался []", out[keyLabels])
	}
}

func TestDecodeAPIDataRejectsGarbage(t *testing.T) {
	for _, raw := range []string{`"string"`, `[1,2]`, `{"lease":"not an object"}`, `{"attempts":"three"}`} {
		if _, err := decodeAPIData(json.RawMessage(raw)); err == nil {
			t.Errorf("%s принят", raw)
		}
	}
}
