package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// prepareExchange кладёт в workdir каталог обмена с парой файлов — то, что
// раннер обязан унести в архив, прежде чем worktree будет удалён.
func prepareExchange(t *testing.T) string {
	t.Helper()
	workdir := t.TempDir()
	dir := filepath.Join(workdir, Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("каталог обмена не создан: %v", err)
	}
	for name, body := range map[string]string{
		FileResult: `{"outcome":"done","summary":"с.","next_owner":"none"}`,
		FileLog:    "строка лога\n",
		FileTask:   "постановка\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("%s не записан: %v", name, err)
		}
	}
	return workdir
}

// Каталог обмена живёт в worktree, а worktree на этапе 2 удаляются. Без копии
// result.json и run.log исчезнут вместе с ним, и разбирать прогон будет не по чему.
func TestArchiveCopiesExchangeDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OFFICE_HOME", home)
	workdir := prepareExchange(t)

	dest, err := Archive(workdir, "550e8400-e29b-41d4-a716-446655440000")
	if err != nil {
		t.Fatalf("архив не создан: %v", err)
	}

	want := filepath.Join(home, "runs", "550e8400-e29b-41d4-a716-446655440000")
	if dest != want {
		t.Errorf("архив в %s, ожидался %s", dest, want)
	}
	for _, name := range []string{FileResult, FileLog, FileTask} {
		if _, err := os.Stat(filepath.Join(dest, name)); err != nil {
			t.Errorf("%s не попал в архив: %v", name, err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(dest, FileResult))
	if err != nil {
		t.Fatalf("копия результата не прочитана: %v", err)
	}
	if !strings.Contains(string(raw), `"outcome":"done"`) {
		t.Errorf("копия результата не совпадает с оригиналом: %s", raw)
	}
}

// Молчаливый пропуск архивации означал бы, что прогон считается сохранённым,
// а его не сохранили. Пусть жалуется.
func TestArchiveFailsWithoutExchangeDir(t *testing.T) {
	t.Setenv("OFFICE_HOME", t.TempDir())

	if _, err := Archive(t.TempDir(), "run-id"); err == nil {
		t.Fatal("архивация пустой рабочей папки прошла молча")
	}
}

// OFFICE_HOME задаёт всё хозяйство раннера: архив прогонов, mock-трекер,
// bare-клоны. Без переменной — предсказуемое место в домашнем каталоге.
func TestHomeFollowsEnv(t *testing.T) {
	t.Setenv("OFFICE_HOME", "/tmp/офис")
	if got, err := Home(); err != nil || got != "/tmp/офис" {
		t.Errorf("Home() = %q, %v; ожидался /tmp/офис", got, err)
	}

	t.Setenv("OFFICE_HOME", "")
	userHome, err := os.UserHomeDir()
	if err != nil {
		t.Skip("домашний каталог не определён")
	}
	if got, err := Home(); err != nil || got != filepath.Join(userHome, ".office") {
		t.Errorf("Home() = %q, %v; ожидался %s", got, err, filepath.Join(userHome, ".office"))
	}
}
