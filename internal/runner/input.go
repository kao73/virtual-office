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

// StateFile — черновик, который агент оставляет в корне рабочей папки между
// прогонами. Пишет его он сам, раннер только приносит файл обратно в контекст.
//
// Пары ему больше нет: планом задачи заведует каталог изменения, и лежит он
// в git, а не в рабочей папке. Файл в корне, переживающий прогоны, но не ветку,
// планом быть не может — следующая роль его не увидит.
const StateFile = "STATE.md"

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

	// Результат прошлого прогона убирается перед новым, и это не нарушение
	// правила «result.json принадлежит агенту». Правило про то, что раннер
	// не сочиняет и не правит отчёт; файл же, оставшийся в переиспользуемой
	// рабочей папке, принадлежит **другому** прогону, и прочитать его как свой —
	// значит выдать чужие слова за отчёт этого.
	//
	// Поймано нагрузочным прогоном: агент, упёршийся в предел шагов, не успел
	// написать результат, а раннер прочитал файл, оставленный предыдущей ролью,
	// и увёл задачу вперёд с чужим «done» в тикете (docs/notes/stage-4-retro.md).
	if err := os.Remove(filepath.Join(workdir, role.ResultFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("результат прошлого прогона не убран: %w", err)
	}

	// Тем же рассуждением: run.log лежит внутри Dir и в бэкенде sbx --clone
	// путешествует вместе с ним туда-обратно (internal/backends/sbx/clone.go)
	// — чужой лог, занесённый в песочницу до того, как os.Create(logPath)
	// его обрежет, приезжает назад поверх свежего и подменяет собой то, что
	// runagent.Execute потом читает для расхода, классификации окончания
	// и архива прогона. На бинд-маунте безобидно (os.Create и так обрезает
	// единственный файл), но убирать здесь надёжнее, чем полагаться на то,
	// какой бэкенд выбран.
	if err := os.Remove(filepath.Join(agentDir, FileLog)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("лог прошлого прогона не убран: %w", err)
	}

	if err := ExcludeAgentDir(workdir); err != nil {
		return err
	}
	// То же самое рассуждение, что и у конверта обмена, но уже только для
	// .comet/runtime/**: `comet native new` (запускает analyst) оставляет его
	// незакоммиченным, и без исключения снимок статуса ниже увидел бы это как
	// грязь этого прогона. .comet/current-change.json — не здесь: обычный
	// трекируемый путь наравне с config.yaml, роль коммитит его сама.
	if err := ExcludeCometRuntime(workdir); err != nil {
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
//
// `-uall` — по той же причине. С умолчанием git сворачивает целиком неотслеживаемый
// каталог в одну строку `?? docs/`, и по ней не сказать ни что внутри, ни лежит ли
// это внутри каталога изменения: первый же план в проекте, где нет `docs/`,
// выглядел бы работой вне своей области.
func WorktreeStatus(workdir string) ([]byte, error) {
	out, err := exec.Command("git", "-C", workdir,
		"-c", "core.quotepath=false", "status", "--porcelain", "-uall").Output()
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
	// Каталог изменения угадывается по task-key, но .comet/current-change.json
	// — если он уже есть — называет его точно: это то же имя, что сам Comet
	// Native считает выбранным именно в этой рабочей папке, и оно не обязано
	// совпадать с CometChangeName(task-key), если аналитик назвал изменение
	// иначе (живой прогон, задача demo-3: аналитик завёл "stats-median" при
	// task-key "demo-3" — угаданный по ключу каталог не существовал, и
	// implementer остался без единой строки "Каталог изменения", хотя
	// изменение было и стояло в fase build). Файл побеждает угадывание —
	// не наоборот: угадывание по task-key остаётся единственным способом
	// узнать каталог до самого первого `comet native new` этого прогона,
	// когда current-change.json ещё не существует нигде.
	//
	// Старый docs/changes/<KEY> — второй источник, для задач, чью Shape-фазу
	// analyst прошёл ещё до перехода на Comet Native: current-change.json
	// он не заводит, туда попасть не может, и тут остаётся угадывание.
	// "План: <путь>/tasks.md" имеет смысл только у старого корня: изменения
	// Comet Native такого файла не пишут вовсе, и строка там просто не
	// появится — это не пробел, а точный ответ.
	dir := CometChangeDirRel(run.TaskKey)
	if name := currentChangeName(workdir); name != "" {
		if selected := CometChangeDirRel(name); exists(filepath.Join(workdir, selected)) {
			dir = selected
		}
	}
	if !exists(filepath.Join(workdir, dir)) {
		if legacy := ChangeDirRel(run.TaskKey); exists(filepath.Join(workdir, legacy)) {
			dir = legacy
		}
	}
	if exists(filepath.Join(workdir, dir)) {
		fmt.Fprintf(&b, "- Каталог изменения: %s\n", dir)
		if plan := filepath.Join(dir, FileTasks); TrackedByGit(workdir, plan) {
			fmt.Fprintf(&b, "- План: %s\n", plan)
		}
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

	// STATE.md пишет сам агент, чтобы следующий прогон продолжил с того же
	// места. Раннер его не трактует, а просто приносит обратно.
	content, err := os.ReadFile(filepath.Join(workdir, StateFile))
	switch {
	case err == nil:
		fmt.Fprintf(&b, "\n## %s\n\n%s\n", StateFile, bytes.TrimSpace(content))
	case errors.Is(err, fs.ErrNotExist):
		// Первый запуск по задаче — файла ещё нет, это нормально.
	default:
		return "", fmt.Errorf("%s не прочитан: %w", StateFile, err)
	}

	return b.String(), nil
}

// exists — есть ли такой каталог или файл. Беду чтения от отсутствия здесь
// не отличают намеренно: контекст собирается на лучших усилиях, и уронить
// из-за него прогон было бы хуже, чем не сказать одной строки.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// currentChangeName читает CometCurrentChangeFile и возвращает поле "change",
// если файла нет, он повреждён или поле пусто — "", тем же принципом лучших
// усилий, что и exists: не найти файл здесь так же нормально, как не найти
// каталог изменения (самый первый прогон по задаче, или задача всё ещё на
// старом корне docs/changes).
func currentChangeName(workdir string) string {
	data, err := os.ReadFile(filepath.Join(workdir, CometCurrentChangeFile))
	if err != nil {
		return ""
	}
	var selection struct {
		Change string `json:"change"`
	}
	if err := json.Unmarshal(data, &selection); err != nil {
		return ""
	}
	return selection.Change
}

// excludeComment помечает происхождение строки в чужом файле исключений.
const excludeComment = "# virtual-office: конверт обмена раннера с агентом"

// ExcludeAgentDir прячет каталог обмена от git средствами, не затрагивающими
// содержимое проекта-клиента: правило пишется в info/exclude, а не в .gitignore.
// Каталог берётся общий: worktree читает info/exclude основного репозитория
// и собственный игнорирует.
func ExcludeAgentDir(workdir string) error {
	return appendExcludeRules(workdir, excludeComment, []string{Dir + "/"})
}

// cometExcludeComment — как excludeComment, но про .comet/: поясняет, почему
// исключён не весь каталог, а только runtime/ (см. ExcludeCometRuntime).
const cometExcludeComment = "# virtual-office: кэш локального исполнения .comet/runtime/ (остальное .comet/ коммитится ролью)"

// ExcludeCometRuntime прячет от git .comet/runtime/ — внутреннее состояние
// исполнения Comet Native, которое заводит `comet native new`/`next`. Это
// кэш конкретной песочницы, а не часть истории проекта: живой прогон нашёл
// в state.json абсолютные хостовые пути (projectRoot/worktreeRoot) этой
// самой рабочей папки — закоммить их, и следующая роль в другой песочнице
// унаследует чужой, неверный путь вместо своего. Без исключения worktree
// к тому же навсегда остаётся "грязным" (`?? .comet/runtime/`): sweepWorktrees
// сочтёт его небезопасным для удаления.
//
// .comet/current-change.json и .comet/config.yaml — вне этого исключения
// нарочно: оба обязаны остаться обычными трекируемыми путями, иначе
// `comet native status` не восстановится в другой рабочей папке или
// у другого раннер-хоста, а следующая роль не найдёт каталог изменения
// через context.md (currentChangeName ниже) — см. roles/analyst/role.md,
// "## Как коммитить".
//
// staleCurrentChangeExclude чистится первым делом: до этой правки
// ExcludeCometRuntime писала правило и на current-change.json тоже, а
// appendExcludeRules только дописывает — сама она никогда ничего не
// удаляет. В рабочей папке, уже тронутой прежней версией, это правило
// оставалось бы в info/exclude навсегда и ловило бы analyst на той же
// ошибке git add, что и в commitLeftovers (клиентский прогон, задача
// demo-3, второй заход): «paths are ignored by one of your .gitignore
// files» — на этот раз не от cloneSweep, а прямо от роли, честно
// следующей новой инструкции role.md «git add .comet/current-change.json».
const staleCurrentChangeExclude = ".comet/current-change.json"

func ExcludeCometRuntime(workdir string) error {
	if err := removeExcludeRule(workdir, staleCurrentChangeExclude); err != nil {
		return err
	}
	return appendExcludeRules(workdir, cometExcludeComment, []string{
		".comet/runtime/",
	})
}

// removeExcludeRule убирает ровно одну строку rule из info/exclude общего
// git-каталога, если она там есть — обратная операция к appendExcludeRules,
// нужная только для миграции устаревших правил (см. вызывающего). Отсутствие
// файла или отсутствие строки — не ошибка, тот же принцип лучших усилий,
// что и у appendExcludeRules: рабочая папка без истории этого правила
// в починке не нуждается.
func removeExcludeRule(workdir, rule string) error {
	out, err := exec.Command("git", "-C", workdir, "rev-parse", "--git-common-dir").Output()
	if err != nil {
		return fmt.Errorf("workdir %s не похож на git-репозиторий: %w", workdir, err)
	}
	common := strings.TrimSpace(string(out))
	if !filepath.IsAbs(common) {
		common = filepath.Join(workdir, common)
	}
	path := filepath.Join(common, "info", "exclude")

	existing, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("%s не прочитан: %w", path, err)
	}

	lines := strings.Split(string(existing), "\n")
	kept := lines[:0]
	changed := false
	for _, line := range lines {
		if strings.TrimSpace(line) == rule {
			changed = true
			continue
		}
		kept = append(kept, line)
	}
	if !changed {
		return nil
	}
	if err := os.WriteFile(path, []byte(strings.Join(kept, "\n")), 0o644); err != nil {
		return fmt.Errorf("%s не переписан: %w", path, err)
	}
	return nil
}

// appendExcludeRules дописывает в info/exclude общего git-каталога рабочей папки
// те строки из rules, которых там ещё нет — общий механизм для ExcludeAgentDir
// и ExcludeCometRuntime. Каждое правило проверяется по отдельности (а не «весь
// набор целиком»), чтобы повторный вызов с частично новым набором правил не
// сломал идемпотентность уже записанных строк. Каталог берётся общий: worktree
// читает info/exclude основного репозитория и собственный игнорирует.
func appendExcludeRules(workdir, comment string, rules []string) error {
	out, err := exec.Command("git", "-C", workdir, "rev-parse", "--git-common-dir").Output()
	if err != nil {
		return fmt.Errorf("workdir %s не похож на git-репозиторий: %w", workdir, err)
	}
	common := strings.TrimSpace(string(out))
	if !filepath.IsAbs(common) {
		common = filepath.Join(workdir, common)
	}

	path := filepath.Join(common, "info", "exclude")

	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s не прочитан: %w", path, err)
	}
	existingLines := strings.Split(string(existing), "\n")
	already := func(rule string) bool {
		for _, line := range existingLines {
			if strings.TrimSpace(line) == rule {
				return true
			}
		}
		return false
	}

	var missing []string
	for _, rule := range rules {
		if !already(rule) {
			missing = append(missing, rule)
		}
	}
	if len(missing) == 0 {
		return nil // все правила уже на месте, второй раз не дописываем
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("каталог %s не создан: %w", filepath.Dir(path), err)
	}
	var b strings.Builder
	if len(existing) > 0 && !bytes.HasSuffix(existing, []byte("\n")) {
		b.WriteString("\n")
	}
	b.WriteString(comment)
	b.WriteString("\n")
	for _, rule := range missing {
		b.WriteString(rule)
		b.WriteString("\n")
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("%s не открыт на дозапись: %w", path, err)
	}
	defer f.Close()
	if _, err := f.WriteString(b.String()); err != nil {
		return fmt.Errorf("%s не дописан: %w", path, err)
	}
	return nil
}
