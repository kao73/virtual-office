package runner

import (
	"path/filepath"
	"strings"
)

// Переменные прогона — агент-нейтральная часть контракта «раннер ↔ агент».
//
// Ограждения запускает сам агент, и параметры прогона они получают из окружения,
// а не аргументами: аргументы задаёт адаптер, а адаптеров будет больше одного,
// и каждый расставил бы их по-своему. Имена одни для всех: второй адаптер обязан
// их повторить (docs/contracts/agent-io.md).
const (
	// EnvTaskKey — ключ задачи в трекере; при ручном прогоне его нет.
	EnvTaskKey = "OFFICE_TASK_KEY"
	// EnvChangeDir — каталог изменения этой задачи. Путь следует из ключа,
	// а не из роли: план у задачи один, а ролей вокруг него три.
	EnvChangeDir = "OFFICE_CHANGE_DIR"
	// EnvBaseCommit — HEAD рабочей папки на старте; в репозитории без коммитов
	// его нет вовсе, и сравнивать тогда не с чем.
	EnvBaseCommit = "OFFICE_BASE_COMMIT"
	// EnvBaseStatus — путь к снимку `git status --porcelain` на старте.
	EnvBaseStatus = "OFFICE_BASE_STATUS"
	// EnvWriteIgnore — что не считать работой роли, через запятую.
	EnvWriteIgnore = "OFFICE_WRITE_IGNORE"
	// EnvResultFile — абсолютный путь файла результата.
	EnvResultFile = "OFFICE_RESULT_FILE"
)

// RunVars — все переменные прогона, в порядке имён. Список нужен тому, кто
// читает их из окружения: перечислять имена во второй раз значило бы однажды
// разойтись с первым списком.
func RunVars() []string {
	return []string{EnvTaskKey, EnvChangeDir, EnvBaseCommit, EnvBaseStatus, EnvWriteIgnore, EnvResultFile}
}

// RunEnv — параметры прогона для ограждений.
//
// Собирает их раннер: всё здесь — его наблюдение за прогоном, а не заявление
// агента. Функция одна на два пути: адаптер переносит эти значения в окружение
// запуска, а раннер, проверяя ограждения после прогона, строит по ним ту же
// обстановку. Разойдись эти два места — ограждение в хуке и ограждение
// в раннере судили бы по разным точкам отсчёта.
//
// Пустые значения законны и означают «нет»: выкидывает их тот, кто пишет
// окружение, — пустая переменная и отсутствующая читаются в шелле одинаково.
func RunEnv(role Role, workdir string, run Run) map[string]string {
	return map[string]string{
		EnvTaskKey:     run.TaskKey,
		EnvChangeDir:   ChangeDir(workdir, run.TaskKey),
		EnvBaseCommit:  run.BaseCommit,
		EnvBaseStatus:  BaseStatusPath(workdir),
		EnvWriteIgnore: strings.Join(role.WriteScope.Ignore, ","),
		EnvResultFile:  filepath.Join(workdir, role.ResultFile),
	}
}

// BaseStatusPath — где в рабочей папке лежит снимок состояния на старте прогона.
func BaseStatusPath(workdir string) string {
	return filepath.Join(workdir, Dir, FileBaseStatus)
}

// WorkdirOf — рабочая папка по пути снимка: обратная BaseStatusPath.
//
// Отдельной переменной с рабочей папкой в контракте нет намеренно: снимок лежит
// в каталоге обмена, каталог обмена — в корне рабочей папки, и седьмое имя
// сообщало бы то же самое ещё раз. Функции стоят рядом, чтобы обратимость
// была видна, а не подразумевалась.
func WorkdirOf(baseStatus string) string {
	if baseStatus == "" {
		return ""
	}
	return filepath.Dir(filepath.Dir(baseStatus))
}
