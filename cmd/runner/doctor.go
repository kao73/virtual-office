// runner doctor — преflight без побочных эффектов: инструменты, projects.local.yaml,
// tracker.yaml, живая JIRA, сеть песочницы, старые снапшоты офиса. Порядок стадий и то,
// что пропускает отказ каждой, — docs/superpowers/specs/2026-09-23-doctor-design.md,
// «Loading sequence and failure isolation». Одна command line, один финальный отчёт;
// падение середины не значит тишину по остальному — все стадии копят находки в один
// список и печатают его целиком, даже когда сами отказали на середине.
//
// check-id находок, по стадиям: tool:<имя>, sbx:network, office:stale-snapshots,
// config:home, config:projects.local.yaml, skip:project-dependent, config:tracker.yaml,
// skip:jira, cred:<ПЕРЕМЕННАЯ>, jira:reachability, jira:account, jira:fields (весь
// GET /field упал), jira:field:<owner|run_id|lease_until|attempts>, jira:link-type,
// jira:workflow:<проект>:<роль>, skip:workflow:<проект>.
package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"

	"github.com/kao73/virtual-office/internal/adapters/claude"
	"github.com/kao73/virtual-office/internal/backends/sbx"
	"github.com/kao73/virtual-office/internal/runagent"
	"github.com/kao73/virtual-office/internal/runner"
	"github.com/kao73/virtual-office/internal/tracker"
	"github.com/kao73/virtual-office/internal/tracker/jira"
)

// finding — одна строка отчёта: что проверялось, чем кончилось, что сказать
// человеку. check — устойчивый адрес находки (для тестов и сообщений
// о пропуске), не текст на естественном языке.
type finding struct {
	check string
	level string // "ok" | "warn" | "fail"
	msg   string
}

// lookPath — exec.LookPath за переменной: тест подменяет её, не трогая
// настоящий PATH машины. Тот же приём, что у cometExecutable в
// internal/pipeline/archive.go, только здесь подменяется сам поиск,
// а не имя утилиты.
var lookPath = exec.LookPath

// checkTool сообщает, резолвится ли утилита на PATH — по имени, а не единым
// «чего-то не хватает»: спецификация требует назвать конкретный инструмент.
func checkTool(name string) finding {
	if _, err := lookPath(name); err != nil {
		return finding{"tool:" + name, "fail", "не найден на PATH"}
	}
	return finding{"tool:" + name, "ok", "найден"}
}

// sbxNotice — sbx.BasePolicy{}.Notice() за переменной: BasePolicy.run
// приватен пакету sbx, и его нечем подменить снаружи. Тот же приём, что
// у lookPath.
var sbxNotice = func() (string, error) { return sbx.BasePolicy{}.Notice() }

// toolPresent — правда, если утилита резолвится на PATH. checkTool
// превращает то же самое в finding; сеть песочницы находке не нужна,
// только факт присутствия.
func toolPresent(name string) bool {
	_, err := lookPath(name)
	return err == nil
}

// checkSandboxNetwork сообщает про базовую политику сети машины — тем же
// вопросом, что задаёт раннер перед прогоном в песочнице
// (internal/backends/sbx.BasePolicy). Открытая сеть — находка, не отказ:
// спецификация прямо запрещает ронять doctor только из-за неё.
func checkSandboxNetwork() finding {
	notice, err := sbxNotice()
	if err != nil {
		return finding{"sbx:network", "warn", "базовая политика не прочитана: " + err.Error()}
	}
	if notice == "" {
		return finding{"sbx:network", "ok", "закрыта"}
	}
	return finding{"sbx:network", "warn", notice}
}

// dirSize — суммарный размер файлов в каталоге. Ошибку обхода не поднимаем:
// размер — справка человеку, а не то, ради чего стоит ронять весь отчёт
// (тот же выбор, что у internal/workspace.measure).
func dirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil //nolint:nilerr // недочитанный путь просто не считаем
		}
		if info, err := entry.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// checkStaleOfficeSnapshots перечисляет ${OFFICE_HOME}/office/<версия>/, кроме
// той, которую резолвит бегущий бинарник, и не трогает ни одну: удаление —
// решение человека, docs/notes/install.md, «runner doctor резолвит office
// ls/prune».
//
// o/resolveErr — результат уже сделанного в начале doctorCommand
// runner.ResolveOffice(runner.Resolve{}): он нужен и здесь, и (Task 10) для
// workflow.yaml, и второй раз его звать незачем.
func checkStaleOfficeSnapshots(o runner.Office, resolveErr error) finding {
	if resolveErr != nil || o.Source != runner.SourcePayload {
		// В режиме клона версий на диске не бывает вовсе — не отказ, а
		// «вопрос не о клоне». Отказ ResolveOffice сюда же: без личности
		// раннера сказать, что считать «текущей» версией, всё равно нечем.
		return finding{"office:stale-snapshots", "ok", "неприменимо (режим клона/разработки)"}
	}
	officeDir := filepath.Dir(o.Root)
	current := filepath.Base(o.Root)
	entries, err := os.ReadDir(officeDir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// Свежий office/: `runner init` его не создаёт, первая распаковка —
		// при первом реальном прогоне. Не найти каталога — не беда, а «ещё
		// нечего перечислять».
		return finding{"office:stale-snapshots", "ok", "нет"}
	case err != nil:
		return finding{"office:stale-snapshots", "fail", err.Error()}
	}
	var n int
	var total int64
	for _, e := range entries {
		if !e.IsDir() || e.Name() == current {
			continue
		}
		n++
		total += dirSize(filepath.Join(officeDir, e.Name()))
	}
	if n == 0 {
		return finding{"office:stale-snapshots", "ok", "нет"}
	}
	return finding{"office:stale-snapshots", "warn",
		fmt.Sprintf("устаревших снапшотов: %d, суммарно %s — убрать руками: rm -r %s/<версия>", n, size(total), officeDir)}
}

