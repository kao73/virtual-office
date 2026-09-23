// runner doctor — диагностика без побочных эффектов: инструменты, projects.local.yaml,
// tracker.yaml, живая JIRA, сеть песочницы, старые снапшоты офиса. Порядок стадий и то,
// что пропускает отказ каждой, — docs/superpowers/specs/2026-09-23-doctor-design.md,
// «Loading sequence and failure isolation». Одна command line, один финальный отчёт;
// падение середины не значит тишину по остальному — все стадии копят находки в один
// список и печатают его целиком, даже когда сами отказали на середине.
//
// check-id находок, по стадиям: tool:<имя>, sbx:network, office:resolve,
// office:stale-snapshots, config:home, config:projects.local.yaml,
// skip:project-dependent, config:tracker.yaml, skip:jira,
// cred:<ПЕРЕМЕННАЯ|роль.user_env|роль.secret_env>, jira:reachability,
// skip:jira-checks, jira:account, jira:fields (весь GET /field упал),
// jira:field:<agent_owner|run_id|lease_until|attempts>, jira:link-type,
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
// (тот же выбор, что у internal/workspace.measure). incomplete — правда,
// если хоть один путь не прочитан: тогда total — нижняя оценка, а не точное
// число, и вызывающий обязан сказать об этом человеку, а не выдать заниженный
// размер за точный (fs.WalkDir просто прекращает спуск в непрочитанное
// поддерево, не помечая это само по себе).
func dirSize(dir string) (total int64, incomplete bool) {
	_ = filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			incomplete = true
			return nil //nolint:nilerr // недочитанный путь просто не считаем, но отмечаем
		}
		if entry.IsDir() {
			return nil
		}
		if info, err := entry.Info(); err == nil {
			total += info.Size()
		} else {
			incomplete = true
		}
		return nil
	})
	return total, incomplete
}

// checkStaleOfficeSnapshots перечисляет ${OFFICE_HOME}/office/<версия>/, кроме
// той, которую резолвит бегущий бинарник, и не трогает ни одну: удаление —
// решение человека, docs/notes/install.md, «runner doctor резолвит office
// ls/prune».
//
// Вызывается только когда runner.ResolveOffice уже прошёл без ошибки —
// её называет отдельная находка office:resolve в doctorCommand, а не эта
// функция: она раньше объединяла «резолв упал» и «это законный режим
// клона» в один и тот же ok, и настоящий отказ ResolveOffice (битый
// OFFICE_CONFIG_ROOT, раннер без личности) читался как «нечего проверять»
// вместо фатальной находки — ровно то, для чего doctor и существует, чтобы
// не пропустить.
//
// Сам список снапшотов — справочная находка (спецификация прямо требует не
// ронять доктора из-за него), поэтому даже ошибка чтения office/ здесь —
// warn, а не fail: office:resolve уже отделяет настоящую беду от этой.
func checkStaleOfficeSnapshots(o runner.Office) finding {
	if o.Source != runner.SourcePayload {
		// В режиме клона версий на диске не бывает вовсе — не беда, а
		// «вопрос не о клоне».
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
		return finding{"office:stale-snapshots", "warn", "список не прочитан: " + err.Error()}
	}
	var n int
	var total int64
	var incomplete bool
	for _, e := range entries {
		if !e.IsDir() || e.Name() == current {
			continue
		}
		n++
		sz, inc := dirSize(filepath.Join(officeDir, e.Name()))
		total += sz
		incomplete = incomplete || inc
	}
	if n == 0 {
		return finding{"office:stale-snapshots", "ok", "нет"}
	}
	sizeText := size(total)
	if incomplete {
		sizeText = "не менее " + sizeText + " (часть дерева не прочитана)"
	}
	return finding{"office:stale-snapshots", "warn",
		fmt.Sprintf("устаревших снапшотов: %d, суммарно %s — убрать руками: rm -r %s/<версия>", n, sizeText, officeDir)}
}

