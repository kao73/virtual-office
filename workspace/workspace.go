// Package workspace готовит агенту рабочую папку и публикует его работу.
//
// На проект заводится bare-клон в хозяйстве раннера, на задачу — worktree
// с веткой `<branch_prefix><KEY>`. Так задачи не мешают друг другу, а история
// каждой лежит отдельной веткой ещё до всякого ревью.
//
// Пуш — единственное место с сетевым git-доступом, и он **вне агента**: роль
// не имеет права на `git push`, и обходной путь через `git -c` из неё закрыт
// перечислением разрешённых подкоманд (см. stage-1-retro.md).
//
// Архив прогонов живёт не здесь, а в runner.Archive: копию каталога обмена делает
// и ручной run-agent, у которого никакого worktree нет.
package workspace

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kao73/virtual-office/runner"
	"github.com/kao73/virtual-office/tracker"
)

// Каталоги хозяйства раннера.
const (
	// ReposDir — bare-клоны проектов.
	ReposDir = "repos"
	// WorktreesDir — рабочие папки задач, если проект не задал свой корень.
	WorktreesDir = "worktrees"
)

// TokenEnv — переменная с токеном для пуша по https. Секреты живут только
// в окружении: ни в конфиге, ни в командной строке (её видно в `ps`).
const TokenEnv = "GITHUB_TOKEN"

// Manager — хозяйство рабочих папок.
type Manager struct {
	home string
}

// New открывает хозяйство в указанном каталоге.
func New(home string) *Manager { return &Manager{home: home} }

// Default — хозяйство раннера: ${OFFICE_HOME}.
func Default() (*Manager, error) {
	home, err := runner.Home()
	if err != nil {
		return nil, err
	}
	return New(home), nil
}

// Workspace — рабочая папка задачи и всё, что о ней нужно знать раннеру.
type Workspace struct {
	Dir    string // worktree: рабочая папка агента
	Repo   string // bare-клон проекта, где живут ветки и объекты
	Branch string

	// lock — взятый Ensure барьер папки; снимается Unlock. Пустой у папки,
	// полученной не из Ensure: List перечисляет их, ничего не занимая.
	lock *os.File
}

// Mounts — что отдать песочнице.
//
// Каталогов два, и второй не роскошь: `.git` внутри worktree — это файл со ссылкой
// на каталог bare-репозитория. Без него git в песочнице отвечает «not a git
// repository», а объекты коммитов писать всё равно некуда. Проверено вживую,
// см. docs/notes/sbx.md.
func (w Workspace) Mounts() []runner.Workspace {
	return []runner.Workspace{{Path: w.Dir}, {Path: w.Repo}}
}

// Ensure готовит рабочую папку задачи: клонирует проект, если его ещё нет,
// освежает его, заводит или переиспользует worktree ветки задачи и берёт
// на неё барьер до конца прогона — см. hold и Unlock.
//
// Переиспользование — не оптимизация, а требование: после `reap` или ответа
// человека задача возвращается к той же незаконченной работе.
//
// Занятую папку Ensure не ждёт и не отнимает: ErrWorktreeBusy, и решает вызывающий.
func (m *Manager) Ensure(task tracker.TaskRef, project tracker.Project) (Workspace, error) {
	repo, err := m.repo(task.Project, project)
	if err != nil {
		return Workspace{}, err
	}

	root, err := canonical(m.worktreeRoot(task.Project, project))
	if err != nil {
		return Workspace{}, err
	}
	ws := Workspace{Dir: filepath.Join(root, task.Key), Repo: repo, Branch: project.Branch(task.Key)}

	// Запись о worktree могла пережить свой каталог: его сносят руками, а иногда
	// и вместе с диском. Тогда чистим запись и заводим заново — на той же ветке,
	// чтобы не потерять сделанное.
	registered, err := m.hasWorktree(repo, ws.Dir)
	if err != nil {
		return Workspace{}, err
	}
	if registered {
		if _, err := os.Stat(ws.Dir); err == nil {
			return m.hold(ws)
		}
		if _, err := git(repo, "worktree", "prune"); err != nil {
			return Workspace{}, err
		}
	}

	if err := m.addWorktree(ws, project); err != nil {
		return Workspace{}, err
	}
	return m.hold(ws)
}

