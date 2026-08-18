package runner

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Файлы, которые агент оставляет в корне рабочей папки между прогонами.
// Их пишет он сам, раннер только приносит их обратно в контекст.
const (
	StateFile = "STATE.md"
	PlanFile  = "PLAN.md"
)

// Input — то, что раннер знает о задаче, а агент узнать не может: постановка
// и собранный контекст. Пустой Task означает, что постановка уже лежит
// в каталоге обмена и переписывать её не нужно.
type Input struct {
	Task string
	// Branch — ветка задачи, на которой стоит рабочая папка; BaseBranch — то,
	// от чего она отведена. Обе пусты при ручном запуске: проекта там нет.
	Branch     string
	BaseBranch string
	// Context — разделы, которые допишутся к собранному раннером контексту:
	// переписка тикета, номер попытки, всё, что зависит от трекера.
	Context string
}

// PrepareInput готовит каталог обмена перед запуском агента: постановку задачи,
// контекст и паспорт запуска.
func PrepareInput(workdir string, role Role, run Run, in Input) error {
	agentDir := filepath.Join(workdir, Dir)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		return fmt.Errorf("каталог обмена не создан: %w", err)
	}

	taskPath := filepath.Join(agentDir, FileTask)
	switch {
	case in.Task != "":
		if err := os.WriteFile(taskPath, []byte(in.Task), 0o644); err != nil {
			return fmt.Errorf("%s не записан: %w", filepath.Join(Dir, FileTask), err)
		}
	default:
		if _, err := os.Stat(taskPath); err != nil {
			return fmt.Errorf("постановки задачи нет: не передан --task и отсутствует %s", filepath.Join(Dir, FileTask))
		}
	}

	contextMD, err := composeContext(workdir, role, run, in)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(agentDir, FileContext), []byte(contextMD), 0o644); err != nil {
		return fmt.Errorf("%s не записан: %w", filepath.Join(Dir, FileContext), err)
	}

	passport, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return fmt.Errorf("паспорт запуска не сериализован: %w", err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, FileRun), append(passport, '\n'), 0o644); err != nil {
		return fmt.Errorf("%s не записан: %w", filepath.Join(Dir, FileRun), err)
	}

	if err := ExcludeAgentDir(workdir); err != nil {
		return err
	}
	// Снимок статуса — последним: каталог обмена уже исключён из git, и в снимке
	// его не видно. Иначе ограждение сравнивало бы дельту с собственным конвертом.
	return writeBaseStatus(workdir)
}

// HeadCommit — коммит, на котором стоит рабочая папка. Пусто без ошибки, если
// коммитов нет вовсе: в свежем репозитории точки отсчёта не существует, а вести
// себя это должно как «сравнивать не с чем», а не как поломка.
func HeadCommit(workdir string) (string, error) {
	out, err := exec.Command("git", "-C", workdir, "rev-parse", "HEAD").Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", nil // репозиторий без коммитов
		}
		return "", fmt.Errorf("HEAD рабочей папки %s не прочитан: %w", workdir, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// WorktreeStatus — незакоммиченное в рабочей папке, `git status --porcelain`.
//
// Команда живёт одной функцией потому, что снимок на старте и сверка после
// прогона обязаны быть сделаны одинаково: разойдись флаги — и дельта покажет
// разницу форматов вместо разницы состояний.
//
// `core.quotepath=false` нужен по делу: с умолчанием git отдаёт кириллицу
// восьмеричными escape-последовательностями, и сравнение пути с каталогом
// изменения ломается на первом же русском имени файла.
func WorktreeStatus(workdir string) ([]byte, error) {
	out, err := exec.Command("git", "-C", workdir,
		"-c", "core.quotepath=false", "status", "--porcelain").Output()
	if err != nil {
		return nil, fmt.Errorf("состояние рабочей папки %s не снято: %w", workdir, err)
	}
	return out, nil
}

// writeBaseStatus снимает состояние рабочей папки на старте прогона.
//
// Нужен он затем, что папка переиспользуется: после reap или ответа человека
// роль возвращается к незаконченной работе, и чужая незакоммиченная правка
// лежит там ещё до её первого шага. Ограждение судит **дельту** — то, что
// появилось за этот прогон, — и без снимка судило бы чужое.
func writeBaseStatus(workdir string) error {
	out, err := WorktreeStatus(workdir)
	if err != nil {
		return err
	}
	path := filepath.Join(workdir, Dir, FileBaseStatus)
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("%s не записан: %w", filepath.Join(Dir, FileBaseStatus), err)
	}
	return nil
}

