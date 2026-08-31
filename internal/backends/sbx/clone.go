package sbx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kao73/virtual-office/internal/runner"
)

// cloneSyncTimeout — один общий предел на весь заход синхронизации --clone
// (все cp/fetch/merge внутри cloneSyncIn или cloneSyncOut вместе, не каждый
// по отдельности): без предела зависший sbx cp или git fetch вешает раннер
// навсегда. На выгрузке
// после прогона цена этого выше, чем зависшая команда, — не подтянутая
// вовремя работа агента унесётся следующим за ней defer remove безвозвратно,
// поэтому cloneSyncOut берёт собственный таймаут поверх context.Background(),
// тем же приёмом, что и remove() в sbx.go, а не поверх ctx вызывающего:
// прогон мог уже быть отменён по своему таймауту, а подтягивать работу всё
// равно нужно.
const cloneSyncTimeout = 5 * time.Minute

// excludeFile — где git хранит непубликуемые правила исключения одной
// рабочей копии; в отличие от .gitignore, этот файл не коммитится и не
// путешествует с git clone. runner.ExcludeAgentDir/ExcludeCometRuntime уже
// написали его на хосте раньше, чем эта песочница вообще создана
// (PrepareInput зовётся до Execute), — а sbx create --clone заводит внутри
// контейнера свежий git clone со свежим, пустым info/exclude. Без переноса
// этого файла агент внутри песочницы увидел бы .agent/.comet/runtime как
// обычную грязь рабочего дерева, а не как исключённое, — role.md обещает
// роли обратное («остальное раннер сам держит вне git»), и `git add -A`
// (от которого role.md отговаривает только словами) смёл бы конверт обмена
// в коммит задачи. Измерено вживую: sbx cp кладёт файл поверх уже
// существующего внутри клона .git/info/exclude штатно, без chown — git
// сам его только читает, а cp оставляет за агентом право на чтение.
//
// Путь — не через `git rev-parse --git-common-dir`, а напрямую: primary
// (l.Workspaces[0].Path) не бывает worktree'ем в принципе — --clone сам
// отказывает на этом ещё на sbx create («not supported when run from
// a Git worktree», измерено вживую), а только у worktree `.git` — файл,
// а не каталог, и общий git-каталог лежит не рядом с рабочей копией.
const excludeFile = ".git/info/exclude"

// cloneSyncIn заносит в песочницу --clone то, чего не видно git-клону:
// правила git-исключения каталога обмена (excludeFile) и сами каталоги вне
// git (l.Clone.Dirs) — git-клон копирует только закоммиченное, а .agent
// и машинно-локальные части .comet/ исключены из git нарочно
// (runner.ExcludeAgentDir, runner.ExcludeCometRuntime).
//
// Возвращает те из l.Clone.Dirs, что реально нашлись на хосте на момент
// прогона: cloneSyncOut использует этот список, чтобы отличить «каталога
// в песочнице не было и на входе — не наше упущение» от «агент должен был
// его увидеть, а не увидел — это беда».
func cloneSyncIn(ctx context.Context, name string, l *runner.Launch, run step) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, cloneSyncTimeout)
	defer cancel()

	primary := l.Workspaces[0].Path

	if err := syncExcludeFile(ctx, name, primary, run); err != nil {
		return nil, fmt.Errorf("правила git-исключения не занесены: %w", err)
	}

	var present, paths []string
	for _, dir := range l.Clone.Dirs {
		src := filepath.Join(primary, dir)
		switch _, err := os.Stat(src); {
		case errors.Is(err, os.ErrNotExist):
			continue // .comet/ бывает не заведён вовсе — задача без изменения Comet Native
		case err != nil:
			return nil, fmt.Errorf("%s не проверен: %w", src, err)
		}
		// .comet/ может не существовать внутри свежего клона вовсе (роль ещё
		// не закоммитила .comet/config.yaml) — тогда sbx cp либо откажет, либо
		// (для одиночного файла вроде current-change.json) поведёт себя
		// недокументированно. Заводим родителя явно, а не полагаемся на то,
		// что цель уже есть.
		if err := run(ctx, "exec", name, "mkdir", "-p", filepath.Dir(src)); err != nil {
			return nil, fmt.Errorf("%s внутри песочницы не заведён: %w", filepath.Dir(src), err)
		}
		if err := run(ctx, "cp", src, name+":"+filepath.Dir(src)+"/"); err != nil {
			return nil, err
		}
		present = append(present, dir)
		paths = append(paths, src)
	}
	if len(paths) == 0 {
		return present, nil
	}

	// sbx cp заносит каталог с хостовым владельцем (измерено вживую): без
	// chown агент внутри песочницы (uid agent) не может в него писать.
	args := append([]string{"exec", "-u", "root", name, "chown", "-R", "agent:agent"}, paths...)
	if err := run(ctx, args...); err != nil {
		return nil, err
	}
	return present, nil
}

