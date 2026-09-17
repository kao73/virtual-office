package office

import (
	"testing"
	"testing/fstest"
)

// Хеш поставки должен различать две грязные сборки одного commit: другой
// байт или другой путь — другой каталог распаковки (D3).
func TestHashIsStableAndSensitive(t *testing.T) {
	a, err := Hash(fixture())
	if err != nil {
		t.Fatalf("не хешируется: %v", err)
	}
	if len(a) != 64 {
		t.Errorf("длина хеша %d, ожидалось 64", len(a))
	}
	b, err := Hash(fixture())
	if err != nil {
		t.Fatalf("не хешируется: %v", err)
	}
	if b != a {
		t.Errorf("одна и та же поставка дала разные хеши: %s != %s", b, a)
	}
	changed := fixture()
	changed["workflow.yaml"] = &fstest.MapFile{Data: []byte("statuses: [x]\n")}
	c, err := Hash(changed)
	if err != nil {
		t.Fatalf("не хешируется: %v", err)
	}
	if c == a {
		t.Error("другой байт содержимого не изменил хеш")
	}
	moved := fixture()
	moved["renamed.yaml"] = moved["workflow.yaml"]
	delete(moved, "workflow.yaml")
	d, err := Hash(moved)
	if err != nil {
		t.Fatalf("не хешируется: %v", err)
	}
	if d == a {
		t.Error("переименование файла не изменило хеш")
	}
	if _, err := Hash(failingFS{FS: fixture(), broken: "workflow.yaml"}); err == nil {
		t.Error("недочитанная поставка получила хеш")
	}
}
