package yougile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/kao73/virtual-office/internal/tracker"
)

// ErrOfficeData — данные офиса в apiData задачи не читаются: apiData не
// объект, virtual_office не объект, не те типы полей, незнакомое поле или
// версия схемы не 1 (новее этой или явно нулевая/отрицательная). Такую
// задачу адаптер не перезаписывает никогда. Листинги её пропускают с записью
// в Logf (status.go, collect), остальные пути падают громко (design doc §2).
var ErrOfficeData = errors.New("yougile: данные офиса в apiData не читаются")

// errAPIDataNotObject уточняет ErrOfficeData: apiData верхнего уровня не
// объект, virtual_office в нём нет вовсе. Для записи это тот же отказ, а
// FindByMarker такую карточку пропускает: нашей метки в ней быть не может
// (comment.go). Битый или новый virtual_office этим не помечается.
var errAPIDataNotObject = errors.New("apiData не JSON-объект")

// keyNamespace — единственный ключ верхнего уровня apiData, которым владеет
// офис. Всё остальное в apiData — чужое, в том числе ключи верхнего уровня,
// оставшиеся от change 1 на office-polygon: они не читаются и не мигрируются.
const keyNamespace = "virtual_office"

// schemaVersion — версия схемы virtual_office, которую понимает адаптер.
// Отсутствующая v читается как 1; любая другая — ErrOfficeData.
const schemaVersion = 1

// leaseData — аренда: пишется и снимается целиком.
type leaseData struct {
	Owner      string    `json:"owner"`
	RunID      string    `json:"run_id"`
	LeaseUntil time.Time `json:"lease_until"`
}

// officeData — virtual_office на проводе. Без omitempty: encode пишет все
// ключи явно — lease null'ом, списки пустыми, — чтобы слияние apiData на
// сервере, если оно там есть, не оставило старых значений.
type officeData struct {
	V         int        `json:"v"`
	Lease     *leaseData `json:"lease"`
	Attempts  int        `json:"attempts"`
	HumanWait bool       `json:"human_wait"`
	Labels    []string   `json:"labels"`
	DependsOn []string   `json:"depends_on"`
}

// apiData — наш взгляд на apiData задачи. attempts и human_wait живут вне
// lease: Release их не трогает, как jira не трогает attempts и метку человека.
//
// Инвариант каждой записи: apiData читается целиком, меняется и пишется
// целиком. extra держит все ключи верхнего уровня, кроме virtual_office, —
// без него запись молча стирала бы чужие данные.
type apiData struct {
	Lease     *leaseData
	Attempts  int
	HumanWait bool
	Labels    []string
	// DependsOn — id задач YouGile, от которых зависит эта (LinkDependsOn).
	DependsOn []string
	extra     map[string]json.RawMessage
}

// decodeAPIData разбирает apiData. Пусто, null или нет virtual_office —
// нулевое значение: задача, которую офис ни разу не трогал, свободна.
func decodeAPIData(raw json.RawMessage) (apiData, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return apiData{}, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil {
		return apiData{}, fmt.Errorf("%w: %w: %v", ErrOfficeData, errAPIDataNotObject, err)
	}

	var d apiData
	if ns, ok := fields[keyNamespace]; ok {
		delete(fields, keyNamespace)
		od, err := decodeOffice(ns)
		if err != nil {
			return apiData{}, err
		}
		d = apiData{Lease: od.Lease, Attempts: od.Attempts, HumanWait: od.HumanWait, Labels: od.Labels, DependsOn: od.DependsOn}
	}
	if len(fields) > 0 {
		d.extra = fields
	}
	return d, nil
}

// decodeOffice разбирает virtual_office строго: только объект, только
// известные поля известных типов. Версию смотрим первой и отдельно — у
// новой схемы поля могут быть другими, и отказ должен назвать версию,
// а не чужое поле. Нормальна только отсутствующая v или v=1.
func decodeOffice(raw json.RawMessage) (officeData, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return officeData{}, fmt.Errorf("%w: %s — не JSON-объект", ErrOfficeData, keyNamespace)
	}
	var head struct {
		V *int `json:"v"`
	}
	if err := json.Unmarshal(trimmed, &head); err != nil {
		return officeData{}, fmt.Errorf("%w: %s.v: %v", ErrOfficeData, keyNamespace, err)
	}
	if head.V != nil && *head.V != schemaVersion {
		return officeData{}, fmt.Errorf("%w: %s.v=%d, адаптер знает только версию %d — не перезаписываем",
			ErrOfficeData, keyNamespace, *head.V, schemaVersion)
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	var od officeData
	if err := dec.Decode(&od); err != nil {
		return officeData{}, fmt.Errorf("%w: %s: %v", ErrOfficeData, keyNamespace, err)
	}
	return od, nil
}

