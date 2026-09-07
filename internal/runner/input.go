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
	"regexp"
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
	// Attachments — вложения тикета, уже скачанные (Office.humanAttachments
	// решает, какие: служебные вроде runner.SplitAttachmentName сюда не
	// попадают). PrepareInput кладёт их настоящими файлами в DirAttachments —
	// не текстом в Task, там бывают картинки и PDF.
	Attachments []InputAttachment
}

// InputAttachment — одно вложение, готовое лечь в рабочую папку: имя
// (человеческое, как назвал автор) и сырые данные.
type InputAttachment struct {
	Name string
	Data []byte
}

// ResolveAttachmentNames превращает человеческие имена вложений в
// безопасные и однозначные имена файлов, в том же порядке. Чистая
// функция без ввода-вывода: и writeAttachments (запись на диск), и
// pipeline.taskBody (упоминание в постановке) считают одно и то же по
// одному и тому же списку — иначе постановка называла бы файл так, как
// его не назвали на самом деле (независимое ревью, находка «task.md
// advertises pre-sanitization names»).
//
// Имя вложения — чужой ввод (человек так назвал файл, не раннер): путь
// собирается через filepath.Base, а голые "." и ".." после него — тоже
// не имя файла, а способ выйти из каталога (filepath.Join(dir, "..") —
// это уже родитель dir), поэтому заменяются заглушкой, как и пустое имя.
//
// Одинаковые после обрезки имена не перезаписывают друг друга —
// проверка идёт по уже ЗАНЯТЫМ итоговым именам, а не по счётчику
// повторов исходного: без этого третье вложение с именем, случайно
// совпавшим с уже сгенерированным именем второго (`"2-a.png"`), тихо
// затирало бы его — независимое ревью воспроизвело эту потерю на
// `["a.png", "a.png", "2-a.png"]`.
func ResolveAttachmentNames(names []string) []string {
	taken := make(map[string]bool, len(names))
	resolved := make([]string, len(names))
	for i, raw := range names {
		name := filepath.Base(raw)
		if name == "" || name == "." || name == ".." || name == string(filepath.Separator) {
			name = "attachment"
		}
		unique := name
		for n := 2; taken[unique]; n++ {
			unique = fmt.Sprintf("%d-%s", n, name)
		}
		taken[unique] = true
		resolved[i] = unique
	}
	return resolved
}

