package guard

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/kao73/virtual-office/internal/runner"
)

// changeDirOnly — вся работа прогона лежит в каталоге изменения.
//
// Работой считается и закоммиченное (`base..HEAD`), и незакоммиченное, но только
// то, что появилось за этот прогон: рабочая папка переиспользуется, и чужая грязь
// в ней лежит ещё до первого шага роли. Отсюда дельта, а не голый `git status`.
//
// С исходом `done` спрашивается ещё две вещи, и обе про одно: план, которого нет
// в git, для следующей роли не существует. Отсюда «внутри каталога чисто»
// и «tasks.md отслеживается».
func changeDirOnly(env Env) error {
	dir, err := changeDirRel(env)
	if errors.Is(err, errNoChangeDir) {
		return errors.New("каталог изменения прогону не задан, а роль требует ограждения change_dir_only: " +
			"работать негде и судить не о чем. Это беда обвязки, а не работы — скажите об этом человеку")
	}
	if err != nil {
		return err
	}

	committed, err := committedPaths(env)
	if err != nil {
		return err
	}
	dirty, err := dirtyPaths(env)
	if err != nil {
		return err
	}

	var outside []string
	for _, path := range append(slices.Clone(committed), dirty...) {
		if !under(path, dir) && !slices.Contains(outside, path) {
			outside = append(outside, path)
		}
	}
	if len(outside) > 0 {
		return fmt.Errorf("работа вне каталога изменения: %s. Этой роли положен только %s: "+
			"коммить каталог целиком (`git add %s`), а лишнее вернуть — незакоммиченное `git restore`, "+
			"уже закоммиченное `git revert`",
			strings.Join(outside, ", "), dir, dir)
	}

	if outcomeOf(env) != runner.OutcomeDone {
		return nil
	}
	// Внутри каталога изменения судится не дельта, а всё незакоммиченное разом,
	// и это разные мерки по делу. Снаружи роль отвечает только за то, что принёс
	// её прогон, — чужая грязь в переиспользуемой папке не её работа. Внутри же
	// вопрос другой: увидит ли план следующая роль. Файл, не уехавший в git,
	// для неё не существует, кто бы его там ни оставил, — в том числе заготовка,
	// которую положил сам раннер и до которой у агента не дошли руки.
	inside, err := dirtInside(env, dir)
	if err != nil {
		return err
	}
	if len(inside) > 0 {
		return fmt.Errorf("план не закоммичен: %s. Исход done означает, что план готов и виден следующей роли, "+
			"а незакоммиченный файл для неё не существует: `git add %s && git commit`",
			strings.Join(inside, ", "), dir)
	}
	if !runner.TrackedByGit(env.Workdir, filepath.Join(dir, runner.FileTasks)) {
		return fmt.Errorf("плана нет: %s не отслеживается git. Исход done без плана принять нельзя — "+
			"заполни файлы каталога %s и закоммить их",
			filepath.Join(dir, runner.FileTasks), dir)
	}
	return nil
}

// planMarksOnly — в плане изменились только отметки пунктов.
//
// План — контракт между ролями, и меняет его тот, кто его писал. Роль, работающая
// по плану, вправе отмечать сделанное и не вправе переписывать задуманное:
// расхождение плана с кодом — это разговор с аналитиком, а не правка молча.
//
// Сравниваются версия на старте прогона и текущая, обе после нормализации:
// отметки, пробелы в хвосте и перевод строки Windows разницей не считаются.
// Плана на базе нет — проверять нечего: задача пришла в очередь без него.
func planMarksOnly(env Env) error {
	dir, err := changeDirRel(env)
	if errors.Is(err, errNoChangeDir) {
		return nil // каталога изменения у этой задачи нет вовсе
	}
	if err != nil {
		return err
	}
	if env.BaseCommit == "" {
		return nil // сравнивать не с чем: репозиторий без коммитов
	}

	path := filepath.Join(dir, runner.FileTasks)
	base, found, err := fileAt(env, path)
	if err != nil || !found {
		return err
	}

	current, err := os.ReadFile(filepath.Join(env.Workdir, path))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("план %s удалён. План меняет аналитик: верни файл — `git restore --source=%s -- %s`; "+
			"если он мешает по делу, это done с next_owner: analyst и объяснением",
			path, short(env.BaseCommit), path)
	case err != nil:
		return fmt.Errorf("%s не прочитан: %w", path, err)
	}

	if normalizePlan(base) == normalizePlan(current) {
		return nil
	}
	return fmt.Errorf("план %s изменён не только отметками. Менять его вправе аналитик: верни файл как был "+
		"(`git restore --source=%s -- %s`, уже закоммиченную правку — `git revert`), отметки `[x]` оставь. "+
		"Расхождение плана с кодом — это done с next_owner: analyst и объяснением, а не правка на месте",
		path, short(env.BaseCommit), path)
}

// errNoChangeDir — каталога изменения у прогона нет. Для одного ограждения это
// беда (проверять нечего, а роль считает себя огороженной), для другого —
// обычное дело, поэтому решает вызывающий.
var errNoChangeDir = errors.New("каталог изменения не задан")

// changeDirRel — каталог изменения относительно рабочей папки: в этом виде
// с ним сравниваются пути, которые отдаёт git.
func changeDirRel(env Env) (string, error) {
	if env.ChangeDir == "" {
		return "", errNoChangeDir
	}
	if env.Workdir == "" {
		return "", errors.New("рабочая папка прогона неизвестна: без неё судить не о чем")
	}
	rel, err := filepath.Rel(env.Workdir, env.ChangeDir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("каталог изменения %s лежит вне рабочей папки %s", env.ChangeDir, env.Workdir)
	}
	return filepath.ToSlash(rel), nil
}

