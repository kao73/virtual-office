package main

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// version отвечает и на машине, где хозяйства ещё нет: ничего не распаковывает
// и не открывает — только считает, где лежал бы офис этой версии.
func TestVersionPrintsIdentityAndOfficeDirWithoutUnpacking(t *testing.T) {
	home := filepath.Join(t.TempDir(), "office-home") // не существует
	t.Setenv("OFFICE_HOME", home)
	t.Setenv("OFFICE_CONFIG_ROOT", "")
	releaseVersion(t, "v0.7.0")
	var out bytes.Buffer
	if err := versionCommand(nil, &out); err != nil {
		t.Fatalf("version отказал: %v", err)
	}
	want := "runner v0.7.0\nофис: " + filepath.Join(home, "office", "v0.7.0") + "\n"
	if out.String() != want {
		t.Errorf("напечатано %q, ожидалось %q", out.String(), want)
	}
	if _, err := os.Stat(home); !errors.Is(err, fs.ErrNotExist) {
		t.Error("version завёл хозяйство или распаковал офис")
	}
}

// configSHAWithDirtyPattern — личность клона: HEAD (40 hex), с возможным
// суффиксом -dirty. fixtureRunner оставляет workflow.yaml неотслеживаемым
// (см. Controller ruling R1 в office_test.go), так что личность выходит
// грязной — тест не держит более строгий вид без -dirty.
var configSHAWithDirtyPattern = regexp.MustCompile(`^runner [0-9a-f]{40}(-dirty)?$`)

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
	if !configSHAWithDirtyPattern.MatchString(lines[0]) {
		t.Errorf("первая строка %q не похожа на identity клона", lines[0])
	}
	wantSecond := "офис: " + root + " (клон, OFFICE_CONFIG_ROOT)"
	if lines[1] != wantSecond {
		t.Errorf("вторая строка %q, ожидалось %q", lines[1], wantSecond)
	}
}