// writeAttachments кладёт вложения тикета в DirAttachments настоящими
// файлами, а не текстом внутри Task: среди них бывают картинки и PDF,
// которые агент читает своими инструментами, а не разбором markdown.
//
// Каталог перезаписывается целиком: рабочая папка тикета переживает
// попытки, а набор вложений между ними мог измениться, и вложение,
// пропавшее у родителя, не должно продолжать лежать в чужой уже папке.
func writeAttachments(agentDir string, attachments []InputAttachment) error {
	dir := filepath.Join(agentDir, DirAttachments)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("прошлые вложения не убраны: %w", err)
	}
	if len(attachments) == 0 {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("каталог вложений не создан: %w", err)
	}

	raw := make([]string, len(attachments))
	for i, a := range attachments {
		raw[i] = a.Name
	}
	resolved := ResolveAttachmentNames(raw)
	for i, a := range attachments {
		if err := os.WriteFile(filepath.Join(dir, resolved[i]), a.Data, 0o644); err != nil {
			return fmt.Errorf("вложение %s не записано: %w", resolved[i], err)
		}
	}
	return nil
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

	if err := writeAttachments(agentDir, in.Attachments); err != nil {
		return err
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
	if err := ClearStaleCometLocks(workdir); err != nil {
		return err
	}
	if err := EnsureCometHookAllowPaths(workdir); err != nil {
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
	if name := CurrentChangeName(workdir); name != "" {
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

// CurrentChangeName читает CometCurrentChangeFile на диске рабочей папки и
// возвращает поле "change", если файла нет, он повреждён или поле пусто —
// "", тем же принципом лучших усилий, что и exists: не найти файл здесь так
// же нормально, как не найти каталог изменения (самый первый прогон по
// задаче, или задача всё ещё на старом корне docs/changes). Экспортирована:
// internal/pipeline (archive.go) резолвит имя изменения тем же способом,
// а не угадыванием по task-key — независимое ревью нашло живой случай
// (задача demo-3, изменение stats-median), где эти имена расходятся.
func CurrentChangeName(workdir string) string {
	data, err := os.ReadFile(filepath.Join(workdir, CometCurrentChangeFile))
	if err != nil {
		return ""
	}
	return ParseCurrentChangeName(data)
}

// ParseCurrentChangeName разбирает уже прочитанное содержимое
// CometCurrentChangeFile и возвращает поле "change" — тем же принципом
// лучших усилий, что и CurrentChangeName: повреждённый JSON или пустое поле
// дают "", а не ошибку. Отдельная от CurrentChangeName функция: internal/
// pipeline.prBody читает этот же файл не с диска, а из bare-клона
// (Workspaces.Show, `git show origin/<ветка>:...`) — у задачи без рабочей
// папки файла на диске нет вовсе.
func ParseCurrentChangeName(data []byte) string {
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
// через context.md (CurrentChangeName ниже) — см. roles/analyst/role.md,
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

// ClearStaleCometLocks убирает CometRuntimeLocksRel перед каждым прогоном —
// тот же приём и по той же причине, что и clearStaleCometLocks в
// internal/backends/sbx/clone.go, только для обычного бинд-маунта:
// продовый конвейер (internal/pipeline/agent.go) worktree переиспользует
// (workspace.Manager.Ensure), а не заводит эфемерную песочницу на каждый
// прогон, и .comet/runtime/native/locks в нём переживает прогон точно так
// же, как переживал бы перенос между песочницами --clone. Второе,
// независимое ревью нашло: усечённый по таймауту/пределу шагов прогон —
// штатный, не исключительный случай — оставляет лок с pid+hostname
// текущего процесса; координатор Comet Native его живость не проверяет
// и следующий прогон той же задачи блокируется навсегда (exit 73) — ровно
// тот отказ, что стоит за EXP-2 (docs/notes/stage-5-live-backlog.md).
//
// Безопасно вызывать здесь: раннер зовёт PrepareInput только на уже
// захваченной через workspace.Manager.hold() рабочей папке — barrier
// гарантирует, что параллельного живого прогона над теми же locks быть не
// может, а значит любой найденный здесь лок обязательно чужой и мёртвый.
// Исключение — cmd/run-agent (ручной инструмент, вызывает PrepareInput
// напрямую, без hold()): барьера там нет вовсе, но и параллельного прогона
// над той же рабочей папкой там тоже никто не гарантирует — тот же риск,
// что и у любой ручной команды над общей папкой, не новый для этой функции.
//
// Отсутствие .comet/runtime/** — не ошибка, тот же принцип лучших усилий,
// что и у ExcludeCometRuntime: задача может не иметь активного изменения
// Comet Native вовсе.
func ClearStaleCometLocks(workdir string) error {
	locks := filepath.Join(workdir, CometRuntimeLocksRel)
	if err := os.RemoveAll(locks); err != nil {
		return fmt.Errorf("устаревшие блокировки Comet Native (%s) не убраны: %w", locks, err)
	}
	return nil
}

// cometHookAllowPathsBlock — что EnsureCometHookAllowPaths дописывает в
// .comet/config.yaml. Без него хук-роутер (comet-hook-router.mjs,
// hooks.pre_tool_use) технически блокирует и .agent/result.json, и
// STATE.md как «правку реализации» вне фазы build, а на фазе verify запись
// .agent/result.json не блокируется, а молча переводит изменение обратно
// в build — проверено живым прогоном (roles/analyst/role.md).
const cometHookAllowPathsBlock = "hook:\n  allow_paths:\n    - .agent\n    - STATE.md\n"

// cometHookKeyPattern узнаёт уже существующий верхнеуровневый ключ `hook:`
// в .comet/config.yaml — построчным сопоставлением, не разбором YAML: тот же
// приём, каким analyst раньше искал этот ключ руками (role.md, «Если в
// .comet/config.yaml ещё нет блока hook:»), только детерминированный.
//
// Без якоря конца строки нарочно: независимое ревью нашло, что `^hook:\s*$`
// не узнаёт `hook: {}` или `hook:  # комментарий` (что угодно после
// двоеточия) — блок дописался бы второй раз, дав дублирующийся верхне-
// уровневый ключ hook: в YAML. "hooks:" (другой ключ, множественное число)
// этот паттерн по-прежнему не ловит: после "hook" там не двоеточие, а "s".
var cometHookKeyPattern = regexp.MustCompile(`(?m)^hook:`)

// hookConfigCommitName/hookConfigCommitEmail — личность коммита, которым
// EnsureCometHookAllowPaths сама фиксирует свою правку .comet/config.yaml,
// тем же приёмом, что и archiveCommitName/archiveCommitEmail
// (internal/pipeline/archive.go) и cloneSweepName/cloneSweepEmail
// (internal/backends/sbx/clone.go): это решение обвязки, а не роли, и в git
// blame это обязано быть видно.
const (
	hookConfigCommitName  = "comet-hook-config"
	hookConfigCommitEmail = "comet-hook-config@office.local"
)

// EnsureCometHookAllowPaths гарантирует блок hook.allow_paths в
// .comet/config.yaml для любой роли, а не только для той, что решила его
// проверить: независимое ревью нашло, что этот блок был описан только
// в roles/analyst/role.md — implementer и reviewer о нём не знали вовсе,
// и для reviewer это молча и незаметно ломало Verify (запись result.json
// откатывала изменение в build, archiveIfReady не находил archive-ready,
// а reviewer как ни в чём не бывало отчитывался done). Отсутствие
// .comet/config.yaml — не ошибка, тот же принцип лучших усилий, что и
// у ExcludeCometRuntime: у задачи может не быть активного изменения
// Comet Native вовсе.
//
// Коммитит свою правку сама, а не оставляет её роли: .comet/config.yaml —
// обычный трекируемый путь, и коммитить его умеет только analyst (roles/
// analyst/role.md, «Как коммитить») — если блок допишется на прогоне
// implementer'а или reviewer'а (например, изменение заведено более старым
// прогоном до этой правки), никто из них его не закоммитит, и рабочая
// папка останется "грязной" навсегда — sweepWorktrees её не уберёт
// (независимое ревью). Раздельный коммит от чужой работы этого прогона —
// то же рассуждение, что у commitLeftovers/archiveIfReady: обвязка правит
// служебный файл, роль об этом может даже не знать.
func EnsureCometHookAllowPaths(workdir string) error {
	path := filepath.Join(workdir, CometConfigFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("%s не прочитан: %w", path, err)
	}
	if cometHookKeyPattern.Match(data) {
		return nil // блок hook: уже есть — не трогаем существующие ключи
	}

	addition := cometHookAllowPathsBlock
	if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
		addition = "\n" + addition
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("%s не открыт на дозапись: %w", path, err)
	}
	if _, err := f.WriteString(addition); err != nil {
		f.Close()
		return fmt.Errorf("%s не дописан: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("%s не дописан: %w", path, err)
	}

	// Независимое ревью (раунд 2): голый `git commit` без pathspec фиксирует
	// весь индекс, а не только эту правку — рабочая папка переиспользуется
	// без reset/clean между прогонами (workspace.Ensure), и там мог
	// остаться застейдженный кусок чужой, ещё не докоммиченной работы (или
	// вовсе неразрешённые записи от конфликта). Pathspec прямо на commit
	// (не только на add) — это git commit --only по сути: фиксирует
	// изменения только по названному пути, что бы ещё ни было в индексе
	// (проверено вживую: `git commit -m ... -- <path>` оставляет прочий
	// застейдженный файл на месте нетронутым). Неудачу самой правки (не
	// нашла .comet/config.yaml, не смогла дописать) это не касается —
	// та по-прежнему возвращается выше как настоящая ошибка.
	if err := gitInWorkdir(workdir, "add", "--", CometConfigFile); err != nil {
		return nil // best-effort: файл уже дописан и защита уже действует, коммит не обязателен для корректности этого прогона
	}
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME="+hookConfigCommitName, "GIT_AUTHOR_EMAIL="+hookConfigCommitEmail,
		"GIT_COMMITTER_NAME="+hookConfigCommitName, "GIT_COMMITTER_EMAIL="+hookConfigCommitEmail)
	// Ошибка коммита (тоже best-effort, та же причина) — например,
	// «cannot do a partial commit during a merge» на рабочей папке
	// с настоящим незавершённым слиянием (воспроизведено вживую) — не
	// возвращается наверх: до появления этого самокоммита вся правка
	// .comet/config.yaml была best-effort, и превращать её в обязательное
	// условие запуска роли (PrepareInput отказал бы целиком) — обменять
	// редкий, терпимый случай (правка осталась незакоммиченной до
	// следующего раза) на куда более тяжёлый (задача не стартует, пока
	// человек не почистит рабочую папку руками). Но не молча: до этой
	// правки неудача коммита не оставляла в логе раннера ни строки, хотя
	// staleCurrentChangeExclude (выше) — прецедент ровно такого же класса
	// git-отказа, случившегося на живом прогоне. Незакоммиченный `add`
	// сбрасывается тем же командой: иначе застейдженный .comet/config.yaml
	// пережил бы неудачу и попал бы в следующий коммит роли как будто
	// сделанный ею.
	if err := gitInWorkdirWithEnv(workdir, env, "commit", "-q", "--no-verify",
		"-m", "chore: add hook.allow_paths to .comet/config.yaml", "--", CometConfigFile); err != nil {
		fmt.Fprintf(os.Stderr, "runner: правка hook.allow_paths в %s дописана, но не закоммичена (%v) — застейджено сброшено, следующий прогон попробует снова\n",
			CometConfigFile, err)
		_ = gitInWorkdir(workdir, "reset", "-q", "--", CometConfigFile)
	}
	return nil
}

// gitInWorkdir/gitInWorkdirWithEnv — git-вызов раннера в рабочей папке,
// с выводом в ошибку при неудаче. --no-verify на коммите — та же причина,
// что у commitScript в internal/backends/sbx/clone.go: клиентский
// репозиторий мог обзавестись pre-commit-хуками, которым не место между
// ролью и служебной правкой обвязки.
func gitInWorkdir(workdir string, args ...string) error {
	return gitInWorkdirWithEnv(workdir, os.Environ(), args...)
}

func gitInWorkdirWithEnv(workdir string, env []string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", workdir}, args...)...)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return nil
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
