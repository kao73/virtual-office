package runner

import (
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

// TrackedByGit — знает ли git об этом файле в рабочей папке.
//
// «Файл есть» и «файл есть у следующей роли» — разные вещи: worktree своё,
// а видит следующая роль только то, что уехало в ветку. Незакоммиченный план
// для неё не существует, и спрашивать об этом надо git, а не файловую систему.
func TrackedByGit(workdir, path string) bool {
	out, err := exec.Command("git", "-C", workdir, "ls-files", "--", path).Output()
	return err == nil && len(out) > 0
}