// Push публикует ветку задачи, если на ней есть неопубликованные коммиты.
// Возвращает, состоялся ли пуш.
//
// Раннер пушит всегда, когда есть что пушить, независимо от исхода: работа
// не должна оставаться только в worktree, который однажды удалят.
func (m *Manager) Push(ws Workspace) (bool, error) {
	// Коммиты ветки, не достижимые ни из одной ссылки origin. Одна команда
	// закрывает оба случая: ветки в origin ещё нет и ветка уже там есть.
	out, err := git(ws.Repo, "rev-list", "--count", ws.Branch, "--not", "--remotes=origin")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(out) == "0" {
		return false, nil
	}

	args := append(credentialArgs(), "push", "origin", ws.Branch+":"+ws.Branch)
	if _, err := git(ws.Repo, args...); err != nil {
		return false, err
	}
	return true, nil
}

// Entry — рабочая папка задачи глазами уборщика: чем она занята и что унесёт
// её удаление.
type Entry struct {
	Workspace
	Project string
	Key     string
	Size    int64 // байт на диске
	Dirty   int   // незакоммиченных путей; 0 — чисто
	Missing bool  // запись о worktree есть, каталога нет
}

// List перечисляет рабочие папки всех проектов.
//
// Спрашивает git, а не обходит каталоги: проект вправе задать свой worktree_root
// и лежать на другом диске, а запись о worktree может пережить сам каталог.
// Обход нашёл бы первое и не заметил второго.
func (m *Manager) List() ([]Entry, error) {
	repos, err := os.ReadDir(filepath.Join(m.home, ReposDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil // хозяйства ещё нет: убирать нечего
	}
	if err != nil {
		return nil, fmt.Errorf("клоны проектов не перечислены: %w", err)
	}

	var entries []Entry
	for _, dir := range repos {
		name, isRepo := strings.CutSuffix(dir.Name(), ".git")
		if !dir.IsDir() || !isRepo {
			continue
		}
		repo := filepath.Join(m.home, ReposDir, dir.Name())

		found, err := worktreesOf(repo, name)
		if err != nil {
			return nil, err
		}
		entries = append(entries, found...)
	}
	return entries, nil
}

// worktreesOf разбирает рабочие папки одного клона.
func worktreesOf(repo, project string) ([]Entry, error) {
	out, err := git(repo, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}

	var entries []Entry
	var current Entry
	var bare bool
	flush := func() {
		// Сам клон git тоже перечисляет — он помечает его строкой `bare`.
		// Рабочей папкой он не является и уборке не подлежит. Различаем по этой
		// пометке, а не сравнением путей: git печатает путь разрешённым,
		// и на macOS сравнение с /var разошлось бы с /private/var.
		if current.Dir == "" || bare {
			return
		}
		entries = append(entries, current)
	}

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			dir := strings.TrimPrefix(line, "worktree ")
			bare = false
			current = Entry{
				Workspace: Workspace{Dir: dir, Repo: repo},
				Project:   project,
				Key:       filepath.Base(dir),
			}
			current.Size, current.Dirty, current.Missing = measure(dir)
		case line == "bare":
			bare = true
		case strings.HasPrefix(line, "branch "):
			current.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		}
	}
	flush()
	return entries, nil
}