// syncExcludeFile переносит хостовые правила git-исключения (excludeFile)
// внутрь свежего git-клона песочницы. Источника может не быть только если
// эта рабочая папка никогда не проходила через runner.PrepareInput —
// такого сегодня не бывает ни у одного вызывающего, но падать на этом
// незачем: без файла агент просто увидит .agent/.comet как некомментированную
// грязь, что не хуже сегодняшнего поведения без --clone вовсе.
func syncExcludeFile(ctx context.Context, name, primary string, run step) error {
	src := filepath.Join(primary, excludeFile)
	switch _, err := os.Stat(src); {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("%s не проверен: %w", src, err)
	}
	return run(ctx, "cp", src, name+":"+filepath.Dir(src)+"/")
}

// notFoundInContainer — как sbx cp сообщает про путь, которого в песочнице
// не нашлось (измерено вживую). Текст, а не код выхода: у step нет кода
// выхода, только обёрнутая ошибка с полным выводом внутри.
const notFoundInContainer = "not found in container"

// cloneSyncOut подтягивает ветку задачи из песочницы в l.Clone.FetchInto
// и забирает каталоги обмена (агент мог дописать в них: .agent/result.json,
// .comet/current-change.json при первом comet native new).
//
// Ветка — раньше каталогов, и это не произвольный порядок: часть
// l.Clone.Dirs (например, .comet/config.yaml, если роль его коммитит —
// см. roles/analyst/role.md) на самом деле лежит и в git. Занеси её на хост
// раньше git-слияния — и следующее за ней `git merge --ff-only` откажет:
// «your local changes... would be overwritten by merge» (воспроизведено
// вживую при разработке). Слиянием сперва — а уже потом поверх чистого
// дерева — каталоги кладутся без риска зацепить то, что git и так вот-вот
// принесёт сам.
//
// Зовётся при любом исходе прогона, включая усечение по таймауту или
// пределу шагов: снос песочницы следом безвозвратно унесёт коммиты агента,
// а по правилам роли усечённый прогон не начинают заново — продолжают
// с места, до которого дошли, и без синхронизации продолжать было бы
// не с чем.
//
// presentOnEntry — список l.Clone.Dirs, реально занесённых cloneSyncIn
// (её собственный возврат): «в песочнице не нашлось» терпимо только для
// каталога, которого не было там и на входе — для остальных это
// настоящая беда, а не законное «роль его не завела».
//
// Возвращает наружу только закоммиченное (fetchBranch) и явно названные
// l.Clone.Dirs — незакоммиченные правки где угодно ещё в дереве задачи снос
// песочницы унесёт безвозвратно, и это не обходится молча: warnUncommitted
// ниже пишет об этом в log. Полноценно тащить остаток — отдельная, более
// крупная задача (см. Task 21 плана): цена ошибки здесь ниже, чем у Critical
// находок этого раунда, — role.md рассчитан на переиспользуемую рабочую
// папку только там, где --clone сегодня не подключён (production-конвейер),
// а eval-roles каждый прогон материализует фикстуру заново и никогда не
// возвращается к тому же каталогу.
func cloneSyncOut(ctx context.Context, name string, l *runner.Launch, run step, presentOnEntry []string, log io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), cloneSyncTimeout)
	defer cancel()

	primary := l.Workspaces[0].Path

	if err := fetchBranch(ctx, name, primary, l.Clone); err != nil {
		return err
	}
	warnUncommitted(ctx, log, name, primary, run)

	for _, dir := range l.Clone.Dirs {
		dst := filepath.Join(primary, dir)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fmt.Errorf("%s на хосте не заведён: %w", filepath.Dir(dst), err)
		}
		err := run(ctx, "cp", name+":"+dst, filepath.Dir(dst)+"/")
		if err == nil {
			continue
		}
		// Каталога в песочнице могло не быть вовсе: не каждая задача заводит
		// .comet/. Различаем по тексту, а не молчим о разнице совсем: если
		// формат сообщения sbx когда-нибудь сменится, эта проверка просто
		// перестанет совпадать и вернёт настоящую ошибку вместо тихого
		// пропуска — безопасное направление отказа. Но только для того,
		// чего не было и на входе: .agent заносит cloneSyncIn сама, и его
		// отсутствие на выходе — не законный случай, а повод не молчать.
		if strings.Contains(err.Error(), notFoundInContainer) && !slices.Contains(presentOnEntry, dir) {
			continue
		}
		return err
	}
	return nil
}

// warnUncommitted пишет в log предупреждение, если внутри песочницы осталась
// незакоммиченная работа: --clone возвращает наружу только коммиты и явно
// названные l.Clone.Dirs, а снос песочницы следом безвозвратно унесёт всё
// остальное — правки, которые агент не успел закоммитить где угодно ещё
// в дереве задачи.
//
// Молчит, а не роняет прогон: незакоммиченный мусор — не всегда беда (роль
// reviewer вообще не коммитит), и цена ложной тревоги на каждый обычный
// прогон выше цены редкой потери, которую эта проверка хотя бы делает видимой
// в логе вместо полной тишины. `grep -q .` внутри песочницы даёт булев
// сигнал через код возврата: непустой `git status --porcelain` — совпадение
// и код 0, пустой — код 1, что step оборачивает в ошибку и здесь просто
// молча означает «нечего сообщать».
func warnUncommitted(ctx context.Context, log io.Writer, name, primary string, run step) {
	if err := run(ctx, "exec", name, "sh", "-c",
		"git -C "+primary+" status --porcelain | grep -q ."); err == nil {
		fmt.Fprintf(log, "\nпесочница %s: внутри осталась незакоммиченная работа — --clone "+
			"подтягивает наружу только коммиты и .agent/.comet, снос песочницы унесёт остальное безвозвратно\n", name)
	}
}