// committedPaths — что прогон закоммитил: разница базы и HEAD.
func committedPaths(env Env) ([]string, error) {
	if env.BaseCommit == "" {
		return nil, nil
	}
	out, err := git(env.Workdir, "diff", "--name-only", "-z", env.BaseCommit, "HEAD")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, path := range strings.Split(string(out), "\x00") {
		if path != "" && !ignored(path, env.Ignore) {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

// dirtyPaths — что прогон оставил незакоммиченным: дельта состояния рабочей
// папки относительно снимка, снятого на старте.
func dirtyPaths(env Env) ([]string, error) {
	if env.BaseStatus == "" {
		return nil, errors.New("снимка состояния на старте прогона нет: судить дельту не по чему. " +
			"Это беда обвязки, а не работы — скажите об этом человеку")
	}
	snapshot, err := os.ReadFile(env.BaseStatus)
	if err != nil {
		return nil, fmt.Errorf("снимок состояния на старте не прочитан: %w", err)
	}
	now, err := runner.WorktreeStatus(env.Workdir)
	if err != nil {
		return nil, err
	}

	was := map[string]bool{}
	for _, line := range strings.Split(string(snapshot), "\n") {
		was[line] = true
	}

	var paths []string
	for _, line := range strings.Split(string(now), "\n") {
		if line == "" || was[line] {
			continue
		}
		for _, path := range statusPaths(line) {
			if !ignored(path, env.Ignore) && !slices.Contains(paths, path) {
				paths = append(paths, path)
			}
		}
	}
	return paths, nil
}

// dirtInside — всё незакоммиченное внутри каталога изменения, без оглядки
// на снимок: здесь важно не «кто это принёс», а «есть ли это в git».
func dirtInside(env Env, dir string) ([]string, error) {
	now, err := runner.WorktreeStatus(env.Workdir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range strings.Split(string(now), "\n") {
		if line == "" {
			continue
		}
		for _, path := range statusPaths(line) {
			if under(path, dir) && !ignored(path, env.Ignore) && !slices.Contains(paths, path) {
				paths = append(paths, path)
			}
		}
	}
	return paths, nil
}

// statusPaths — пути из строки `git status --porcelain`.
//
// Их бывает два: переименование называет и откуда, и куда, а уехавший из каталога
// файл — такая же работа вне каталога, как и приехавший.
func statusPaths(line string) []string {
	if len(line) < 4 {
		return nil
	}
	rest := line[3:]
	if from, to, found := strings.Cut(rest, " -> "); found {
		return []string{unquote(from), unquote(to)}
	}
	return []string{unquote(rest)}
}

// unquote разворачивает путь, который git взял в кавычки: так он поступает
// с именами, где есть кавычка, обратная косая или управляющий символ.
func unquote(path string) string {
	if !strings.HasPrefix(path, `"`) {
		return path
	}
	if unquoted, err := strconv.Unquote(path); err == nil {
		return unquoted
	}
	return path
}

// ignored — не считать ли этот путь работой роли. Совпадение по имени
// составляющей: `.venv` исключает и сам каталог, и всё, что в нём.
func ignored(path string, ignore []string) bool {
	if len(ignore) == 0 {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if slices.Contains(ignore, part) {
			return true
		}
	}
	return false
}

// under — лежит ли путь в каталоге.
func under(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+"/")
}

// fileAt читает файл на базовом коммите. Отсутствие файла — не ошибка: его
// там могло не быть вовсе, и это законное состояние.
func fileAt(env Env, path string) ([]byte, bool, error) {
	out, err := exec.Command("git", "-C", env.Workdir, "show", env.BaseCommit+":"+path).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("%s на коммите %s не прочитан: %w", path, short(env.BaseCommit), err)
	}
	return out, true, nil
}

// marks — отметка выполненного пункта в любом написании.
var marks = regexp.MustCompile(`\[[xX]\]`)

// normalizePlan приводит план к виду, в котором отметки не считаются разницей.
func normalizePlan(raw []byte) string {
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	text = marks.ReplaceAllString(text, "[ ]")

	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

// outcomeOf — исход прогона. Разобранный вызывающим побеждает: раннер судит
// по тому же исходу, по которому повезёт задачу.
//
// Нечитаемый файл исходом не считается: об этом говорит другое ограждение,
// и повторять его претензию незачем.
func outcomeOf(env Env) runner.Outcome {
	if env.Outcome != "" {
		return env.Outcome
	}
	if env.ResultFile == "" {
		return ""
	}
	result, err := runner.ReadResultFile(env.ResultFile)
	if err != nil {
		return ""
	}
	return result.Outcome
}

// git зовёт git в рабочей папке прогона.
//
// `core.quotepath=false` — тот же флаг, с которым снимается состояние папки:
// иначе кириллица приезжает восьмеричными последовательностями, и сравнение
// пути с каталогом изменения ломается на первом же русском имени файла.
func git(workdir string, args ...string) ([]byte, error) {
	full := append([]string{"-C", workdir, "-c", "core.quotepath=false"}, args...)
	out, err := exec.Command("git", full...).Output()
	if err != nil {
		return nil, fmt.Errorf("git %s не выполнен: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// short — первые восемь символов идентификатора: столько же, сколько в маркере.
func short(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}
