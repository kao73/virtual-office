package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// withLookPath подменяет lookPath на предсказуемый список инструментов — без
// этого тест зависел бы от того, что установлено на машине, где его гоняют.
func withLookPath(t *testing.T, present ...string) {
	t.Helper()
	set := map[string]bool{}
	for _, name := range present {
		set[name] = true
	}
	prev := lookPath
	lookPath = func(file string) (string, error) {
		if set[file] {
			return "/usr/bin/" + file, nil
		}
		return "", exec.ErrNotFound
	}
	t.Cleanup(func() { lookPath = prev })
}

// findingPrefix — начало строки, которую печатает concludeExit для находки
// с данным уровнем и check-id. Используется вместо руками посчитанных
// пробелов и не зависит от того, что именно написано в msg.
func findingPrefix(level, check string) string {
	return fmt.Sprintf("%-4s %-28s", level, check)
}

func TestDoctorReportsToolPresence(t *testing.T) {
	withLookPath(t, "git", "claude")
	fixtureRunner(t, mockProject)

	var out bytes.Buffer
	if err := doctorCommand(nil, &out); err != nil {
		t.Fatalf("доктор отказал на исправной машине: %v\n%s", err, out.String())
	}
	for _, want := range []string{findingPrefix("ok", "tool:git"), findingPrefix("ok", "tool:claude")} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("нет строки %q:\n%s", want, out.String())
		}
	}
}

func TestDoctorNamesEachMissingToolWithoutStoppingAtFirst(t *testing.T) {
	withLookPath(t /* ни одного инструмента */)
	fixtureRunner(t, mockProject)

	var out bytes.Buffer
	err := doctorCommand(nil, &out)
	if err == nil {
		t.Fatal("отсутствующие инструменты не провалили доктора")
	}
	for _, want := range []string{findingPrefix("fail", "tool:git"), findingPrefix("fail", "tool:claude")} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("нет строки %q:\n%s", want, out.String())
		}
	}
}

func TestDoctorChecksSbxToolOnlyOnSbxBackend(t *testing.T) {
	withLookPath(t, "git", "claude") // sbx отсутствует
	fixtureRunner(t, mockProject)

	var outLocal bytes.Buffer
	if err := doctorCommand([]string{"--backend", "local"}, &outLocal); err != nil {
		t.Fatalf("local не должен спрашивать sbx: %v\n%s", err, outLocal.String())
	}
	if strings.Contains(outLocal.String(), "tool:sbx") {
		t.Errorf("local всё равно упомянул sbx:\n%s", outLocal.String())
	}

	var outSbx bytes.Buffer
	err := doctorCommand([]string{"--backend", "sbx"}, &outSbx)
	if err == nil || !strings.Contains(outSbx.String(), findingPrefix("fail", "tool:sbx")) {
		t.Errorf("sbx-бэкенд не проверил отсутствующий sbx: %v\n%s", err, outSbx.String())
	}
}
