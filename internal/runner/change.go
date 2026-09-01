package runner

import (
	"os/exec"
	"path/filepath"
	"regexp"
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

// changeName — безопасное имя каталога изменения из ключа задачи, общее для
// старого и нового корня: тот же ключ адресует одну и ту же задачу в обоих.
func changeName(taskKey string) string {
	name := safeKey(taskKey)
	if name == "" {
		name = ManualChange
	}
	return name
}

// ChangeDirRel — каталог изменения задачи, относительно корня рабочей папки.
func ChangeDirRel(taskKey string) string {
	return filepath.Join(ChangesDir, changeName(taskKey))
}

// ChangeDir — то же абсолютным путём.
func ChangeDir(workdir, taskKey string) string {
	return filepath.Join(workdir, ChangeDirRel(taskKey))
}

// CometChangesDir — новый корень каталогов изменений для analyst/implementer/
// reviewer, которые ведут задачу через Comet Native (Shape/Build/Verify).
// ChangesDir (docs/changes) остаётся вторым источником — для задач, чью
// Shape-фазу analyst прошёл ещё до этого перехода, и для ручных прогонов вне
// комет-конвейера.
const CometChangesDir = "docs/comet/changes"

// CometCurrentChangeFile — куда `comet native new`/`comet state select` пишет,
// какое изменение сейчас выбрано в этой рабочей папке. Обычный трекируемый
// путь, как и .comet/config.yaml — роль коммитит его сама (roles/analyst/
// role.md, «Как коммитить»), а cloneSweep (internal/backends/sbx/clone.go,
// commitLeftovers) страхует, если она забыла. Это и делает файл источником
// истины для composeContext (см. CurrentChangeName в input.go): в отличие от
// имени изменения, угаданного по task-key, он не может разойтись с тем, что
// сам Comet Native считает активным именно в этой рабочей папке.
const CometCurrentChangeFile = ".comet/current-change.json"

// CometConfigFile — конфиг Comet Native, который заводит `comet native new`
// и который EnsureCometHookAllowPaths (input.go) правит, чтобы гарантировать
// блок hook.allow_paths.
const CometConfigFile = ".comet/config.yaml"

// CometChangeName — <name> изменения Comet Native задачи: используется и
// путём в git (CometChangeDirRel), и самой командой `comet native ... <name>`,
// которой каталог, а не только имя, ни к чему.
func CometChangeName(taskKey string) string {
	return cometSafeName(taskKey)
}

// CometChangeDirRel — каталог изменения Comet Native задачи, относительно
// корня рабочей папки. Имя каталога — то же cometSafeName, что и у
// CometChangeName: archive.go ищет изменение по CometChangeName, а
// prpass.go/input.go читают каталог по CometChangeDirRel — оба обязаны
// называть одно и то же изменение.
func CometChangeDirRel(taskKey string) string {
	return filepath.Join(CometChangesDir, cometSafeName(taskKey))
}

// CometChangeDirForName — каталог изменения Comet Native по уже известному,
// а не угаданному имени (например, прочитанному из CometCurrentChangeFile):
// без повторной санитизации через cometSafeName — оно уже прошло её один раз,
// когда `comet native new` заводило изменение под этим именем.
func CometChangeDirForName(name string) string {
	return filepath.Join(CometChangesDir, name)
}

// cometNativeNamePattern — то, что реальный Native CLI принимает как <name>
// изменения (native-paths.js: NATIVE_CHANGE_NAME_PATTERN, обнаружено живым
// запуском comet при ревью Задачи 5): только строчные латинские буквы,
// цифры и одиночные дефисы-разделители, начинается с буквы.
var cometNativeNamePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// cometSafeName приводит changeName(taskKey) к виду, который реальный Native
// CLI примет как <name>. Обычный ключ трекера ("OFF-1") после lower-case уже
// подходит; то, что легаси-хелпер (changeName) считает "безопасным" для
// файловой системы (точки, подчёркивания вида "_manual") — не годится для
// Native, у него более строгий паттерн. При несовпадении после lower-case —
// санитизация: всё вне [a-z0-9] схлопывается в один дефис, края обрезаются,
// пустой результат становится "change", результат без буквы в начале
// получает префикс "x-".
func cometSafeName(taskKey string) string {
	name := strings.ToLower(changeName(taskKey))
	if cometNativeNamePattern.MatchString(name) {
		return name
	}
	var b strings.Builder
	prevDash := false
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash && b.Len() > 0 {
			b.WriteByte('-')
			prevDash = true
		}
	}
	name = strings.Trim(b.String(), "-")
	if name == "" {
		name = "change"
	}
	if name[0] < 'a' || name[0] > 'z' {
		name = "x-" + name
	}
	return name
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