// fetchBranch подтягивает ветку задачи из песочницы в l.Clone.FetchInto —
// обычно исходный worktree, а не одноразовый клон-источник (l.Workspaces[0]),
// который будет снесён вместе с песочницей.
//
// sbx create --clone уже завёл в клоне-источнике ремоут sandbox-<имя> на
// git-daemon песочницы — URL читается из него, а не собирается заново: порт
// daemon'а sbx выбирает сам и больше нигде не публикует (измерено вживую).
//
// Слияние — fast-forward-only: раннер даёт на рабочую папку задачи ровно один
// прогон за раз, и не-перемотка значит, что её тем временем двигал кто-то
// ещё, — тогда лучше отказать явно, чем затереть чужую работу. Проверка
// текущей ветки FetchInto перед слиянием — по той же причине: --ff-only сам
// по себе перематывает то, что сейчас выкачено, а не обязательно c.Branch,
// и без явной проверки чужая ветка была бы передвинута молча.
func fetchBranch(ctx context.Context, name, primary string, c *runner.CloneSync) error {
	remote := "sandbox-" + name

	url, err := gitOutput(ctx, primary, "remote", "get-url", remote)
	if err != nil {
		return fmt.Errorf("URL песочницы не прочитан из %s: %w", primary, err)
	}

	current, err := gitOutput(ctx, c.FetchInto, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return fmt.Errorf("текущая ветка %s не определена: %w", c.FetchInto, err)
	}
	if current != c.Branch {
		return fmt.Errorf("%s стоит на ветке %q, а не на ожидаемой %q — перематывать некуда", c.FetchInto, current, c.Branch)
	}

	if _, err := gitOutput(ctx, c.FetchInto, "fetch", "--quiet", url, c.Branch); err != nil {
		return fmt.Errorf("ветка %s не подтянута из песочницы: %w", c.Branch, err)
	}
	if _, err := gitOutput(ctx, c.FetchInto, "merge", "--ff-only", "FETCH_HEAD"); err != nil {
		return fmt.Errorf("ветка %s не перемотана в %s: %w", c.Branch, c.FetchInto, err)
	}
	return nil
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

// cloneOutcome решает код возврата и ошибку Run по трём независимым
// сигналам: истекло ли время, чем ответил exec-процесс агента и подтянулась
// ли работа агента обратно из песочницы (cloneErr, всегда nil без --clone).
// Выделено отдельной функцией — здесь дороже всего молча потерять коммиты
// агента, и приоритет между сигналами стоит того, чтобы его проверял юнит-тест
// без настоящего sbx: cloneErr обязан подменить собой success/exitErr-путь
// (иначе прогон выглядел бы состоявшимся, хотя работа не долетела до
// worktree), но только дописываться к уже объясняющей ошибке на
// timeout/runErr-путях, а не затирать её.
func cloneOutcome(log io.Writer, name string, timedOut bool, timeout time.Duration, runErr, cloneErr error) (int, error) {
	var exitErr *exec.ExitError
	switch {
	case timedOut:
		logCloneErr(log, name, cloneErr)
		// Обёрнутая runner.ErrRunTimeout, а не просто текст: вышедшее время
		// означает «работал и не успел», а прочие беды этой функции — «прогона
		// не было». По коду -1 они неразличимы, по errors.Is — да
		// (internal/runagent/runagent.go:terminationOf опирается именно на неё).
		return -1, runner.RunTimeout(timeout, "песочница "+name)
	case errors.As(runErr, &exitErr):
		if cloneErr != nil {
			return -1, fmt.Errorf("работа агента не подтянута из песочницы %s: %w", name, cloneErr)
		}
		return exitErr.ExitCode(), nil
	case runErr != nil:
		logCloneErr(log, name, cloneErr)
		return -1, fmt.Errorf("агент не запущен в песочнице %s: %w", name, runErr)
	case cloneErr != nil:
		return -1, fmt.Errorf("работа агента не подтянута из песочницы %s: %w", name, cloneErr)
	default:
		return 0, nil
	}
}

// logCloneErr пишет в лог прогона беду синхронизации --clone, когда сам
// прогон уже кончился ненормально (таймаут, необъяснимая ошибка exec) —
// такую беду не заменяют собой возвращаемую ошибку, а дописывают к ней:
// первопричина уже названа, и она важнее вторичной потери.
func logCloneErr(log io.Writer, name string, err error) {
	if err != nil {
		fmt.Fprintf(log, "\nпесочница %s: работа агента не подтянута обратно: %v\n", name, err)
	}
}
