package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/kao73/virtual-office/runner"
)

// LockFile — замок рабочей папки, `<worktree>/.agent/lock`. Лежит в каталоге
// обмена, потому что это хозяйство раннера, а не проекта-клиента: git его
// не видит (см. runner.ExcludeAgentDir).
const LockFile = "lock"

// ErrWorktreeBusy — рабочая папка занята другим прогоном.
var ErrWorktreeBusy = errors.New("рабочая папка занята другим прогоном")

// hold берёт барьер рабочей папки на время прогона.
//
// Барьер нужен помимо трекера. Захват в JIRA не CAS (см. контракт трекера,
// «Сколько раннеров на проект»), и двое могут уйти работать над одной задачей;
// но рабочая папка и ветка ключуются по ключу задачи, а не по run_id, поэтому
// первым пострадал бы worktree, а выглядело бы это испорченной работой,
// а не проигранной гонкой. Здесь второй останавливается сразу.
//
// Замок — именно flock, и это не деталь. Он держится на **открытом описании
// файла**, а не на процессе: ОС снимает его сама, чем бы прогон ни кончился,
// включая kill -9. Аренда в трекере так не умеет — оттуда и reaper. Заодно
// flock конфликтует сам с собой в пределах процесса (в отличие от fcntl),
// так что барьер проверяется тестом без второго процесса.
func (m *Manager) hold(ws Workspace) (Workspace, error) {
	// Каталог обмена прячется от git прямо здесь, а не в PrepareInput: замок
	// появляется раньше постановки задачи, и без правила в info/exclude свежая
	// рабочая папка выглядела бы грязной.
	if err := runner.ExcludeAgentDir(ws.Dir); err != nil {
		return Workspace{}, err
	}
	dir := filepath.Join(ws.Dir, runner.Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Workspace{}, fmt.Errorf("каталог обмена не создан: %w", err)
	}

	file, err := os.OpenFile(filepath.Join(dir, LockFile), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return Workspace{}, fmt.Errorf("замок рабочей папки не открыт: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return Workspace{}, fmt.Errorf("%w: %s", ErrWorktreeBusy, ws.Dir)
		}
		return Workspace{}, fmt.Errorf("замок рабочей папки %s не взят: %w", ws.Dir, err)
	}

	ws.lock = file
	return ws, nil
}

// TryLock берёт барьер уже существующей рабочей папки, не создавая её.
//
// Нужен уборке: она идёт от папок, а не от задач, и папку, в которой прямо
// сейчас работает агент, трогать не вправе. Ensure для этого не годится — он
// заводит worktree, а уборке заводить нечего.
//
// Занята — ErrWorktreeBusy, и решает вызывающий: уборка такую папку пропускает.
func (m *Manager) TryLock(ws Workspace) (Workspace, error) { return m.hold(ws) }

// Unlock снимает барьер. Зовёт его тот, кто взял папку через Ensure, и лучше
// через defer: ОС снимет замок и сама, но только со смертью процесса, а loop
// живёт долго.
//
// Файл при этом остаётся на месте — он и есть замок. Сноси его Unlock, и
// следующий претендент создал бы новый файл, заперев совсем другой замок:
// flock живёт на inode, а не на имени.
func (w Workspace) Unlock() error {
	if w.lock == nil {
		return nil
	}
	// Закрытия достаточно: с последним закрытием описания файла flock снимается.
	return w.lock.Close()
}