// checkCredentials — по одной находке на переменную окружения каждой учётки
// tracker.yaml (default и все roles), без повторов: учётка-дубликат
// (например, роль без своей пары UserEnv/SecretEnv, разделяющая общую)
// называется один раз, а не по разу на каждую роль — иначе один незаданный
// секрет выглядел бы как несколько разных бед.
//
// Переменная, заданная пустым значением (`export X=`), — не задана по сути:
// os.LookupEnv сказал бы "есть", хотя jira.OpenAs (internal/tracker/jira/jira.go)
// её значение потом трактует ровно как отсутствие. Роль с настроенным
// user_env, но без своего secret_env (или наоборот) — не тихий пропуск: сама
// возможность завести половину учётки существует (LoadConfig проверяет
// только accounts.default целиком, не accounts.roles.*), и OpenAs на такой
// роли ушёл бы за секретом в переменную с пустым именем.
func checkCredentials(a jira.Accounts) []finding {
	seen := map[jira.Account]bool{}
	var out []finding
	check := func(label string, acc jira.Account) {
		if seen[acc] {
			return
		}
		seen[acc] = true
		for _, field := range []struct{ key, env string }{
			{"user_env", acc.UserEnv},
			{"secret_env", acc.SecretEnv},
		} {
			if field.env == "" {
				out = append(out, finding{"cred:" + label + "." + field.key, "fail", field.key + " не настроен"})
				continue
			}
			if os.Getenv(field.env) != "" {
				out = append(out, finding{"cred:" + field.env, "ok", "задана"})
			} else {
				out = append(out, finding{"cred:" + field.env, "fail", "не задана"})
			}
		}
	}
	check("default", a.Default)
	for _, role := range slices.Sorted(maps.Keys(a.Roles)) {
		check(role, a.Roles[role])
	}
	return out
}

// jiraChecker — то немногое, что нужно доктору от *jira.Tracker: не весь
// tracker.Tracker (Claim/Comment/Transition и остальное были бы побочными
// эффектами, которые спецификация запрещает), а ровно четыре read-only
// метода. Узкий интерфейс — ради теста: подделке не нужно изображать
// HTTP-сервер, только эти четыре ответа.
type jiraChecker interface {
	tracker.WorkflowChecker
	CheckAccount() error
	CheckFields() ([]jira.FieldCheck, error)
	CheckLinkType() error
}

var _ jiraChecker = (*jira.Tracker)(nil)

func checkAccount(trk jiraChecker) finding {
	if err := trk.CheckAccount(); err != nil {
		return finding{"jira:account", "fail", err.Error()}
	}
	return finding{"jira:account", "ok", "учётка подтверждена"}
}

func checkFields(trk jiraChecker) []finding {
	checks, err := trk.CheckFields()
	if err != nil {
		return []finding{{"jira:fields", "fail", err.Error()}}
	}
	out := make([]finding, 0, len(checks))
	for _, c := range checks {
		id := "jira:field:" + c.Config
		switch {
		case !c.Present:
			out = append(out, finding{id, "fail",
				fmt.Sprintf("%s (fields.%s) не найдено на инстансе", c.ID, c.Config)})
		case c.ActualType != c.ExpectedType:
			out = append(out, finding{id, "fail",
				fmt.Sprintf("%s (fields.%s): тип %s, ожидался %s", c.ID, c.Config, c.ActualType, c.ExpectedType)})
		default:
			out = append(out, finding{id, "ok", fmt.Sprintf("%s: %s", c.ID, c.ActualType)})
		}
	}
	return out
}

func checkLinkType(trk jiraChecker, dependsOnLink string) finding {
	if dependsOnLink == "" {
		return finding{"jira:link-type", "ok", "depends_on_link не настроен, проверка пропущена"}
	}
	if err := trk.CheckLinkType(); err != nil {
		return finding{"jira:link-type", "fail", err.Error()}
	}
	return finding{"jira:link-type", "ok", "присутствует: " + dependsOnLink}
}

