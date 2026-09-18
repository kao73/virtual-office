package office

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

// fixture — маленькая поставка в форме embed: базовый слой под подчёркиванием,
// исполняемый хук, кит под bootstrap/ и обычные файлы.
func fixture() fstest.MapFS {
	return fstest.MapFS{
		"roles/_base/base.yaml":                     {Data: []byte("network: {}\n")},
		"hooks/require-result.sh":                   {Data: []byte("#!/bin/sh\nexit 0\n")},
		"skills/comet/.source.yaml":                 {Data: []byte("package: comet\n")},
		"bootstrap/sbx-kits/comet-cli/spec.yaml":    {Data: []byte("name: comet\n")},
		"bootstrap/sbx-kits/bake-comet-template.sh": {Data: []byte("#!/usr/bin/env bash\n")},
		"workflow.yaml":                             {Data: []byte("statuses: []\n")},
	}
}

// tempDirs — остатки .unpack-* в каталоге офисов: после любого исхода их
// быть не должно (кроме убитого процесса, которого тест не изображает).
// noTempDirs — распаковка за собой не оставила временных каталогов. Проверяют
// это все сценарии: осиротевший .unpack-* убирать некому.
func noTempDirs(t *testing.T, officeDir string) {
	t.Helper()
	found, err := filepath.Glob(filepath.Join(officeDir, tempPrefix+"*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Errorf("остались временные каталоги: %v", found)
	}
}

func TestUnpackLaysOutTreeWithModes(t *testing.T) {
	officeDir := filepath.Join(t.TempDir(), "office") // ещё не существует
	root, err := Unpack(fixture(), officeDir, "v0.7.0")
	if err != nil {
		t.Fatalf("не распаковано: %v", err)
	}
	if want := filepath.Join(officeDir, "v0.7.0"); root != want {
		t.Errorf("корень %s, ожидался %s", root, want)
	}
	for rel, exec := range map[string]bool{
		"roles/_base/base.yaml":           false,
		"hooks/require-result.sh":         true,
		"skills/comet/.source.yaml":       false,
		"sbx-kits/comet-cli/spec.yaml":    false, // bootstrap/ снят
		"sbx-kits/bake-comet-template.sh": true,
		"workflow.yaml":                   false,
	} {
		fi, err := os.Stat(filepath.Join(root, rel))
		if err != nil {
			t.Errorf("%s не распакован: %v", rel, err)
			continue
		}
		if got := fi.Mode().Perm()&0o111 != 0; got != exec {
			t.Errorf("%s исполняемый=%v, ожидалось %v", rel, got, exec)
		}
		if exec && fi.Mode().Perm() != 0o755 {
			t.Errorf("%s права %o, ожидались 0755", rel, fi.Mode().Perm())
		}
	}
	if _, err := os.Stat(filepath.Join(root, "bootstrap")); !errors.Is(err, fs.ErrNotExist) {
		t.Error("bootstrap/ остался в распакованном офисе")
	}
	noTempDirs(t, officeDir)
}

// Распакованная версия неприкосновенна: правка руками в ней законна.
func TestUnpackLeavesExistingOfficeAlone(t *testing.T) {
	officeDir := t.TempDir()
	root, err := Unpack(fixture(), officeDir, "v0.7.0")
	if err != nil {
		t.Fatalf("не распаковано: %v", err)
	}
	workflowPath := filepath.Join(root, "workflow.yaml")
	if err := os.WriteFile(workflowPath, []byte("правка руками\n"), 0o644); err != nil {
		t.Fatalf("правка не записана: %v", err)
	}
	root2, err := Unpack(fixture(), officeDir, "v0.7.0")
	if err != nil {
		t.Fatalf("повторная распаковка отказала: %v", err)
	}
	if root2 != root {
		t.Errorf("корень %s, ожидался %s", root2, root)
	}
	data, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("%s не прочитан: %v", workflowPath, err)
	}
	if string(data) != "правка руками\n" {
		t.Errorf("workflow.yaml = %q, правка руками затёрта", data)
	}
	noTempDirs(t, officeDir)
}

func TestUnpackVersionsSideBySide(t *testing.T) {
	officeDir := t.TempDir()
	rootOld, err := Unpack(fixture(), officeDir, "v0.7.0")
	if err != nil {
		t.Fatalf("v0.7.0 не распакована: %v", err)
	}
	rootNew, err := Unpack(fixture(), officeDir, "v0.8.0")
	if err != nil {
		t.Fatalf("v0.8.0 не распакована: %v", err)
	}
	for _, root := range []string{rootOld, rootNew} {
		if _, err := os.Stat(filepath.Join(root, "workflow.yaml")); err != nil {
			t.Errorf("%s: workflow.yaml не распакован: %v", root, err)
		}
	}
	if rootOld == rootNew {
		t.Errorf("v0.7.0 и v0.8.0 распакованы в один каталог: %s", rootOld)
	}
	noTempDirs(t, officeDir)
}

// failingFS ломает чтение одного файла: так выглядит распаковка, оборванная
// на середине. Оборачивает только Open — fs.ReadFile и fs.WalkDir тогда идут
// через него, а не через ReadFileFS/ReadDirFS самого MapFS.
type failingFS struct {
	fs.FS
	broken string
}

func (f failingFS) Open(name string) (fs.File, error) {
	if name == f.broken {
		return nil, errors.New("диск кончился")
	}
	return f.FS.Open(name)
}

func TestUnpackFailureLeavesNoTarget(t *testing.T) {
	officeDir := t.TempDir()
	_, err := Unpack(failingFS{FS: fixture(), broken: "workflow.yaml"}, officeDir, "v0.7.0")
	if err == nil || !strings.Contains(err.Error(), "диск кончился") {
		t.Fatalf("оборванная распаковка не названа: %v", err)
	}
	if _, err := os.Stat(filepath.Join(officeDir, "v0.7.0")); !errors.Is(err, fs.ErrNotExist) {
		t.Error("после отказа остался каталог офиса")
	}
	noTempDirs(t, officeDir)
	// Следующая попытка — с нуля, и она проходит.
	if _, err := Unpack(fixture(), officeDir, "v0.7.0"); err != nil {
		t.Fatalf("повторная распаковка после отказа: %v", err)
	}
}

// Два tick'а под cron распаковывают одновременно: каталог один, временных нет.
func TestUnpackRaceYieldsOneOffice(t *testing.T) {
	officeDir := t.TempDir()
	roots, errs := make([]string, 4), make([]error, 4)
	var wg sync.WaitGroup
	for i := range roots {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			roots[i], errs[i] = Unpack(fixture(), officeDir, "v0.7.0")
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("горутина %d: %v", i, err)
		}
	}
	for i, root := range roots {
		if root != roots[0] {
			t.Errorf("горутина %d вернула %s, ожидался %s", i, root, roots[0])
		}
	}
	if _, err := os.Stat(filepath.Join(roots[0], "workflow.yaml")); err != nil {
		t.Errorf("workflow.yaml не распакован: %v", err)
	}
	entries, err := os.ReadDir(officeDir)
	if err != nil {
		t.Fatalf("%s не прочитан: %v", officeDir, err)
	}
	if len(entries) != 1 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("в каталоге офисов %d записей, ожидалась одна: %v", len(entries), names)
	}
	noTempDirs(t, officeDir)
}
