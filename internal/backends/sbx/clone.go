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
// и кэш локального исполнения .comet/runtime/ исключены из git нарочно
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
		// не закоммитила .comet/config.yaml, и .comet/runtime/ тем более не
		// заведён) — тогда sbx cp либо откажет, либо поведёт себя
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

	if err := clearStaleCometLocks(ctx, name, primary, present, run); err != nil {
		return nil, err
	}
	return present, nil
}

// cometRuntimeDir — то же самое имя, которым l.Clone.Dirs называет .comet/
// runtime (cmd/run-agent/main.go).
const cometRuntimeDir = ".comet/runtime"

// clearStaleCometLocks убирает locks/.coordinator, занесённые cloneSyncIn
// вместе с остальным .comet/runtime, прежде чем в свежей песочнице стартует
// агент.
//
// Независимое ревью нашло: эти файлы несут pid+hostname песочницы, которая
// их создала, координатор их живость не проверяет и отказывает навсегда,
// пока кто-то не удалит файл руками (docs/notes/stage-5-live-backlog.md,
// «Находки живого прогона EXP-2», воспроизведено вживую четыре раза подряд,
// `exit 73`). Это дефект самого CLI comet — но перенос .comet/runtime
// между эфемерными песочницами именно этим кодом и есть то, что превращает
// одну мёртвую блокировку одной снесённой песочницы в постоянную блокировку
// для всех последующих прогонов той же рабочей папки. Остальной .comet/
// runtime (changes/, transactions/) переносится как есть — это то самое
// исполнение, ради которого --clone вообще существует; локи — не оно.
//
// Второе, независимое ревью нашло, что это лечит только путь --clone —
// сегодняшний продовый конвейер идёт бинд-маунтом (internal/pipeline/
// agent.go, «Options.Clone нарочно не выставляется») с переиспользуемым
// worktree (workspace.Manager.Ensure), а не эфемерной песочницей: тот же
// класс мёртвого лока там чистит runner.ClearStaleCometLocks
// (internal/runner/input.go), вызванная из PrepareInput под тем же барьером
// worktree, что и здесь — под замком sbx.
func clearStaleCometLocks(ctx context.Context, name, primary string, present []string, run step) error {
	if !slices.Contains(present, cometRuntimeDir) {
		return nil // .comet/runtime не заносился этим прогоном
	}
	locks := filepath.Join(primary, runner.CometRuntimeLocksRel)
	if err := run(ctx, "exec", name, "rm", "-rf", locks); err != nil {
		return fmt.Errorf("устаревшие блокировки Comet Native (%s) не убраны из свежей песочницы: %w", locks, err)
	}
	return nil
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
// и забирает каталоги вне git (агент мог дописать в них: .agent/result.json,
// .comet/runtime/ при первом comet native new).
//
// Порядок — commitLeftovers, потом fetchBranch, потом каталоги — и это
// не произвольный порядок, а два независимых ограничения разом:
//
//  1. commitLeftovers — раньше fetchBranch, а не после (как было раньше,
//     см. историю этого файла и Task 23 плана): коммит-подчистка обязана
//     появиться в истории песочницы прежде, чем оттуда сходит git fetch —
//     иначе она останется внутри снесённой песочницы точно так же, как то,
//     что она спасает.
//  2. fetchBranch — раньше каталогов: часть l.Clone.Dirs (например,
//     .comet/config.yaml, если роль его коммитит — см. roles/analyst/role.md)
//     на самом деле лежит и в git. Занеси её на хост раньше git-слияния —
//     и следующее за ней `git merge --ff-only` откажет: «your local
//     changes... would be overwritten by merge» (воспроизведено вживую при
//     разработке). Слиянием сперва — а уже потом поверх чистого дерева —
//     каталоги кладутся без риска зацепить то, что git и так вот-вот
//     принесёт сам.
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
// Возвращает наружу закоммиченное (fetchBranch, включая коммит-подчистку
// commitLeftovers) и явно названные l.Clone.Dirs. До Задачи 23 незакоммиченный
// остаток снос песочницы безвозвратно терял и только логировал это
// (warnUncommitted) — живым прогоном найдено, что это не теоретический
// случай: ни implementer, ни reviewer не коммитят
// docs/comet/changes/<name>/comet-state.yaml, который `comet native`
// постоянно правит своими CLI-вызовами как побочный эффект протокола, и
// состояние Comet Native терялось на каждом --clone-прогоне после первого.
// commitLeftovers ниже это чинит общим приёмом, а не точечно под один файл.
//
// Неудача commitLeftovers копится в sweepErr и возвращается только после
// fetchBranch И после цикла по l.Clone.Dirs ниже, а не вместо них: сеть
// безопасности не вправе отменить ни подтяжку настоящих коммитов агента,
// ни подтяжку .agent/result.json — независимое ревью нашло живьём
// воспроизводимый сценарий (усечение по таймауту прямо посреди `git
// commit` роли оставляет `.git/index.lock`, из-за которого сама подчистка
// не может даже начать `git add`), где ранний return sweepErr стирал бы
// час честной работы агента ради спасения объедков, которых, возможно, и не
// было — и отдельно нашло, что до этой правки тот же ранний return ещё
// и терял настоящий result.json уже состоявшегося прогона, превращая
// здоровый исход в синтетический `failed` на runagent.ReadResult. fetchBranch
// и цикл по Dirs тем самым остаются безусловными — ровно тем свойством,
// которого требует doc-комментарий fetchBranch («зовётся при любом
// исходе прогона»).
func cloneSyncOut(ctx context.Context, name string, l *runner.Launch, run step, presentOnEntry []string, log io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), cloneSyncTimeout)
	defer cancel()

	primary := l.Workspaces[0].Path

	sweepErr := commitLeftovers(ctx, log, name, primary, l.Clone.Dirs, run)
	if sweepErr != nil {
		fmt.Fprintf(log, "\nпесочница %s: подчистка незакоммиченного не удалась: %v\n", name, sweepErr)
	}
	if err := fetchBranch(ctx, name, primary, l.Clone); err != nil {
		return err
	}

	// sweepErr возвращается только после этого цикла, а не раньше него:
	// независимое ревью нашло, что прежний ранний return sweepErr здесь
	// пропускал и подтяжку .agent/result.json — того же рода потеря, что
	// и у самой fetchBranch выше (см. её doc-комментарий), только для
	// каталогов вместо коммитов. Сеть безопасности commitLeftovers не
	// вправе отменять ни то, ни другое.
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
	return sweepErr
}