// loadWorkingStatuses читает workflow.yaml офиса (не ${OFFICE_HOME} — граф
// один на всех проектов, отсюда office.Root) и отдаёт рабочий статус каждой
// роли, у которой он есть. Сегодня это только implementer/"InProgress"
// (office/workflow.yaml), но doctor не должен знать имя роли — узнать об
// этом положено графу, не коду.
func loadWorkingStatuses(o runner.Office, resolveErr error) (map[string]string, error) {
	if resolveErr != nil {
		return nil, resolveErr
	}
	workflow, err := tracker.LoadWorkflow(filepath.Join(o.Root, tracker.WorkflowFile))
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, role := range workflow.Order() {
		flow, err := workflow.Role(role)
		if err != nil {
			continue // тот же граф уже прошёл validate() при загрузке — сюда не дойдёт
		}
		if flow.Working != "" {
			out[role] = flow.Working
		}
	}
	return out, nil
}

// checkWorkflow — обёртка над Tracker.CheckWorkflow для одной пары (проект,
// роль). В отличие от internal/pipeline, который зовёт её раз за процесс и
// только после захвата, доктор зовёт её сразу — задачи в рабочем статусе
// может не быть, и это не беда, а «нечего показать образцом» (ok, не fail).
func checkWorkflow(trk jiraChecker, project, role, workingStatus string) finding {
	id := fmt.Sprintf("jira:workflow:%s:%s", project, role)
	check, err := trk.CheckWorkflow(project, workingStatus)
	switch {
	case err != nil:
		return finding{id, "warn", "проверка workflow не выполнена: " + err.Error()}
	case check.Sample == "":
		return finding{id, "ok", "нет образца в статусе " + workingStatus}
	case check.SelfEntry:
		return finding{id, "warn", "workflow допускает двух владельцев: в " + workingStatus + " есть переход из него самого"}
	default:
		return finding{id, "ok", "единоличный переход в " + workingStatus}
	}
}