// checkCredentials — по одной находке на переменную окружения каждой учётки
// tracker.yaml (default и все roles), без повторов: учётка-дубликат
// (например, роль без своей пары UserEnv/SecretEnv, разделяющая общую)
// называется один раз, а не по разу на каждую роль — иначе один незаданный
// секрет выглядел бы как несколько разных бед.
func checkCredentials(a jira.Accounts) []finding {
	seen := map[jira.Account]bool{}
	var out []finding
	check := func(acc jira.Account) {
		if seen[acc] {
			return
		}
		seen[acc] = true
		for _, v := range []string{acc.UserEnv, acc.SecretEnv} {
			if v == "" {
				continue
			}
			if _, ok := os.LookupEnv(v); ok {
				out = append(out, finding{"cred:" + v, "ok", "задана"})
			} else {
				out = append(out, finding{"cred:" + v, "fail", "не задана"})
			}
		}
	}
	check(a.Default)
	for _, role := range slices.Sorted(maps.Keys(a.Roles)) {
		check(a.Roles[role])
	}
	return out
}

// doctorCommand — точка входа подкоманды. Флагов кроме --backend нет: ни
// --role (доктор не привязан к роли), ни --json (спецификация требует
// простого текста).
func doctorCommand(args []string, out io.Writer) error {
	fs := flags("doctor")
	backend := fs.String("backend", runagent.BackendLocal, "бэкенд агента: sbx или local — какие проверки бэкенда включать")
	if err := fs.Parse(args); err != nil {
		return err
	}

	var findings []finding
	report := func(f finding) { findings = append(findings, f) }

	// Stage 1a — инструменты, не зависящие ни от проектов, ни от трекера.
	report(checkTool("git"))
	report(checkTool(claude.Executable))
	if *backend == "sbx" {
		report(checkTool(sbx.Executable))
		if toolPresent(sbx.Executable) {
			report(checkSandboxNetwork())
		}
	}

	// runner.Resolve{} — нулевое значение, то есть Unpack: false: доктор не
	// распаковывает и не создаёт файлов — спецификация требует нулевых
	// побочных эффектов. office/officeErr используются здесь и (Task 10)
	// для workflow.yaml.
	office, officeErr := runner.ResolveOffice(runner.Resolve{})
	report(checkStaleOfficeSnapshots(office, officeErr))

	// Stage 2 — projects.local.yaml. Отказ здесь не крашит доктора и не
	// молчит о непроверенном: он называет файл и говорит, что зависящие от
	// него проверки пропущены — ровно то, что видит человек сразу после
	// runner init, пока не переименовал образец.
	home, err := runner.Home()
	if err != nil {
		report(finding{"config:home", "fail", err.Error()})
		return concludeExit(out, findings)
	}
	projects, err := tracker.LoadProjects(filepath.Join(home, tracker.ProjectsLocalFile))
	if err != nil {
		report(finding{"config:projects.local.yaml", "fail", err.Error()})
		report(finding{"skip:project-dependent", "warn",
			"tool(comet)/cred/JIRA-проверки пропущены: projects.local.yaml не загрузился"})
		return concludeExit(out, findings)
	}

	for _, key := range projects.Keys() {
		if projects[key].Forge != "" {
			report(checkTool("comet"))
			break
		}
	}

	jiraProjects := projects.For("jira")
	if len(jiraProjects) == 0 {
		return concludeExit(out, findings)
	}

	// Stage 3 — tracker.yaml, только когда хоть один проект назвал jira.
	cfg, err := jira.LoadConfig(filepath.Join(home, jira.TrackerFile))
	if err != nil {
		report(finding{"config:tracker.yaml", "fail", err.Error()})
		report(finding{"skip:jira", "warn", "cred/JIRA-проверки пропущены: tracker.yaml не загрузился"})
		return concludeExit(out, findings)
	}

	for _, f := range checkCredentials(cfg.Accounts) {
		report(f)
	}

	return concludeExit(out, findings)
}

// concludeExit печатает каждую находку в порядке появления и отказывает,
// только если хоть одна — fail: warn ни на что не влияет, ok — тоже.
func concludeExit(out io.Writer, findings []finding) error {
	var failed int
	for _, f := range findings {
		fmt.Fprintf(out, "%-4s %-28s %s\n", f.level, f.check, f.msg)
		if f.level == "fail" {
			failed++
		}
	}
	if failed == 0 {
		return nil
	}
	return fmt.Errorf("doctor: %d check(s) failed", failed)
}