// composeContext собирает context.md. На этом этапе это имя роли, паспорт запуска,
// действующие ограничения и STATE.md, если он есть.
func composeContext(workdir string, role Role, run Run, in Input) (string, error) {
	var b strings.Builder

	b.WriteString("# Контекст запуска\n\n")
	fmt.Fprintf(&b, "- Роль: %s\n", role.Name)
	fmt.Fprintf(&b, "- run_id: %s\n", run.RunID)
	if run.TaskKey != "" {
		fmt.Fprintf(&b, "- Задача: %s\n", run.TaskKey)
	}
	// Ветки — единственный способ отделить работу по задаче от всего остального:
	// `git diff <база>...HEAD` показывает её целиком, и без имени базы этот вопрос
	// в рабочей папке не задать.
	if in.Branch != "" {
		fmt.Fprintf(&b, "- Ветка задачи: %s\n", in.Branch)
	}
	if in.BaseBranch != "" {
		fmt.Fprintf(&b, "- Базовая ветка: %s\n", in.BaseBranch)
	}
	fmt.Fprintf(&b, "- Файл результата: %s\n", role.ResultFile)
	fmt.Fprintf(&b, "- Предел шагов: %d\n", role.Limits.MaxTurns)
	fmt.Fprintf(&b, "- Предел времени: %d с\n", role.Limits.TimeoutSec)
	fmt.Fprintf(&b, "- Разрешённые инструменты: %s\n", strings.Join(role.Tools.Allow, ", "))
	if len(role.Tools.Deny) > 0 {
		fmt.Fprintf(&b, "- Запрещённые инструменты: %s\n", strings.Join(role.Tools.Deny, ", "))
	}

	if in.Context != "" {
		fmt.Fprintf(&b, "\n%s\n", strings.TrimSpace(in.Context))
	}

	// STATE.md и PLAN.md пишет сам агент, чтобы следующий прогон продолжил
	// с того же места. Раннер их не трактует, а просто приносит обратно.
	for _, name := range []string{StateFile, PlanFile} {
		content, err := os.ReadFile(filepath.Join(workdir, name))
		switch {
		case err == nil:
			fmt.Fprintf(&b, "\n## %s\n\n%s\n", name, bytes.TrimSpace(content))
		case errors.Is(err, fs.ErrNotExist):
			// Первый запуск по задаче — файлов ещё нет, это нормально.
		default:
			return "", fmt.Errorf("%s не прочитан: %w", name, err)
		}
	}

	return b.String(), nil
}

// excludeComment помечает происхождение строки в чужом файле исключений.
const excludeComment = "# virtual-office: конверт обмена раннера с агентом"

// ExcludeAgentDir прячет каталог обмена от git средствами, не затрагивающими
// содержимое проекта-клиента: правило пишется в info/exclude, а не в .gitignore.
// Каталог берётся общий: worktree читает info/exclude основного репозитория
// и собственный игнорирует.
func ExcludeAgentDir(workdir string) error {
	out, err := exec.Command("git", "-C", workdir, "rev-parse", "--git-common-dir").Output()
	if err != nil {
		return fmt.Errorf("workdir %s не похож на git-репозиторий: %w", workdir, err)
	}
	common := strings.TrimSpace(string(out))
	if !filepath.IsAbs(common) {
		common = filepath.Join(workdir, common)
	}

	path := filepath.Join(common, "info", "exclude")
	rule := Dir + "/"

	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s не прочитан: %w", path, err)
	}
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == rule {
			return nil // уже исключён, второй раз не дописываем
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("каталог %s не создан: %w", filepath.Dir(path), err)
	}
	addition := excludeComment + "\n" + rule + "\n"
	if len(existing) > 0 && !bytes.HasSuffix(existing, []byte("\n")) {
		addition = "\n" + addition
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("%s не открыт на дозапись: %w", path, err)
	}
	defer f.Close()
	if _, err := f.WriteString(addition); err != nil {
		return fmt.Errorf("%s не дописан: %w", path, err)
	}
	return nil
}