// cloneSweepName/cloneSweepEmail — личность коммита, которым commitLeftovers
// сама сохраняет незакоммиченный остаток после агента, тем же приёмом, что
// archiveCommitName/archiveCommitEmail в internal/pipeline/archive.go: это
// не решение роли, а сеть безопасности обвязки — и в git blame это обязано
// быть видно, а не выглядеть так, будто роль сама решила это закоммитить.
//
// Задаются переменными окружения git-процесса (внутри commitScript ниже),
// а не `-c user.name=...`: у git переменные окружения перебивают -c-конфиг.
// Сегодня в этом конкретном exec-вызове никакого чужого GIT_AUTHOR_NAME
// нет — identityVars роли идут `--env`-флагами только на execArgs
// (internal/backends/sbx/sbx.go), запуск самого агента, и в createArgs
// песочницы, а тем более в отдельных exec-вызовах commitLeftovers, их нет
// (независимое ревью проверило по исходнику). Выбор — на будущее, не
// заплатка под сегодняшний баг: `--env` для этих вызовов однажды может
// понадобиться по другой причине, и тогда `-c` тихо проиграл бы ей.
const (
	cloneSweepName  = "clone-sweep"
	cloneSweepEmail = "clone-sweep@office.local"
)

// mergeCheckScript/dirtyCheckScript/addScript/stagedCheckScript/commitScript
// — шаги commitLeftovers, каждый своим exec-вызовом, а не одним большим
// скриптом: между add и commit нужен настоящий Go-условный переход (см.
// commitLeftovers), которого одна строка shell не даёт без потери точности
// лога. $1 — путь (primary) везде; addScript получает вдобавок $2.. — имена
// dirs (см. dirs у commitLeftovers), которые нужно исключить из коммита-
// подчистки. Путь и имена — позиционные аргументы шелла, не подставлены
// в текст скрипта конкатенацией: конкатенация ломается на путях с пробелом
// или другим спецсимволом шелла — раньше это молча превращало «дерево
// грязное» в «дерево чистое» (независимое ревью, живой сценарий с путём
// вроде «/Users/x/my repo»), а не только теоретический риск инъекции
// (сам путь задаёт не агент).
//
// add -A -- . затем reset -q -- на dirs, а не `add -A -- . :!dir`: то же
// исключение, что и pathspec-негация даёт для уже отслеживаемых путей, но
// без её слепого пятна. dirs — как раз то, что ExcludeAgentDir/
// ExcludeCometRuntime заносят в .git/info/exclude в настоящем прогоне;
// у git «add» с пathspec, буквально называющим уже игнорируемый путь
// (даже в исключающей форме «:!path»), падает с ненулевым кодом и
// «paths are ignored by one of your .gitignore files» — не предупреждение,
// а настоящий отказ, `advice.addIgnoredFile=false` его не лечит (только
// прячет текст). Живой прогон на demo-3 воспроизвёл это: add на
// «.agent»/«.comet/current-change.json»/«.comet/runtime», уже сидящих
// в info/exclude, валил всю подчистку. add -A -- . без явных имён путей
// такого не делает — тихо пропускает уже игнорируемое, как обычно; reset
// следом снимает из индекса то немногое, что осталось не проигнорированным
// (сценарий «защиты в глубину» ниже, когда info/exclude почему-то пуст) —
// «reset -q» на путь, которого нет в индексе или вовсе на диске, безвреден
// (код 0, см. TestCommitLeftoversExcludesNamedDirsEvenWithoutGitExclude).
// mergeCheckScript/dirtyCheckScript отвечают тремя разными кодами выхода,
// а не двумя: 0 — найдено (unmerged-записи / грязное дерево), 1 — легитимное
// «нет» (git отработала, но ничего не нашла), 2 — сама git-команда упала
// (побитый индекс, ENOSPC, унесённый primary), а не просто ничего не нашла.
// Первая версия пыталась различать эти «1» и «2» по тексту (искала «fatal:»,
// потом — свой собственный маркер в выводе) — оба раза ошибочно: текст «fatal:»
// не покрывает «error: ...» и не-git отказы, а собственный маркер оказывается
// частью текста САМОГО СКРИПТА и потому виден в обёрнутой ошибке (sbxRun/
// execStep кладут туда и args, откуда исполнялась команда) при ЛЮБОМ отказе —
// не только когда его напечатал echo. Явный, отдельный код выхода этой
// путаницы не знает: gitCheckFailed ниже читает его через errors.As, а не
// сравнивает текст.
const (
	mergeCheckScript = `out=$(git -C "$1" ls-files --unmerged) || exit 2; [ -n "$out" ]`
	dirtyCheckScript = `out=$(git -C "$1" status --porcelain) || exit 2; [ -n "$out" ]`

	addScript         = `primary=$1; shift; git -C "$primary" add -A -- . && { [ "$#" -eq 0 ] || git -C "$primary" reset -q -- "$@"; }`
	stagedCheckScript = `git -C "$1" diff --cached --quiet`
)

