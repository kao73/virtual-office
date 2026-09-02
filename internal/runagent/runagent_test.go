package runagent

import (
	"errors"
	"fmt"
	"testing"

	"github.com/kao73/virtual-office/internal/backends/sbx"
)

// BLOCKER-находка независимого ревью: result.json дочитан успешно, но
// backend run() всё равно вернул sbx.ErrCloneSyncIncomplete (--clone
// синхронизация вышла из песочницы не целиком — например,
// syncCometState/commitLeftovers упали, — хотя .agent Dirs-цикл его всё
// равно принёс) — раньше Execute такую ошибку теряла целиком, и настоящая
// инфраструктурная беда репортилась в тикет как чистый успех. syncErr —
// то самое место в Execute, которое эту ошибку обязано прокинуть дальше.
func TestSyncErrSurfacesRunErrWhenResultWasStillRead(t *testing.T) {
	runErr := fmt.Errorf("работа агента %w office-x: comet-state.yaml не подтянут",
		sbx.ErrCloneSyncIncomplete)

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

// BLOCKER-находка независимого ревью (round 2): cloneOutcome отдаёт runErr
// тем же каналом ещё на двух путях, не связанных с --clone синком, —
// усечённый по таймауту прогон и незапустившийся exec. Оба, если result.json
// всё же дочитан, — законный, уже разобранный правилами роли исход
// («усечённый прогон не начинают заново»), а не незавершённая
// синхронизация. syncErr обязана хоронить именно sbx.ErrCloneSyncIncomplete,
// а не любую непустую runErr.
func TestSyncErrIgnoresNonCloneSyncErrors(t *testing.T) {
	timeoutErr := errors.New("песочница office-x: контекст роли истёк")
	if err := syncErr(true, timeoutErr); err != nil {
		t.Errorf("syncErr = %v, ожидался nil — усечённый по таймауту прогон с результатом не хоронят", err)
	}

	execErr := errors.New("агент не запущен в песочнице office-x: exec: \"sbx\": file not found")
	if err := syncErr(true, execErr); err != nil {
		t.Errorf("syncErr = %v, ожидался nil для незапустившегося exec без ErrCloneSyncIncomplete", err)
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
