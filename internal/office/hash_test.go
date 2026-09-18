package office

import (
	"io/fs"
	"testing"
	"testing/fstest"
)

// hashOf — хеш или падение теста: проверяются свойства хеша, а не то, что
// фикстура читается.
func hashOf(t *testing.T, srcs ...fs.FS) string {
	t.Helper()
	h, err := Hash(srcs...)
	if err != nil {
		t.Fatalf("не хешируется: %v", err)
	}
	return h
}

// Хеш поставки должен различать две грязные сборки одного commit: другой
// байт или другой путь — другой каталог распаковки (D3).
func TestHashIsStableAndSensitive(t *testing.T) {
	a := hashOf(t, fixture())
	if len(a) != 64 {
		t.Errorf("длина хеша %d, ожидалось 64", len(a))
	}
	b := hashOf(t, fixture())
	if b != a {
		t.Errorf("одна и та же поставка дала разные хеши: %s != %s", b, a)
	}
	changed := fixture()
	changed["workflow.yaml"] = &fstest.MapFile{Data: []byte("statuses: [x]\n")}
	c := hashOf(t, changed)
	if c == a {
		t.Error("другой байт содержимого не изменил хеш")
	}
	moved := fixture()
	moved["renamed.yaml"] = moved["workflow.yaml"]
	delete(moved, "workflow.yaml")
	d := hashOf(t, moved)
	if d == a {
		t.Error("переименование файла не изменило хеш")
	}
	if _, err := Hash(failingFS{FS: fixture(), broken: "workflow.yaml"}); err == nil {
		t.Error("недочитанная поставка получила хеш")
	}
}

// Второе дерево — ограждения: их правка меняет хеш при той же поставке,
// а перенос файла между деревьями не сходится в один хеш.
func TestHashCoversEveryTree(t *testing.T) {
	validators := func(body string) fstest.MapFS {
		return fstest.MapFS{"payload/validators/validate-result-linux-arm64": {Data: []byte(body)}}
	}
	a := hashOf(t, fixture(), validators("v1"))
	b := hashOf(t, fixture(), validators("v2"))
	if a == b {
		t.Error("другое ограждение при той же поставке не изменило хеш")
	}
	only := hashOf(t, fixture())
	if only == a {
		t.Error("поставка без ограждений дала тот же хеш, что с ними")
	}
}

// Путь и длина в хеше — не украшение: без них два разных дерева с одинаковой
// склейкой байтов дали бы один ключ каталога, и вторая грязная сборка молча
// работала бы на офисе первой. Пара ниже отличается только границами.
func TestHashFramesPathAndLength(t *testing.T) {
	// Одинаковая склейка содержимого, разные длины: «x»+«yz» против «xy»+«z».
	split := hashOf(t, fstest.MapFS{"a": {Data: []byte("x")}, "b": {Data: []byte("yz")}})
	other := hashOf(t, fstest.MapFS{"a": {Data: []byte("xy")}, "b": {Data: []byte("z")}})
	if split == other {
		t.Error("деревья с одинаковой склейкой байтов, но разной разбивкой дали один хеш")
	}
	// Одно и то же содержимое под разными именами.
	named := hashOf(t, fstest.MapFS{"a": {Data: []byte("x")}})
	renamed := hashOf(t, fstest.MapFS{"b": {Data: []byte("x")}})
	if named == renamed {
		t.Error("один файл под разными именами дал один хеш: путь в хеш не попадает")
	}
}
