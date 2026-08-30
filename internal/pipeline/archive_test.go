package pipeline

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeComet подкладывает на PATH подложный `comet`: на "native status ... --json"
// печатает {"phase": phase}, на "native archive ..." создаёт файл-метку и
// возвращает её путь. Он предваряет системный PATH, а не заменяет его: git,
// которым archiveIfReady пользуется через Ensure/Push, обязан остаться
// доступным.
func fakeComet(t *testing.T, phase string) (archivedMarker string) {
	t.Helper()
	binDir := t.TempDir()
	archivedMarker = filepath.Join(t.TempDir(), "archived")

	script := "#!/bin/sh\n" +
		"case \"$2\" in\n" +
		"  status) echo \"{\\\"phase\\\":\\\"" + phase + "\\\"}\" ;;\n" +
		"  archive) : > \"" + archivedMarker + "\" ;;\n" +
		"  *) exit 1 ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(binDir, "comet"), []byte(script), 0o755); err != nil {
		t.Fatalf("подложный comet не записан: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return archivedMarker
}

func TestArchiveIfReadyRunsArchiveWhenPhaseMatches(t *testing.T) {
	o := newOffice(t)
	marker := fakeComet(t, archiveReadyPhase)
	task := o.approved(t, "OFF-1")

	ok, err := o.archiveIfReady(task, o.Projects["OFF"])
	if err != nil || !ok {
		t.Fatalf("archiveIfReady = %v, %v", ok, err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("comet native archive не был вызван: %v", err)
	}
}

func TestArchiveIfReadySkipsWhenPhaseNotReady(t *testing.T) {
	o := newOffice(t)
	marker := fakeComet(t, "verify")
	task := o.approved(t, "OFF-1")

	ok, err := o.archiveIfReady(task, o.Projects["OFF"])
	if err != nil || !ok {
		t.Fatalf("archiveIfReady = %v, %v", ok, err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("comet native archive вызван, хотя фаза не archive-ready")
	}
}

// Отсутствие comet на этой машине — известный, ещё не решённый пробел
// (tasks.md 1.1), а не повод никогда не открывать pull request.
func TestArchiveIfReadyIsNonBlockingWithoutComet(t *testing.T) {
	o := newOffice(t)
	old := cometExecutable
	cometExecutable = "comet-not-installed-in-tests"
	t.Cleanup(func() { cometExecutable = old })
	task := o.approved(t, "OFF-1")

	ok, err := o.archiveIfReady(task, o.Projects["OFF"])
	if err != nil || !ok {
		t.Fatalf("отсутствие comet не должно блокировать PR-проход: ok=%v, err=%v", ok, err)
	}
}

// Рабочая папка занята — единственный случай, где archiveIfReady просит
// openPR подождать следующего прохода, а не открывать PR без архивирования.
func TestArchiveIfReadyDefersWhenWorktreeBusy(t *testing.T) {
	o := newOffice(t)
	task := o.approved(t, "OFF-1")
	project := o.Projects["OFF"]

	ws, err := o.Workspaces.Ensure(task.Ref(), project)
	if err != nil {
		t.Fatalf("рабочая папка не занята для теста: %v", err)
	}
	defer ws.Unlock()

	ok, err := o.archiveIfReady(task, project)
	if err != nil {
		t.Fatalf("archiveIfReady вернул ошибку вместо мягкого отказа: %v", err)
	}
	if ok {
		t.Error("archiveIfReady должен был отступить: рабочая папка занята")
	}
}
