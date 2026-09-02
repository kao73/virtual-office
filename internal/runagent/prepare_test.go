package runagent

import (
	"strings"
	"testing"

	"github.com/kao73/virtual-office/internal/runner"
)

// --clone сам берёт первым Workspaces клон-источник и Mounts игнорирует
// технически — но до этой проверки ничто не мешало вызывающему выставить
// оба поля разом, и несовместимость держалась только на слово в трёх
// разных doc-комментариях (Options.Mounts, Options.Clone, sbx.createArgs).
// Prepare обязана отказать сама, раньше внешнего `sbx create --clone`.
func TestPrepareRejectsCloneWithMounts(t *testing.T) {
	_, err := Prepare(Options{
		Clone:  &runner.CloneSync{FetchInto: "/tmp/x", Branch: "task-1"},
		Mounts: []runner.Workspace{{Path: "/tmp/bare.git"}},
	})
	if err == nil {
		t.Fatal("Clone вместе с Mounts принято без ошибки")
	}
	if !strings.Contains(err.Error(), "Mounts") {
		t.Errorf("ошибка не называет причину: %v", err)
	}
}

// Задача 7 (docs/superpowers/specs/2026-09-02-pipeline-clone-wiring-design.md):
// под --clone opts.Workdir — одноразовый клон-источник, который cloneSyncOut
// (internal/backends/sbx/clone.go) не трогает вовсе — всё послепрогонное
// (.agent/result.json, git-коммиты через fetchBranch) уезжает в
// opts.Clone.FetchInto, настоящую рабочую папку задачи.
func TestResultWorkdirUsesFetchIntoWhenCloneSet(t *testing.T) {
	opts := Options{
		Workdir: "/tmp/disposable-clone-source",
		Clone:   &runner.CloneSync{FetchInto: "/tmp/real-worktree"},
	}
	if got := resultWorkdir(opts); got != "/tmp/real-worktree" {
		t.Errorf("resultWorkdir = %q, ожидалось /tmp/real-worktree (Clone.FetchInto)", got)
	}
}

func TestResultWorkdirUsesWorkdirWithoutClone(t *testing.T) {
	opts := Options{Workdir: "/tmp/bind-mount-worktree"}
	if got := resultWorkdir(opts); got != "/tmp/bind-mount-worktree" {
		t.Errorf("resultWorkdir = %q, ожидалось /tmp/bind-mount-worktree (opts.Workdir, Clone нет)", got)
	}
}