// measure взвешивает рабочую папку: сколько занимает и сколько в ней
// незакоммиченного. Каталога может не быть вовсе — запись о нём переживает
// и снос руками, и потерю диска.
func measure(dir string) (size int64, dirty int, missing bool) {
	if _, err := os.Stat(dir); err != nil {
		return 0, 0, true
	}

	// Ошибку обхода не поднимаем: размер — справка для человека, и ради неё
	// незачем ронять весь список.
	_ = filepath.WalkDir(dir, func(_ string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil //nolint:nilerr // недочитанный файл просто не считаем
		}
		if info, err := entry.Info(); err == nil {
			size += info.Size()
		}
		return nil
	})

	if out, err := git(dir, "status", "--porcelain"); err == nil {
		if out = strings.TrimSpace(out); out != "" {
			dirty = len(strings.Split(out, "\n"))
		}
	}
	return size, dirty, false
}

// Remove удаляет рабочую папку задачи. Ветка остаётся в bare-клоне: worktree
// эфемерен, работа — нет.
func (m *Manager) Remove(ws Workspace) error {
	_, err := git(ws.Repo, "worktree", "remove", "--force", ws.Dir)
	return err
}

// repo возвращает bare-клон проекта, заводя его при первом обращении,
// и освежает: свежая задача обязана начинаться от свежего origin.
func (m *Manager) repo(name string, project tracker.Project) (string, error) {
	root, err := canonical(filepath.Join(m.home, ReposDir))
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, name+".git")

	if _, err := os.Stat(filepath.Join(path, "HEAD")); errors.Is(err, os.ErrNotExist) {
		// init + remote add, а не clone --bare: так у нас появляются ссылки
		// refs/remotes/origin/*, от которых ветвятся задачи, а ветки агентов
		// живут отдельно, в refs/heads/*.
		if _, err := git("", "init", "--quiet", "--bare", "-b", project.DefaultBranch, path); err != nil {
			return "", err
		}
		if _, err := git(path, "remote", "add", "origin", project.RepoURL); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", fmt.Errorf("клон проекта %s не проверен: %w", name, err)
	}

	if _, err := git(path, append(credentialArgs(), "fetch", "--quiet", "--prune", "origin")...); err != nil {
		return "", err
	}
	return path, nil
}

// worktreeRoot — где живут рабочие папки проекта. Конфигурация важнее умолчания:
// проекты могут лежать на разных дисках.
func (m *Manager) worktreeRoot(name string, project tracker.Project) string {
	if project.WorktreeRoot != "" {
		return project.WorktreeRoot
	}
	return filepath.Join(m.home, WorktreesDir, name)
}

func (m *Manager) hasWorktree(repo, dir string) (bool, error) {
	out, err := git(repo, "worktree", "list", "--porcelain")
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(out, "\n") {
		if path, found := strings.CutPrefix(strings.TrimSpace(line), "worktree "); found && path == dir {
			return true, nil
		}
	}
	return false, nil
}

// addWorktree заводит рабочую папку на ветке задачи.
//
// Ветка ищется в трёх местах, по порядку: локальные ссылки клона,
// `origin/<ветка задачи>` и — если её нигде нет — `origin/<default>`.
//
// Средний случай не роскошь. Раннер сам публикует ветку задачи после каждого
// прогона, а локальные ссылки живут в клоне, который сносят: стёртое хозяйство,
// вторая машина, новый диск. Без взгляда на origin implementer стартовал бы
// от ветки по умолчанию, не увидев плана аналитика, а его собственный пуш потом
// отвергался бы как non-fast-forward — работа целая, но никто не понимает, где.
func (m *Manager) addWorktree(ws Workspace, project tracker.Project) error {
	local, err := refExists(ws.Repo, "refs/heads/"+ws.Branch)
	if err != nil {
		return err
	}
	remote, err := refExists(ws.Repo, "refs/remotes/origin/"+ws.Branch)
	if err != nil {
		return err
	}

	switch {
	case local:
		// Локальная ветка есть — но fetch мог принести на неё чужие коммиты.
		// Отставшую подтягиваем, разошедшуюся оставляем как есть: сливать
		// расхождение — не дело раннера, и об этом всё равно скажет push-failed
		// (docs/contracts/tracker-protocol.md).
		if remote {
			if err := fastForward(ws.Repo, ws.Branch); err != nil {
				return err
			}
		}
		_, err = git(ws.Repo, "worktree", "add", "--quiet", ws.Dir, ws.Branch)
	case remote:
		_, err = git(ws.Repo, "worktree", "add", "--quiet", "-b", ws.Branch, ws.Dir, "origin/"+ws.Branch)
	default:
		_, err = git(ws.Repo, "worktree", "add", "--quiet", "-b", ws.Branch, ws.Dir, "origin/"+project.DefaultBranch)
	}
	return err
}