// commitScript — --no-verify: клиентский репозиторий мог обзавестись
// git-хуками уже внутри песочницы (npm install ставит husky/lefthook
// в .git/hooks), и pre-commit-хук, упавший на случайном мусоре, не должен
// ронять весь прогон ради коммита, который сам по себе не обязателен.
const commitScript = `GIT_AUTHOR_NAME="` + cloneSweepName + `" GIT_AUTHOR_EMAIL="` + cloneSweepEmail + `" ` +
	`GIT_COMMITTER_NAME="` + cloneSweepName + `" GIT_COMMITTER_EMAIL="` + cloneSweepEmail + `" ` +
	`git -C "$1" commit -q --no-verify -m "chore: preserve sandbox-local changes left uncommitted by the run"`

// mergeInProgress — есть ли в песочнице незавершённое слияние (неразрешённые
// записи индекса, `git ls-files --unmerged»). `git add -A` на таком дереве
// разрешил бы конфликтные пути в индексе прямо с текстом «<<<<<<<», а коммит
// поверх них объявил бы конфликт разрешённым — порча ветки задачи хуже, чем
// просто не спасти незакоммиченное в этом одном случае: его снос песочницы
// унесёт, как и до этой правки, не тише и не хуже.
//
// Не по файлам-маркерам (MERGE_HEAD и т. п.): независимое ревью нашло живой
// сценарий без единого из них — конфликтующий `git stash pop`
// (`implementer` может засташить перед сверкой, см. roles/implementer/role.md)
// оставляет ровно те же неразрешённые записи индекса, но ни одного
// стандартного файла-маркера слияния. `ls-files --unmerged` смотрит на сам
// индекс, а не на то, какая команда его туда довела, — шире и вернее.
func mergeInProgress(ctx context.Context, name, primary string, run step) bool {
	err := run(ctx, "exec", name, "sh", "-c", mergeCheckScript, "sh", primary)
	if err == nil {
		return true // найдены неразрешённые записи
	}
	// mergeCheckScript само различает «неразрешённых записей нет» (код 1,
	// $out пуст) от «git ls-files сама упала» (побитый индекс, ENOSPC,
	// унесённый primary — код 2): независимое ревью нашло, что более ранняя
	// версия на `| grep -q .` не различала их вовсе, а её первая замена
	// различала только по тексту («fatal:», затем свой маркер) — оба раза
	// ошибочно, см. doc-комментарий gitCheckFailed. Настоящую беду считаем
	// «слияние идёт» — портить ветку хуже, чем лишний раз пропустить
	// подчистку (см. doc-комментарий выше).
	return gitCheckFailed(err)
}

