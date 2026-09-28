package yougile

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/kao73/virtual-office/internal/tracker"
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

func claimReq(runID string) tracker.ClaimRequest {
	return tracker.ClaimRequest{
		Key: testKey, RunID: runID, Owner: "implementer", LeaseUntil: now.Add(30 * time.Minute),
		ExpectStatus: "Ready", WorkingStatus: "InProgress",
	}
}

func putLease(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	data, _ := body["apiData"].(map[string]any)
	lease, _ := data["lease"].(map[string]any)
	return lease
}

// Аренда и перевод в рабочую колонку — одной записью: YouGile это умеет,
// и состояния «аренда есть, статус старый» между ними не бывает.
func TestClaimWritesLeaseAndColumnInOnePut(t *testing.T) {
	tr, fake := fixture(t)
	if err := tr.Claim(claimReq("run-1")); err != nil {
		t.Fatalf("захват не удался: %v", err)
	}
	if len(fake.puts) != 1 {
		t.Fatalf("PUT'ов %d, ожидался один", len(fake.puts))
	}
	lease := putLease(t, fake.puts[0])
	if lease["run_id"] != "run-1" || lease["owner"] != "implementer" {
		t.Errorf("аренда: %#v", lease)
	}
	if fake.puts[0]["columnId"] != colWork {
		t.Errorf("columnId = %#v, ожидалась рабочая колонка", fake.puts[0]["columnId"])
	}
	task, _ := tr.Get(testKey)
	if task.Status != "InProgress" || task.RunID != "run-1" || !task.LeaseUntil.Equal(now.Add(30*time.Minute)) {
		t.Errorf("после захвата: %+v", task)
	}
}

func TestClaimWithoutWorkingStatusKeepsColumn(t *testing.T) {
	for name, working := range map[string]string{"рабочего статуса нет": "", "рабочий = текущий": "Ready"} {
		t.Run(name, func(t *testing.T) {
			tr, fake := fixture(t)
			req := claimReq("run-1")
			req.WorkingStatus = working
			if err := tr.Claim(req); err != nil {
				t.Fatal(err)
			}
			if _, moved := fake.puts[0]["columnId"]; moved {
				t.Errorf("колонка тронута: %#v", fake.puts[0])
			}
			if fake.task(testKey).ColumnID != colReady {
				t.Error("задача уехала из Ready")
			}
		})
	}
}

// Spec: «Claiming an already-owned live lease fails» — и ничего не пишет.
func TestClaimRefusesLiveLease(t *testing.T) {
	tr, fake := fixture(t)
	fake.setLease(testKey, "run-other", now.Add(time.Minute))
	err := tr.Claim(claimReq("run-1"))
	if !errors.Is(err, tracker.ErrClaimLost) {
		t.Errorf("захват живой аренды дал %v", err)
	}
	if len(fake.puts) != 0 {
		t.Errorf("живая аренда перезаписана: %#v", fake.puts)
	}
	if task, _ := tr.Get(testKey); task.RunID != "run-other" {
		t.Errorf("владелец сменился: %q", task.RunID)
	}
}

func TestClaimTakesExpiredLease(t *testing.T) {
	tr, fake := fixture(t)
	fake.setLease(testKey, "run-dead", now.Add(-time.Minute))
	if err := tr.Claim(claimReq("run-1")); err != nil {
		t.Errorf("истёкшая аренда не отдана: %v", err)
	}
}

func TestClaimChecksExpectedStatus(t *testing.T) {
	tr, fake := fixture(t)
	fake.tasks[testKey].ColumnID = colReview
	if err := tr.Claim(claimReq("run-1")); !errors.Is(err, tracker.ErrClaimLost) {
		t.Errorf("захват из чужого статуса дал %v", err)
	}
	if len(fake.puts) != 0 {
		t.Error("записано, хотя статус не тот")
	}
}

// Spec: «A losing claimant is told it lost» — нашу запись перезаписали
// следом, перечитывание это видит.
func TestClaimLostWhenOverwrittenAfterWrite(t *testing.T) {
	tr, fake := fixture(t)
	fake.afterPut = func(id string) { fake.setLease(id, "run-winner", now.Add(time.Hour)) }
	if err := tr.Claim(claimReq("run-1")); !errors.Is(err, tracker.ErrClaimLost) {
		t.Errorf("проигранная гонка дала %v", err)
	}
}

func TestSecondClaimantLoses(t *testing.T) {
	tr, _ := fixture(t)
	if err := tr.Claim(claimReq("run-1")); err != nil {
		t.Fatal(err)
	}
	second := claimReq("run-2")
	second.ExpectStatus = "InProgress"
	if err := tr.Claim(second); !errors.Is(err, tracker.ErrClaimLost) {
		t.Errorf("второй захват дал %v", err)
	}
	if task, _ := tr.Get(testKey); task.RunID != "run-1" {
		t.Errorf("владелец: %q", task.RunID)
	}
}

// Review Focus #2.
func TestClaimKeepsForeignAPIDataAndCounters(t *testing.T) {
	tr, fake := fixture(t)
	fake.tasks[testKey].APIData = map[string]any{"crm": map[string]any{"deal": 7}, "attempts": 2, "human_wait": true}
	if err := tr.Claim(claimReq("run-1")); err != nil {
		t.Fatal(err)
	}
	data := fake.task(testKey).APIData
	if data["crm"] == nil || data["attempts"] != float64(2) || data["human_wait"] != true {
		t.Errorf("apiData после захвата: %#v", data)
	}
}

func TestClaimUnknownWorkingStatusWritesNothing(t *testing.T) {
	tr, fake := fixture(t)
	req := claimReq("run-1")
	req.WorkingStatus = "Nowhere"
	if err := tr.Claim(req); err == nil {
		t.Error("незнакомый рабочий статус принят")
	}
	if len(fake.puts) != 0 {
		t.Error("записано при незнакомом рабочем статусе")
	}
}

func TestRenewExtendsOwnLiveLease(t *testing.T) {
	tr, fake := fixture(t)
	fake.setLease(testKey, "run-1", now.Add(time.Minute))
	later := now.Add(time.Hour)
	if err := tr.Renew(testKey, "run-1", later); err != nil {
		t.Fatal(err)
	}
	task, _ := tr.Get(testKey)
	if !task.LeaseUntil.Equal(later) || task.RunID != "run-1" || task.Owner != "someone" {
		t.Errorf("после продления: %+v", task)
	}
	if fake.count("GET /api-v2/chats/") != 1 { // только Get из самого теста
		t.Error("проверка владения тянула переписку — лишний запрос под rate limit")
	}
}

// Spec: «Renewing an expired lease fails» — и аренда остаётся как была.
func TestRenewRefusesExpiredLease(t *testing.T) {
	tr, fake := fixture(t)
	fake.setLease(testKey, "run-1", now.Add(-time.Minute))
	if err := tr.Renew(testKey, "run-1", now.Add(time.Hour)); !errors.Is(err, tracker.ErrNotOwner) {
		t.Errorf("продление истёкшей дало %v", err)
	}
	if len(fake.puts) != 0 {
		t.Error("истёкшая аренда переписана")
	}
}

func TestRenewRefusesForeignLease(t *testing.T) {
	tr, fake := fixture(t)
	fake.setLease(testKey, "run-other", now.Add(time.Minute))
	if err := tr.Renew(testKey, "run-1", now.Add(time.Hour)); !errors.Is(err, tracker.ErrNotOwner) {
		t.Errorf("продление чужой дало %v", err)
	}
}