// doctorCommand — точка входа подкоманды. Флагов кроме --backend нет: ни
// --role (доктор не привязан к роли), ни --json (спецификация требует
// простого текста).
func doctorCommand(args []string, out io.Writer) error {
	fset := flags("doctor")
	backend := fset.String("backend", runagent.DefaultBackend, "бэкенд агента: sbx или local — какие проверки бэкенда включать")
	if err := fset.Parse(args); err != nil {
		return err
	}
	// Опечатка в --backend не должна тихо читаться как local: та же
	// проверка, что держит newOffices (cmd/runner/office.go) под всеми
	// остальными подкомандами, — не своя, чтобы имена бэкендов не разошлись
	// в двух местах. Сама Sandboxes здесь не нужна, только её ошибка.
	if _, err := runagent.SandboxesOf(*backend); err != nil {
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
	// побочных эффектов. office/officeErr используются здесь и ниже — для
	// stale-snapshots, go на клоне и (при наличии jira-проектов) workflow.yaml.
	//
	// Отказ резолва — фатальная находка сама по себе, а не повод молчать:
	// `runner version` на той же самой машине с тем же самым отказом (битый
	// OFFICE_CONFIG_ROOT, раннер без личности) отказывает и выходит 2 —
	// doctor обязан вести себя так же, а не читать это как «нечего
	// проверять». officeErr при этом не бросает doctor'а: ниже он ещё
	// пригодится skip:workflow, если найдутся jira-проекты.
	office, officeErr := runner.ResolveOffice(runner.Resolve{})
	if officeErr != nil {
		report(finding{"office:resolve", "fail", officeErr.Error()})
	} else {
		report(checkStaleOfficeSnapshots(office))
		if office.Source == runner.SourceClone {
			// Клон собирает ограждение через `go build` (internal/runner/validator.go)
			// при каждом реальном прогоне — bin/*-обёртки именно этот режим и
			// используют. Отсутствующий go на PATH сегодня всплыл бы только на
			// первом прогоне, не раньше.
			report(checkTool("go"))
		}
	}

	// Stage 2 — projects.local.yaml. Отказ здесь не крашит доктора и не
	// молчит о непроверенном: он называет файл и говорит, что зависящие от
	// него проверки пропущены — ровно то, что видит человек сразу после
	// runner init, пока не переименовал образец.
	home, err := runner.Home()
	if err != nil {
		report(finding{"config:home", "fail", err.Error()})
		report(finding{"skip:project-dependent", "warn",
			"tool(comet)/cred/JIRA-проверки пропущены: OFFICE_HOME не определён"})
		return concludeExit(out, findings)
	}
	projects, err := tracker.LoadProjects(filepath.Join(home, tracker.ProjectsLocalFile))
	if err != nil {
		report(finding{"config:projects.local.yaml", "fail", err.Error()})
		report(finding{"skip:project-dependent", "warn",
			"tool(comet)/cred/JIRA-проверки пропущены: projects.local.yaml не загрузился"})
		return concludeExit(out, findings)
	}

	// comet нужен любому проекту, дошедшему до PR-прохода, не только тем, у
	// кого настроен forge: internal/pipeline/prpass.go зовёт archiveIfReady
	// (internal/pipeline/archive.go, runComet) что для forge=="" ветки
	// (сразу перед prSkipped), что для forge!="" — открытие PR идёт уже
	// после архивирования. Проверка была раньше привязана к forge и не
	// ловила самый частый случай в этом самом репозитории (оба полигона —
	// без forge): отсутствующий comet там молча вырождается в «архивирование
	// пропущено» (internal/pipeline/archive.go), а не в отказ.
	//
	// Безусловно, не под "если проектов больше нуля": LoadProjects выше уже
	// отказал бы на пустом projects.local.yaml («не называет ни одного
	// проекта») раньше, чем выполнение дошло бы сюда.
	report(checkTool("comet"))

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

	// Тип — jiraChecker, не *jira.Tracker: гарантия «доктор не мутирует
	// трекер» тогда покрывает и это тело функции, а не только четыре
	// функции-обёртки ниже, которым он передаётся аргументом.
	var trk jiraChecker
	trk, err = jira.Open(cfg)
	if err != nil {
		report(finding{"jira:reachability", "fail", err.Error()})
		// jira.Open — чтение tracker.yaml (base_url, auth.mode, статус-карта,
		// секрет в окружении), не сетевой запрос: настоящая недоступность
		// инстанса всплывёт как jira:account ниже. Здесь она не может, а
		// падение сюда роняет account/fields/link-type/workflow разом — та
		// же изоляция, что у config:tracker.yaml и config:projects.local.yaml
		// выше: назвать, что пропущено, а не промолчать.
		report(finding{"skip:jira-checks", "warn",
			"jira:account/fields/link-type/workflow пропущены: трекер не открыт"})
		return concludeExit(out, findings)
	}
	report(checkAccount(trk))
	for _, f := range checkFields(trk) {
		report(f)
	}
	report(checkLinkType(trk, cfg.DependsOnLink))

	workingStatuses, workflowErr := loadWorkingStatuses(office, officeErr)
	for _, key := range jiraProjects.Keys() {
		if workflowErr != nil {
			report(finding{"skip:workflow:" + key, "warn", "проверка workflow пропущена: " + workflowErr.Error()})
			continue
		}
		for _, role := range slices.Sorted(maps.Keys(workingStatuses)) {
			report(checkWorkflow(trk, key, role, workingStatuses[role]))
		}
	}

	return concludeExit(out, findings)
}

// concludeExit печатает каждую находку в порядке появления и отказывает,
// если среди них есть что-то кроме ok/warn: warn ни на что не влияет, ok —
// тоже, а fail (или любой прочий уровень) — отказ.
func concludeExit(out io.Writer, findings []finding) error {
	var failed int
	for _, f := range findings {
		fmt.Fprintf(out, "%-4s %-28s %s\n", f.level, f.check, f.msg)
		// Fail-closed: только "ok" и "warn" — опознанные неопасные уровни;
		// всё прочее (опечатка в литерале level, будущий четвёртый уровень
		// без обновления этой проверки) обязано считаться отказом, а не
		// молча сходить за успех — доктор существует именно для того, чтобы
		// не лгать про статус "всё в порядке".
		if f.level != "ok" && f.level != "warn" {
			failed++
		}
	}
	if failed == 0 {
		return nil
	}
	return fmt.Errorf("doctor: %d check(s) failed", failed)
}