// gitCheckFailed различает код выхода 2 (mergeCheckScript/dirtyCheckScript
// сами дают его только когда git внутри упала) от кода 1 (легитимное «не
// найдено») по номеру, не по тексту: sbxRun и execStep (clone_test.go) оба
// оборачивают исходный `*exec.ExitError` через `%w`, errors.As достаёт его
// сквозь обёртку. Текстовый маркер (более ранняя версия) для этого не
// годился в принципе: он живёт в самом ТЕКСТЕ СКРИПТА, а sbxRun/execStep
// оба кладут в текст ошибки не только вывод команды, но и её args — где
// маркер тем самым виден при любом отказе, напечатал его echo или нет.
func gitCheckFailed(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == 2
}

// commitLeftovers сохраняет то, что агент оставил незакоммиченным внутри
// песочницы, отдельным коммитом от служебной личности выше — не подменяя
// собой дисциплину роли «коммить то, что сделал, а не всё подряд»
// (roles/*/role.md, «Как коммитить»), а страхуя её: --clone возвращает
// наружу только закоммиченное (fetchBranch) и явно названные dirs, а снос
// песочницы следом безвозвратно уносил всё остальное — до Задачи 24 только
// предупреждая об этом в log (warnUncommitted), не спасая.
//
// dirs — то же самое, что l.Clone.Dirs (конверт обмена: .agent,
// .comet/runtime), явным исключением из
// коммита-подчистки (add -A -- . затем reset -q -- на dirs — см.
// doc-комментарий addScript выше), а не косвенно через
// `.git/info/exclude`. Раньше на это исключение полагались только через
// syncExcludeFile, копию, которая тиха и необязательна (пустой источник
// хоста — не ошибка) и резолвит путь иначе, чем ExcludeAgentDir (`git
// rev-parse --git-common-dir»); независимое ревью нашло, что расхождение
// этих двух путей молча оставляет .git/info/exclude в песочнице пустым —
// тогда явное исключение здесь остаётся единственным, что не даёт
// системному промпту, паспорту прогона и `.agent/result.json» уехать
// в коммит ветки задачи клиентского проекта.
//
// Между add и «нечего коммитить» — отдельный шаг (stagedCheckScript), а не
// просто «commit и посмотреть на код возврата»: то, что было грязным по
// `git status`, не обязано остаться застейдженным после add -A -- . плюс
// reset на dirs — reset мог снять со стейджа всё целиком (именно тот
// случай, ради которого добавлено исключение выше), или изменение вовсе не
// стейджится добавлением (например, только указатель подмодуля).
// Независимое ревью нашло оба сценария живьём: без этого шага `git commit`
// выходил с «nothing to commit», commitLeftovers превращала это в ошибку,
// а cloneOutcome — совсем здоровый прогон агента в код -1. «Нечего
// коммитить после фильтрации» — не беда, а такой же законный исход, как
// «дерево изначально было чистым».
//
// Неудача настоящего add/commit — другое дело, настоящая ошибка: cloneSyncOut
// копит её и пробрасывает дальше уже после fetchBranch (см. её
// doc-комментарий) — цена та же, что у потери коммитов агента, которую
// fetchBranch тоже не прощает молча.
func commitLeftovers(ctx context.Context, log io.Writer, name, primary string, dirs []string, run step) error {
	if mergeInProgress(ctx, name, primary, run) {
		fmt.Fprintf(log, "\nпесочница %s: незавершённое слияние — подчистка пропущена, чтобы не "+
			"закоммитить конфликтные маркеры как разрешённые\n", name)
		return nil
	}

	if err := run(ctx, "exec", name, "sh", "-c", dirtyCheckScript, "sh", primary); err != nil {
		// Та же пайп-неоднозначность, что у mergeCheckScript (см.
		// gitCheckFailed): настоящий отказ git status здесь раньше молча
		// читался как «дерево чистое» и терял работу агента без единой
		// строки в логе. Настоящую беду пробрасываем дальше как ошибку —
		// не тише, чем неудача самого add/commit ниже.
		if gitCheckFailed(err) {
			return fmt.Errorf("рабочее дерево внутри песочницы %s не проверено: %w", name, err)
		}
		return nil // нечего сохранять
	}

	addArgs := append([]string{"exec", name, "sh", "-c", addScript, "sh", primary}, dirs...)
	if err := run(ctx, addArgs...); err != nil {
		return fmt.Errorf("незакоммиченная работа внутри песочницы %s не занесена в индекс: %w", name, err)
	}

	if err := run(ctx, "exec", name, "sh", "-c", stagedCheckScript, "sh", primary); err == nil {
		return nil // после add -A -- . :!dir застейдженного не осталось — нечего коммитить, это не беда
	}

	if err := run(ctx, "exec", name, "sh", "-c", commitScript, "sh", primary); err != nil {
		return fmt.Errorf("незакоммиченная работа внутри песочницы %s не сохранена: %w", name, err)
	}

	fmt.Fprintf(log, "\nпесочница %s: внутри осталась незакоммиченная работа — сохранена отдельным "+
		"коммитом (chore: preserve sandbox-local changes), не от лица роли\n", name)
	return nil
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
