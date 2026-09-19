package office

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// payloadDirs и payloadFiles повторяют директиву embed в payload.go нарочно:
// разошлись — тест скажет.
var (
	payloadDirs  = []string{"roles", "skills", "hooks", "sbx-kits"}
	payloadFiles = []string{"workflow.yaml", "budgets.yaml", "tracker.example.yaml", "projects.local.example.yaml"}
)

// walkDisk обходит файлы каталога поставки на диске.
func walkDisk(t *testing.T, dir string, fn func(path string, info fs.FileInfo)) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		fn(path, info)
		return nil
	})
	if err != nil {
		t.Fatalf("%s не обойдён: %v", dir, err)
	}
}

// Каждый файл на диске под каталогами поставки обязан быть в Payload. Ловит
// пропавший префикс all:: без него roles/_base и .source.yaml выпадают из
// бинарника молча, и роль без базового слоя откажет уже у пользователя.
func TestPayloadCarriesEveryFileOnDisk(t *testing.T) {
	for _, dir := range payloadDirs {
		walkDisk(t, dir, func(path string, _ fs.FileInfo) {
			if _, err := fs.Stat(Payload, filepath.ToSlash(path)); err != nil {
				t.Errorf("%s есть на диске, но не в Payload: %v", path, err)
			}
		})
	}
	for _, name := range payloadFiles {
		if _, err := fs.Stat(Payload, name); err != nil {
			t.Errorf("%s не в Payload: %v", name, err)
		}
	}
}

// Поимённо — то, без чего офис не работает: обход выше прошёл бы молча,
// пропади файл с диска вовсе.
func TestPayloadNamesTheEssentials(t *testing.T) {
	want := []string{
		"roles/_base/base.yaml", "roles/_base/base.md",
		"hooks/require-result.sh", "hooks/debug-env.sh",
		"skills/comet/scripts/comet-hook-router.mjs",
		"sbx-kits/bake-comet-template.sh", "sbx-kits/comet-cli/spec.yaml",
	}
	roles, err := os.ReadDir("roles")
	if err != nil {
		t.Fatalf("roles/ не прочитан: %v", err)
	}
	shipped := 0
	for _, r := range roles {
		if !r.IsDir() || r.Name() == "_base" {
			continue
		}
		shipped++
		want = append(want, "roles/"+r.Name()+"/role.yaml", "roles/"+r.Name()+"/role.md")
	}
	if shipped < 3 {
		t.Errorf("ролей на диске %d, ожидалось не меньше трёх", shipped)
	}
	for _, name := range want {
		if _, err := fs.Stat(Payload, name); err != nil {
			t.Errorf("%s не в Payload: %v", name, err)
		}
	}
}

// Бит исполняемости восстанавливает распаковка по shebang (D2): файл с битом
// и без «#!» вышел бы из бинарника обычным, и хук с кодом 126 перестал бы
// ограждать молча. Ловится там, где лежит поставка.
func TestExecutablePayloadFilesStartWithShebang(t *testing.T) {
	for _, dir := range payloadDirs {
		walkDisk(t, dir, func(path string, info fs.FileInfo) {
			if info.Mode()&0o111 == 0 {
				return
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%s не прочитан: %v", path, err)
			}
			if !bytes.HasPrefix(raw, []byte("#!")) {
				t.Errorf("%s исполняемый, но без shebang: после распаковки бит пропадёт", path)
			}
		})
	}
}

// Поставка и каталог офиса в клоне — одно дерево: путь внутри embed равен
// пути внутри office/. Это и есть смысл переезда: распаковка больше ничего
// не переименовывает, а значит «роль лежит там же, где лежала» — свойство,
// а не обещание. Go-файлы пакета и ограждения в поставку не входят.
func TestPayloadMirrorsOfficeDirectory(t *testing.T) {
	skip := func(path string) bool {
		return strings.HasSuffix(path, ".go") ||
			path == "validators" || strings.HasPrefix(path, "validators/")
	}

	inPayload := map[string]bool{}
	err := fs.WalkDir(Payload, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		inPayload[path] = true
		if _, err := os.Stat(filepath.FromSlash(path)); err != nil {
			t.Errorf("%s в поставке, но не на диске под office/: %v", path, err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("поставка не обойдена: %v", err)
	}

	err = filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(path)
		if skip(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !inPayload[rel] {
			t.Errorf("%s лежит в office/, но не едет в поставке", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("каталог офиса не обойдён: %v", err)
	}
}