// fastForward подтягивает локальную ветку задачи к origin, если это перемотка.
//
// Ветка в этот момент ни в одной рабочей папке не выложена — addWorktree зовут
// только тогда, когда папки нет, — поэтому двигается она ссылкой, без checkout.
func fastForward(repo, branch string) error {
	local, remote := "refs/heads/"+branch, "refs/remotes/origin/"+branch

	cmd := exec.Command("git", "-C", repo, "merge-base", "--is-ancestor", local, remote)
	cmd.Env = gitEnv()
	switch err := cmd.Run(); {
	case err == nil:
		_, err := git(repo, "branch", "--force", branch, remote)
		return err
	case isExitCode(err, 1):
		return nil // локальная впереди или ветки разошлись — не наше дело
	default:
		return fmt.Errorf("ветка %s не сверена с origin: %w", branch, err)
	}
}

// refExists — есть ли такая ссылка в клоне. Имя даётся целиком
// (`refs/heads/...`, `refs/remotes/origin/...`): сокращённое git разрешает
// по своим правилам приоритета, а нам нужно знать, какая именно ссылка есть.
func refExists(repo, ref string) (bool, error) {
	cmd := exec.Command("git", "-C", repo, "show-ref", "--verify", "--quiet", ref)
	cmd.Env = gitEnv()
	switch err := cmd.Run(); {
	case err == nil:
		return true, nil
	case isExitCode(err, 1):
		return false, nil
	default:
		return false, fmt.Errorf("ссылка %s не проверена: %w", ref, err)
	}
}

// credentialArgs подкладывает git токен из окружения, не оставляя его ни в конфиге
// репозитория, ни в командной строке: помощник читает переменную сам, уже внутри.
// Первый пустой credential.helper сбрасывает системные — связка ключей macOS
// иначе перехватила бы запрос и полезла спрашивать пароль.
func credentialArgs() []string {
	if os.Getenv(TokenEnv) == "" {
		return nil
	}
	helper := fmt.Sprintf(`!f() { test "$1" = get && echo username=x-access-token && echo password=$%s; }; f`, TokenEnv)
	return []string{"-c", "credential.helper=", "-c", "credential.helper=" + helper}
}

// gitEnv — окружение git-команд раннера. Личность коммитов задаёт адаптер
// в момент запуска агента, и раннер её не переопределяет: коммиты — работа агента,
// а не его обвязки.
func gitEnv() []string {
	return append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
}

// git выполняет команду и возвращает её вывод. Ошибка несёт вывод целиком:
// git объясняет причину в stderr, и терять её — значит разбираться вслепую.
func git(dir string, args ...string) (string, error) {
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	cmd := exec.Command("git", args...)
	cmd.Env = gitEnv()

	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

func isExitCode(err error, code int) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == code
}

// canonical создаёт каталог и возвращает путь без симлинков.
//
// Каноничность обязательна: git записывает в файл `.git` рабочей папки
// разрешённый путь к каталогу репозитория. Отдай раннер песочнице путь через
// симлинк — и внутри git ответит «not a git repository», хотя смонтировано всё.
// На macOS так ведёт себя обычный /tmp.
func canonical(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("каталог %s не создан: %w", dir, err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("путь %s не разрешён: %w", dir, err)
	}
	return resolved, nil
}
