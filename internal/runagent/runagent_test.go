package runagent

import (
	"errors"
	"testing"
)

// BLOCKER-находка независимого ревью: result.json дочитан успешно, но
// backend run() всё равно вернул ошибку (на sbx — cloneErr из-за упавшей
// synchronizации --clone, например syncCometState/commitLeftovers, при этом
// .agent-цикл её всё равно принёс) — раньше Execute такую ошибку теряла
// целиком, и настоящая инфраструктурная беда репортилась в тикет как чистый
// успех. syncErr — то самое место в Execute, которое эту ошибку обязано
// прокинуть дальше.
func TestSyncErrSurfacesRunErrWhenResultWasStillRead(t *testing.T) {
	runErr := errors.New("песочница office-x: длящееся состояние Comet Native не подтянуто")

	err := syncErr(true, runErr)
	if err == nil {
		t.Fatal("runErr потерян, хотя result.json дочитан успешно")
	}
	if !errors.Is(err, runErr) {
		t.Errorf("syncErr не оборачивает исходный runErr: %v", err)
	}
	// Типизированная обёртка, а не голая fmt.Errorf: SandboxAgent.Run
	// (internal/pipeline/agent.go) обязана отличать эту ошибку от неудачи
	// runner.Archive, которая возвращается тем же общим путём (Outcome уже
	// заполнен, err не nil), но не теряет ничего — независимое ревью.
	var syncIncomplete *ErrSyncIncomplete
	if !errors.As(err, &syncIncomplete) {
		t.Errorf("syncErr не оборачивает в *ErrSyncIncomplete: %v (%T)", err, err)
	}
}

// Без результата (result.json не дочитан) — reason уже несёт текст runErr
// внутри FailedResult (см. Execute), и оборачивать его здесь второй раз
// незачем: нет результата, нет и этой ошибки.
func TestSyncErrNoopWithoutResult(t *testing.T) {
	if err := syncErr(false, errors.New("agent process failed")); err != nil {
		t.Errorf("syncErr = %v, ожидался nil без result.json — reason уже несёт runErr", err)
	}
}

// Здоровый прогон: result.json дочитан, run() без ошибки — молчание.
func TestSyncErrNoopOnCleanRun(t *testing.T) {
	if err := syncErr(true, nil); err != nil {
		t.Errorf("syncErr = %v, ожидался nil на здоровом прогоне", err)
	}
}
