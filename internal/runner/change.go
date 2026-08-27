package runner

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ChangesDir — где в проекте-клиенте живут каталоги изменений. Путь относительный:
// отсчитывается от корня рабочей папки.
const ChangesDir = "docs/changes"

// ManualChange — каталог изменения ручного прогона. Ключа задачи там нет вовсе,
// а писать план некуда было бы: отладка роли обязана выглядеть как работа.
const ManualChange = "_manual"

// Файлы каталога изменения. Имена — контракт между ролями: одна их пишет,
// две читают, и раннер судит по ним же, есть ли план.
const (
	FileBrief  = "brief.md"
	FileDesign = "design.md"
	FileTasks  = "tasks.md"
)

// TemplatesDir — подкаталог роли с заготовками артефактов.
const TemplatesDir = "templates"

// ScopeChangeDir — единственное значение write_scope.dir: «роли положено писать
// в каталог изменения этой задачи, и больше никуда». Значение, а не флаг, потому
// что областей записи со временем может стать больше одной.
const ScopeChangeDir = "change_dir"

// ChangeFiles — файлы каталога изменения в порядке чтения: зачем, как и что делать.
func ChangeFiles() []string { return []string{FileBrief, FileDesign, FileTasks} }

// ChangeDirRel — каталог изменения задачи, относительно корня рабочей папки.
func ChangeDirRel(taskKey string) string {
	name := safeKey(taskKey)
	if name == "" {
		name = ManualChange
	}
	return filepath.Join(ChangesDir, name)
}

// ChangeDir — то же абсолютным путём.
func ChangeDir(workdir, taskKey string) string {
	return filepath.Join(workdir, ChangeDirRel(taskKey))
}

// safeKey чистит ключ задачи, прежде чем тот станет именем каталога.
//
// Ключи трекеров выглядят как OFFICE-1, но приходят они снаружи, а путь из них
// собирается внутри чужого репозитория: ключ с косой чертой или точками увёл бы
// каталог за пределы проекта. Всё, что не буква, не цифра и не `-._`, заменяется
// на подчёркивание — молча испорченного пути не будет, будет видимо странный.
func safeKey(key string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-', r == '_', r == '.':
			return r
		}
		return '_'
	}, key)
}

// PrepareChangeDir создаёт каталог изменения по шаблонам роли и отдаёт список
// созданных файлов.
//
// Готовит его раннер, а не агент, и делает это для роли, объявившей
// `write_scope.dir: change_dir`. Имени роли в коде при этом нет: поведение
// следует из спецификации, как и всё остальное. Аналитику остаётся правка
// готовых файлов — инструмента создания у него нет вовсе.
//
// Уже существующие файлы не трогаются: на втором прогоне по той же задаче
// в каталоге лежит работа прошлого, и затирать её шаблоном нельзя.
func PrepareChangeDir(workdir string, role Role, taskKey string) ([]string, error) {
	if role.WriteScope.Dir != ScopeChangeDir {
		return nil, nil
	}

	dir := ChangeDir(workdir, taskKey)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("каталог изменения не создан: %w", err)
	}

	var created []string
	for _, name := range ChangeFiles() {
		path := filepath.Join(dir, name)
		switch _, err := os.Stat(path); {
		case err == nil:
			continue
		case !errors.Is(err, fs.ErrNotExist):
			return created, fmt.Errorf("%s не прочитан: %w", path, err)
		}

		body, err := os.ReadFile(role.TemplateFile(name))
		if err != nil {
			return created, fmt.Errorf("шаблон роли %s не прочитан: %w", role.Name, err)
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			return created, fmt.Errorf("%s не записан: %w", path, err)
		}
		created = append(created, path)
	}
	return created, nil
}

// SweepChangeDir убирает пустышки — файлы, которые раннер создал перед прогоном,
// а агент не тронул и не закоммитил.
//
// Без уборки провалившийся прогон оставлял бы в рабочей папке три шаблона,
// и следующая роль читала бы их как план. Судится каждый файл отдельно: тронутый
// остаётся (в нём работа), отслеживаемый git — тоже (он уже в истории).
//
// Зовётся после ограждений, а не до: незакоммиченный шаблон в каталоге —
// это ровно то, по чему ограждение отличает «план готов» от «план забыт».
func SweepChangeDir(workdir string, role Role, created []string) error {
	for _, path := range created {
		body, err := os.ReadFile(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue // агент удалил сам — его право
		case err != nil:
			return fmt.Errorf("%s не прочитан: %w", path, err)
		}

		template, err := os.ReadFile(role.TemplateFile(filepath.Base(path)))
		if err != nil {
			return fmt.Errorf("шаблон роли %s не прочитан: %w", role.Name, err)
		}
		if string(body) != string(template) || TrackedByGit(workdir, path) {
			continue
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("%s не убран: %w", path, err)
		}
	}

	// Каталог, оставшийся пустым, тоже мусор. Непустой os.Remove не тронет,
	// и это здесь и нужно: пустоту он уберёт, работу оставит.
	if len(created) > 0 {
		_ = os.Remove(filepath.Dir(created[0]))
	}
	return nil
}

// TrackedByGit — знает ли git об этом файле в рабочей папке.
//
// «Файл есть» и «файл есть у следующей роли» — разные вещи: worktree своё,
// а видит следующая роль только то, что уехало в ветку. Незакоммиченный план
// для неё не существует, и спрашивать об этом надо git, а не файловую систему.
func TrackedByGit(workdir, path string) bool {
	out, err := exec.Command("git", "-C", workdir, "ls-files", "--", path).Output()
	return err == nil && len(out) > 0
}
