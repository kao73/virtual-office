package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const scopedRoleYAML = `name: tester
prompt: role.md
includes:
  - ../_base/base.md
skills: []
tools:
  allow: ["Read"]
hooks:
  stop:
    - hooks/require-result.sh
write_scope:
  dir: change_dir
  ignore: [".venv"]
guards:
  - change_dir_only
limits:
  max_turns: 5
  timeout_sec: 60
result_file: .agent/result.json
`

// scopedRole — роль, объявившая областью записи каталог изменения: такой раннер
// готовит артефакты по шаблонам.
func scopedRole(t *testing.T) Role {
	t.Helper()
	root := fixtureOffice(t, scopedRoleYAML)
	for _, name := range ChangeFiles() {
		path := filepath.Join(root, RolesDir, "tester", TemplatesDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("каталог шаблонов не создан: %v", err)
		}
		if err := os.WriteFile(path, []byte("# заготовка "+name+"\n"), 0o644); err != nil {
			t.Fatalf("шаблон %s не записан: %v", name, err)
		}
	}
	role, err := LoadRole(root, "tester")
	if err != nil {
		t.Fatalf("роль не загружена: %v", err)
	}
	return role
}

// Каталог изменения готовит раннер: у роли нет инструмента создания файлов вовсе,
// ей остаётся правка готовых.
func TestPrepareChangeDirCreatesFromTemplates(t *testing.T) {
	workdir := gitRepo(t)

	created, err := PrepareChangeDir(workdir, scopedRole(t), "OFF-1")
	if err != nil {
		t.Fatalf("каталог изменения не подготовлен: %v", err)
	}
	if len(created) != len(ChangeFiles()) {
		t.Fatalf("создано не всё: %v", created)
	}

	for _, name := range ChangeFiles() {
		body, err := os.ReadFile(filepath.Join(workdir, ChangeDirRel("OFF-1"), name))
		if err != nil {
			t.Fatalf("%s не создан: %v", name, err)
		}
		if !strings.Contains(string(body), "заготовка "+name) {
			t.Errorf("%s не из шаблона: %q", name, body)
		}
	}
}

// На втором прогоне по той же задаче в каталоге лежит работа прошлого: затирать
// её шаблоном нельзя.
func TestPrepareChangeDirKeepsExistingWork(t *testing.T) {
	workdir, role := gitRepo(t), scopedRole(t)
	dir := filepath.Join(workdir, ChangeDirRel("OFF-1"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("каталог не создан: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileTasks), []byte("- [ ] уже написано\n"), 0o644); err != nil {
		t.Fatalf("план не записан: %v", err)
	}

	created, err := PrepareChangeDir(workdir, role, "OFF-1")
	if err != nil {
		t.Fatalf("каталог изменения не подготовлен: %v", err)
	}
	if len(created) != len(ChangeFiles())-1 {
		t.Errorf("раннер тронул чужой файл: %v", created)
	}

	body, err := os.ReadFile(filepath.Join(dir, FileTasks))
	if err != nil || !strings.Contains(string(body), "уже написано") {
		t.Errorf("план перезаписан шаблоном: %q (%v)", body, err)
	}
}

// Роль без объявленной области записи каталога не получает: поведение следует
// из спецификации роли, а не из её имени.
func TestPrepareChangeDirSkipsRoleWithoutScope(t *testing.T) {
	workdir := gitRepo(t)

	created, err := PrepareChangeDir(workdir, fixtureRole(t), "OFF-1")
	if err != nil {
		t.Fatalf("подготовка не удалась: %v", err)
	}
	if len(created) != 0 {
		t.Errorf("роли без write_scope подготовлен каталог: %v", created)
	}
	if _, err := os.Stat(filepath.Join(workdir, ChangeDirRel("OFF-1"))); err == nil {
		t.Error("каталог изменения создан там, где его не просили")
	}
}

// Пустышки не должны пережить прогон: следующая роль прочитала бы три шаблона
// как план.
func TestSweepChangeDirRemovesUntouchedTemplates(t *testing.T) {
	workdir, role := gitRepo(t), scopedRole(t)
	created, err := PrepareChangeDir(workdir, role, "OFF-1")
	if err != nil {
		t.Fatalf("каталог изменения не подготовлен: %v", err)
	}

	if err := SweepChangeDir(workdir, role, created); err != nil {
		t.Fatalf("уборка не удалась: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workdir, ChangeDirRel("OFF-1"))); err == nil {
		t.Error("пустой каталог изменения пережил прогон")
	}
}

// Тронутое и закоммиченное уборка не трогает: в первом лежит работа, второе
// уже в истории.
func TestSweepChangeDirKeepsWork(t *testing.T) {
	workdir, role := gitRepo(t), scopedRole(t)
	created, err := PrepareChangeDir(workdir, role, "OFF-1")
	if err != nil {
		t.Fatalf("каталог изменения не подготовлен: %v", err)
	}
	dir := filepath.Join(workdir, ChangeDirRel("OFF-1"))

	if err := os.WriteFile(filepath.Join(dir, FileBrief), []byte("# Зачем\n\nПочинить оплату.\n"), 0o644); err != nil {
		t.Fatalf("brief не записан: %v", err)
	}
	git(t, workdir, "add", filepath.Join(ChangeDirRel("OFF-1"), FileTasks))
	git(t, workdir, "-c", "user.email=t@example.test", "-c", "user.name=test", "commit", "-q", "-m", "план")

	if err := SweepChangeDir(workdir, role, created); err != nil {
		t.Fatalf("уборка не удалась: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, FileBrief)); err != nil {
		t.Errorf("заполненный файл убран: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, FileTasks)); err != nil {
		t.Errorf("закоммиченный файл убран: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, FileDesign)); err == nil {
		t.Error("нетронутая заготовка пережила прогон")
	}
}

// Ключ задачи едет в путь, и приходит он снаружи: каталог не должен уезжать
// из проекта, а ручной прогон обязан выглядеть как работа.
func TestChangeDirRel(t *testing.T) {
	cases := map[string]string{
		"OFFICE-1":  "docs/changes/OFFICE-1",
		"":          "docs/changes/_manual",
		"../../etc": "docs/changes/.._.._etc",
	}
	for key, want := range cases {
		if got := ChangeDirRel(key); got != want {
			t.Errorf("ChangeDirRel(%q) = %q, ожидалось %q", key, got, want)
		}
	}
}