// encode — объект целиком для PUT: чужие ключи как были, virtual_office —
// весь и явно.
func (d apiData) encode() map[string]any {
	out := make(map[string]any, len(d.extra)+1)
	for key, value := range d.extra {
		out[key] = value
	}
	out[keyNamespace] = officeData{
		V: schemaVersion, Lease: d.Lease, Attempts: d.Attempts, HumanWait: d.HumanWait,
		Labels:    orEmpty(d.Labels),
		DependsOn: orEmpty(d.DependsOn),
	}
	return out
}

// orEmpty — nil-срез как [], а не null: пустой список пишется списком.
func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Claim — захват: записать аренду (и рабочую колонку) одним PUT, перечитать,
// сверить владельца.
//
// CAS в YouGile нет, как и в JIRA. Сверка закрывает половину гонки — ту, где
// нашу запись затёрли после нас: мы честно проигрываем. Зеркальную — оба
// прочли задачу свободной до чьей-либо записи — не закрывает ничто; см.
// доккомент jira.Claim. Живую чужую аренду, видимую при чтении, захват не
// перезаписывает; в зеркальной гонке второй пишущий затирает первого.
func (t *Tracker) Claim(req tracker.ClaimRequest) error {
	if req.RunID == "" {
		return errors.New("yougile: захват без run_id — сверять владельца будет не с чем")
	}
	var working string
	if req.WorkingStatus != "" {
		var err error
		if working, err = t.columnFor(req.WorkingStatus); err != nil {
			return err
		}
	}

	raw, task, data, err := t.load(req.Key)
	if err != nil {
		return err
	}
	switch {
	case raw.Archived:
		// Заархивировали между ListReady и захватом: на доске карточки не
		// видно, и работать по ней нельзя. Проверка здесь, а не в getRaw:
		// Get и Release архивной задачи должны оставаться рабочими.
		//
		// Аренду архивной карточки reaper не снимет: ListExpired, как и
		// ListReady, архив отсеивает (tasksInColumn; отдаёт его только List). Истёкшая аренда захвату
		// потом не мешает, так что висит она безвредно — принято, не чинится.
		return fmt.Errorf("%w: %s в архиве", tracker.ErrClaimLost, req.Key)
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

	_, fresh, _, err := t.load(req.Key)
	if err != nil {
		return err
	}
	if fresh.RunID != req.RunID {
		return fmt.Errorf("%w: после захвата %s владеет %s", tracker.ErrClaimLost, req.Key, fresh.RunID)
	}
	// Аренда и колонка ушли одним PUT, но принял ли сервер колонку, видно
	// только здесь. Не сдвинулась — захват не удался, как у jira.Claim
	// при отказе перевода. Аренда наша, и работать мы не будем — снимаем
	// её сразу, чтобы задача не выпала из очереди до истечения. Не снялась —
	// её снимет reaper; в ответ идёт исходная беда.
	if working != "" && fresh.Status != req.WorkingStatus {
		_ = t.Release(req.Key, tracker.ByRun(req.RunID))
		return fmt.Errorf("yougile: %s захвачена, но осталась в статусе %q вместо %q",
			req.Key, fresh.Status, req.WorkingStatus)
	}
	return nil
}

// Renew продлевает свою живую аренду. Истёкшую продлевать поздно: её уже мог
// забрать другой — CheckOwner откажет ErrNotOwner.
func (t *Tracker) Renew(key, runID string, leaseUntil time.Time) error {
	return t.mutateAPIData(key, tracker.ByRun(runID), func(d *apiData) {
		if d.Lease != nil { // CheckOwner уже гарантировал живую аренду этого прогона
			d.Lease.LeaseUntil = leaseUntil
		}
	})
}

// Release снимает аренду, не трогая колонку, attempts, human_wait и чужие
// ключи apiData. Снимает и сам прогон, и reaper — системной операцией
// над чужой истёкшей арендой.
func (t *Tracker) Release(key string, by tracker.Actor) error {
	return t.mutateAPIData(key, by, func(d *apiData) { d.Lease = nil })
}

// SetHumanFlag — атрибут «ждёт человека» в apiData.human_wait. Право —
// общее tracker.CheckOwner: прогон ставит его под своей живой арендой,
// системная операция — на задаче без живой аренды. Вне lease флаг лежит,
// чтобы Release его не снимал.
func (t *Tracker) SetHumanFlag(key string, by tracker.Actor, on bool) error {
	return t.mutateAPIData(key, by, func(d *apiData) { d.HumanWait = on })
}

// SetAttempts — счётчик попыток в apiData.attempts.
func (t *Tracker) SetAttempts(key string, by tracker.Actor, n int) error {
	return t.mutateAPIData(key, by, func(d *apiData) { d.Attempts = n })
}
