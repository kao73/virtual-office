//go:build unix

package office

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Строгий umask не должен просачиваться в разложенный офис: иначе каталоги
// выходят 0700, и чужой uid — песочница или второй пользователь общего
// ${OFFICE_HOME} — не войдёт в них, хотя файлы внутри читаемы. Ожидание
// берётся из фикстуры (shebang → 0755), а не из того, что получилось:
// иначе тест согласился бы и с потерянным битом исполняемости.
func TestUnpackIgnoresUmask(t *testing.T) {
	prev := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(prev) })

	src := fixture()
	officeDir := filepath.Join(t.TempDir(), "office")
	root, err := Unpack(src, officeDir, "v0.7.0")
	if err != nil {
		t.Fatalf("не распаковано: %v", err)
	}

	// Каталог версий — родитель распакованного: 0700 на нём закрыл бы офис
	// не хуже, чем 0700 внутри.
	if fi, err := os.Stat(officeDir); err != nil {
		t.Fatalf("каталог офисов не прочитан: %v", err)
	} else if got := fi.Mode().Perm(); got != 0o755 {
		t.Errorf("%s: права %o, ожидались 755", officeDir, got)
	}

	want := map[string]fs.FileMode{}
	if err := walkFiles(src, func(path string, data []byte) error {
		mode := fs.FileMode(0o644)
		if bytes.HasPrefix(data, []byte("#!")) {
			mode = 0o755
		}
		want[filepath.FromSlash(strings.TrimPrefix(path, bootstrapPrefix))] = mode
		return nil
	}); err != nil {
		t.Fatalf("фикстура не обойдена: %v", err)
	}

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			if got := fi.Mode().Perm(); got != 0o755 {
				t.Errorf("каталог %s: права %o, ожидались 755", path, got)
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		expected, ok := want[rel]
		if !ok {
			t.Errorf("%s распакован, но такого файла нет в поставке", rel)
			return nil
		}
		if got := fi.Mode().Perm(); got != expected {
			t.Errorf("%s: права %o, по поставке ожидались %o", rel, got, expected)
		}
		delete(want, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("обход не удался: %v", err)
	}
	for rel := range want {
		t.Errorf("%s не распакован", rel)
	}
}
