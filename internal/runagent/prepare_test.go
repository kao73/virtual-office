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
