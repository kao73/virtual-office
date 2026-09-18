package main

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kao73/virtual-office/internal/runner"
)

// version отвечает и на машине, где хозяйства ещё нет: ничего не распаковывает
// и не открывает — только считает, где лежал бы офис этой версии.
func TestVersionPrintsIdentityAndOfficeDirWithoutUnpacking(t *testing.T) {
	home := filepath.Join(t.TempDir(), "office-home") // не существует
	t.Setenv(runner.HomeEnv, home)
	t.Setenv(runner.ConfigRootEnv, "")
	releaseVersion(t, "v0.7.0")
	var out bytes.Buffer
	if err := versionCommand(nil, &out); err != nil {
		t.Fatalf("version отказал: %v", err)
	}
	want := "runner v0.7.0\nофис: " + filepath.Join(home, runner.OfficeDir, "v0.7.0") + "\n"
	if out.String() != want {
		t.Errorf("напечатано %q, ожидалось %q", out.String(), want)
	}
	if _, err := os.Stat(home); !errors.Is(err, fs.ErrNotExist) {
		t.Error("version завёл хозяйство или распаковал офис")
	}
}

// versionLinePattern — вся первая строка вывода: "runner " + личность клона
// (HEAD, 40 hex, с возможным суффиксом -dirty). Корень фикстуры грязен по
// построению: fixtureRunner пишет workflow.yaml до git init, файл остаётся
// неотслеживаемым, и git status --porcelain видит его как правку — тест не
// держит более строгий вид без -dirty.
func commitVersionLine(line string) bool {
	identity, ok := strings.CutPrefix(line, "runner ")
	return ok && runner.IsCommitIdentity(identity)
}

// В режиме клона — commit клона и его путь, с пометкой, откуда он взялся.
func TestVersionInCloneModeNamesTheClone(t *testing.T) {
	root, _ := fixtureRunner(t, mockProject)
	var out bytes.Buffer
	if err := versionCommand(nil, &out); err != nil {
		t.Fatalf("version отказал: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("строк напечатано %d, ожидалось 2:\n%s", len(lines), out.String())
	}
	if !commitVersionLine(lines[0]) {
		t.Errorf("первая строка %q не похожа на identity клона", lines[0])
	}
	wantSecond := "офис: " + root + " (клон, OFFICE_CONFIG_ROOT)"
	if lines[1] != wantSecond {
		t.Errorf("вторая строка %q, ожидалось %q", lines[1], wantSecond)
	}
}
