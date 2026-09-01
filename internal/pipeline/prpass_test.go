package pipeline

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kao73/virtual-office/internal/forge"
	"github.com/kao73/virtual-office/internal/runner"
	"github.com/kao73/virtual-office/internal/tracker"
)

// gitEnv — личность коммитов тестового агента, как её задаёт gitIn.
func gitEnv() []string {
	return append(os.Environ(),
		"GIT_AUTHOR_NAME=агент", "GIT_AUTHOR_EMAIL=agent@office.local",
		"GIT_COMMITTER_NAME=агент", "GIT_COMMITTER_EMAIL=agent@office.local")
}

// fakeForge — forge без сети. Настоящая реализация подключается тем же
// интерфейсом, поэтому PR-проход проверяется целиком, без GitHub.
type fakeForge struct {
	url      string      // адрес, который вернёт OpenPR
	state    forge.State // что ответить про открытый PR
	openErr  error
	stateErr error

	opened []openedPR
	asked  []string
}

type openedPR struct {
	project, branch, base, title, body string
}

func (f *fakeForge) OpenPR(project, branch, base, title, body string) (string, error) {
	f.opened = append(f.opened, openedPR{project, branch, base, title, body})
	if f.openErr != nil {
		return "", f.openErr
	}
	return f.url, nil
}

func (f *fakeForge) PRState(url string) (forge.State, error) {
	f.asked = append(f.asked, url)
	return f.state, f.stateErr
}

// withForge подключает офису поддельный forge и объявляет его проекту.
func (o *office) withForge(f *fakeForge) *fakeForge {
	project := o.Projects["OFF"]
	project.Forge = forge.Kind
	o.Projects["OFF"] = project
	o.Forges = map[string]forge.Forge{forge.Kind: f}
	return f
}

// approved доводит задачу до очереди PR-прохода тем же путём, что и жизнь:
// автор делает работу, ревьюер её принимает.
func (o *office) approved(t *testing.T, key string) tracker.Task {
	t.Helper()
	o.agent.byRole = map[string]runner.Result{
		"implementer": {Outcome: runner.OutcomeDone, Summary: "Сделал.", NextOwner: "reviewer"},
		"reviewer":    {Outcome: runner.OutcomeDone, Summary: "Принято, замечаний нет.", NextOwner: "human"},
	}
	o.tickRoleOnce(t, "implementer")
	o.tickRoleOnce(t, "reviewer")

	task := o.get(t, key)
	if task.Status != "Approved" {
		t.Fatalf("%s: статус %q, ожидался Approved", key, task.Status)
	}
	return task
}

func (o *office) pass(t *testing.T) {
	t.Helper()
	if err := o.PRPass(context.Background()); err != nil {
		t.Fatalf("системный проход не прошёл: %v", err)
	}
}

// pushDefault кладёт коммит в ветку по умолчанию origin — так ветка проекта
// уезжает вперёд, пока задача ждёт слияния.
func pushDefault(t *testing.T, origin, name, body string) {
	t.Helper()
	work := t.TempDir()
	gitIn(work, "init", "-q", "-b", "master")
	gitIn(work, "remote", "add", "origin", origin)
	gitIn(work, "fetch", "-q", "origin", "master")
	gitIn(work, "checkout", "-q", "-B", "master", "origin/master")
	if err := os.WriteFile(filepath.Join(work, name), []byte(body), 0o644); err != nil {
		t.Fatalf("файл не записан: %v", err)
	}
	gitIn(work, "add", name)
	gitIn(work, "commit", "-q", "-m", "правка в ветке по умолчанию")
	gitIn(work, "push", "-q", "origin", "master")
}

// writes — агент, который правит файл, а не делает пустой коммит: конфликт
// на пустых коммитах не воспроизвести.
func writes(name, body string) func(Request) {
	return func(req Request) {
		path := filepath.Join(req.Workdir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			panic(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			panic(err)
		}
		gitIn(req.Workdir, "add", name)
	}
}

// Задача, дошедшая до конца разбора, получает pull request — и остаётся
// на месте: сливает человек, а офис ждёт его решения.
func TestPRPassOpensPullRequest(t *testing.T) {
	o := newOffice(t)
	f := o.withForge(&fakeForge{url: "https://github.test/kao73/client/pull/7", state: forge.Open})
	o.agent.commit = "работа автора"
	o.approved(t, "OFF-1")

	o.pass(t)

	if len(f.opened) != 1 {
		t.Fatalf("открыто pull request: %d, ожидался один", len(f.opened))
	}
	got := f.opened[0]
	if got.project != "OFF" || got.branch != "agent/OFF-1" || got.base != "master" {
		t.Errorf("pull request открыт не туда: %+v", got)
	}
	task := o.get(t, "OFF-1")
	if task.Status != "Approved" {
		t.Errorf("статус %q: задача ждёт человека там же, где ждала", task.Status)
	}
	body := lastComment(t, task).Body
	if !strings.Contains(body, f.url) {
		t.Errorf("адрес pull request не попал в тикет:\n%s", body)
	}
	if m, ok := tracker.MarkerOf(body); !ok || m.Event != tracker.EventPROpened || m.Role != "office" {
		t.Errorf("запись об открытии не помечена как надо: %s", body)
	}
}

// Тело pull request собирается из постановки в ветке и последнего отчёта
// в переписке: первое лежит в git, второго там нет вовсе.
func TestPRPassBodyCarriesBriefAndReport(t *testing.T) {
	o := newOffice(t)
	f := o.withForge(&fakeForge{url: "https://github.test/kao73/client/pull/1", state: forge.Open})
	o.agent.work = writes(filepath.Join(runner.ChangeDirRel("OFF-1"), runner.FileBrief),
		"Цель: считать среднее.\n")
	o.agent.commit = "работа автора"
	o.approved(t, "OFF-1")

	o.pass(t)

	if len(f.opened) != 1 {
		t.Fatalf("открыто pull request: %d", len(f.opened))
	}
	got := f.opened[0]
	if !strings.Contains(got.title, "OFF-1") {
		t.Errorf("в заголовке нет ключа задачи: %q", got.title)
	}
	if !strings.Contains(got.body, "Цель: считать среднее") {
		t.Errorf("постановка не попала в тело:\n%s", got.body)
	}
	if !strings.Contains(got.body, "Принято, замечаний нет") {
		t.Errorf("отчёт разбора не попал в тело:\n%s", got.body)
	}
	// Маркер — разметка для раннера; pull request читают люди. Поймано живой
	// проверкой: в первом настоящем PR первой строкой разбора стояло
	// `[office run:… role:reviewer …]`.
	if strings.Contains(got.body, tracker.Prefix) {
		t.Errorf("маркер отчёта попал в тело pull request:\n%s", got.body)
	}
}

// Задача, пришедшая мимо аналитика, каталога изменения не имеет: тело берётся
// из тикета.
func TestPRPassBodyFallsBackToTicket(t *testing.T) {
	o := newOffice(t)
	f := o.withForge(&fakeForge{url: "https://github.test/kao73/client/pull/2", state: forge.Open})
	o.agent.commit = "работа автора"
	o.approved(t, "OFF-1")

	o.pass(t)

	if len(f.opened) != 1 {
		t.Fatalf("открыто pull request: %d", len(f.opened))
	}
	if !strings.Contains(f.opened[0].body, "Сделать что-нибудь полезное") {
		t.Errorf("описание тикета не попало в тело:\n%s", f.opened[0].body)
	}
	if !strings.Contains(f.opened[0].body, "Задача OFF-1") {
		t.Errorf("тема тикета не попала в тело:\n%s", f.opened[0].body)
	}
}

// Постановка Comet Native — новый, предпочтительный источник тела pull
// request; старый docs/changes/<KEY> не должен побеждать его, если оба есть.
func TestPRPassBodyPrefersCometChangeOverLegacy(t *testing.T) {
	o := newOffice(t)
	f := o.withForge(&fakeForge{url: "https://github.test/kao73/client/pull/3", state: forge.Open})
	o.agent.work = func(req Request) {
		writes(filepath.Join(runner.ChangeDirRel("OFF-1"), runner.FileBrief),
			"Старая постановка.\n")(req)
		writes(filepath.Join(runner.CometChangeDirRel("OFF-1"), runner.FileBrief),
			"Цель: считать среднее через Comet.\n")(req)
	}
	o.agent.commit = "работа автора"
	o.approved(t, "OFF-1")

	o.pass(t)

	got := f.opened[0].body
	if !strings.Contains(got, "Цель: считать среднее через Comet") {
		t.Errorf("постановка Comet Native не попала в тело:\n%s", got)
	}
	if strings.Contains(got, "Старая постановка") {
		t.Errorf("старая постановка не должна побеждать новую:\n%s", got)
	}
}

// Независимое ревью: имя изменения, угаданное по task-key, может разойтись
// с тем, что аналитик реально выбрал (живой случай: задача demo-3, изменение
// stats-median) — prBody обязана резолвить имя через .comet/current-change.json
// первым делом, тем же способом, что и archiveIfReady в archive.go, а не
// только угадыванием.
func TestPRPassBodyResolvesNameFromCurrentChangeFile(t *testing.T) {
	o := newOffice(t)
	f := o.withForge(&fakeForge{url: "https://github.test/kao73/client/pull/21", state: forge.Open})
	o.agent.work = func(req Request) {
		writes(runner.CometCurrentChangeFile, `{"change":"stats-median"}`+"\n")(req)
		writes(filepath.Join(runner.CometChangeDirForName("stats-median"), runner.FileBrief),
			"Цель: имя изменения расходится с угаданным по task-key.\n")(req)
	}
	o.agent.commit = "работа автора"
	o.approved(t, "OFF-1")

	o.pass(t)

	got := f.opened[0].body
	if !strings.Contains(got, "Цель: имя изменения расходится с угаданным по task-key") {
		t.Errorf("brief.md по настоящему имени изменения не найден — prBody осталась на угаданном off-1:\n%s", got)
	}
}

// Задача, которую analyst вёл до перехода на Comet Native, хранит постановку
// в старом корне — prBody не должен молча забыть про неё только потому, что
// нового корня нет.
func TestPRPassBodyFallsBackToLegacyChangeDir(t *testing.T) {
	o := newOffice(t)
	f := o.withForge(&fakeForge{url: "https://github.test/kao73/client/pull/4", state: forge.Open})
	o.agent.work = writes(filepath.Join(runner.ChangeDirRel("OFF-1"), runner.FileBrief),
		"Цель: старая постановка ещё жива.\n")
	o.agent.commit = "работа автора"
	o.approved(t, "OFF-1")

	o.pass(t)

	if !strings.Contains(f.opened[0].body, "Цель: старая постановка ещё жива") {
		t.Errorf("старая постановка не подхвачена как fallback:\n%s", f.opened[0].body)
	}
}

// fakeCometArchivesForReal подкладывает подложный `comet`, чей "native
// archive ... --confirmed" по-настоящему переименовывает каталог изменения
// (mv, а не только mkdir каталога назначения, как в archive_test.go's
// fakeComet) — так, что archiveSucceeded находит источник действительно
// исчезнувшим и archiveIfReady доходит до настоящего коммита и push.
// Нужен отдельно от fakeComet: там источник остаётся на месте нарочно,
// и archiveSucceeded в этом тесте отказал бы, так и не дойдя до push,
// а этому тесту важно проверить именно то, что происходит на ветке после
// push.
func fakeCometArchivesForReal(t *testing.T, name string) {
	t.Helper()
	binDir := t.TempDir()
	script := "#!/bin/sh\n" +
		"case \"$2\" in\n" +
		"  status) echo '{\"command\":\"status\",\"exitCode\":0,\"data\":{\"phase\":\"archive\",\"loop\":{\"stage\":\"" + archiveReadyStage + "\"}}}' ;;\n" +
		"  archive) mkdir -p " + cometArchiveScope + "/archive && mv " + cometArchiveScope + "/changes/" + name + " " + cometArchiveScope + "/archive/0000-00-00-" + name + " ;;\n" +
		"  *) exit 1 ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(binDir, "comet"), []byte(script), 0o755); err != nil {
		t.Fatalf("подложный comet не записан: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// Независимое ревью (Критично #1): archiveIfReady переименовывает и пушит
// docs/comet/changes/<name> в docs/comet/archive/<date>-<name> — до правки
// это происходило РАНЬШЕ prBody, и prBody, читая brief.md по старому пути
// на уже обновлённом HEAD, промахивалась мимо него и молча подставляла
// сырой текст тикета вместо постановки — ровно для тех задач, что дошли до
// архивирования в один проход. Этот тест воспроизводит именно такую задачу:
// изменение уже в archive-ready, и pull request всё равно обязан получить
// содержимое brief.md, а не текст тикета.
func TestPRPassBodyCarriesBriefEvenWhenArchivedInSamePass(t *testing.T) {
	o := newOffice(t)
	f := o.withForge(&fakeForge{url: "https://github.test/kao73/client/pull/20", state: forge.Open})
	name := runner.CometChangeName("OFF-1")
	fakeCometArchivesForReal(t, name)
	o.agent.work = writes(filepath.Join(runner.CometChangeDirRel("OFF-1"), runner.FileBrief),
		"Цель: архивная постановка обязана дойти до pull request.\n")
	o.agent.commit = "работа автора"
	o.approved(t, "OFF-1")

	o.pass(t)

	if len(f.opened) != 1 {
		t.Fatalf("открыто pull request: %d", len(f.opened))
	}
	got := f.opened[0].body
	if !strings.Contains(got, "Цель: архивная постановка обязана дойти до pull request") {
		t.Errorf("brief.md не попал в тело — архивирование опередило чтение постановки:\n%s", got)
	}
	if strings.Contains(got, "Сделать что-нибудь полезное") {
		t.Errorf("тело откатилось на сырой текст тикета вместо brief.md:\n%s", got)
	}
}

// Независимое ревью (Важно #2): архивирование — свойство изменения, а не
// forge — но archiveIfReady стояло после ветки "project.Forge == ''" в
// switch, и на проекте без forge (оба текущих полигона живут именно так)
// Archive был недостижим целиком. Тест воспроизводит проект без forge
// (дефолт newOffice, o.withForge здесь нарочно не зовётся) и проверяет,
// что архивный коммит всё равно уезжает в ветку.
func TestOpenPRArchivesEvenWithoutForge(t *testing.T) {
	o := newOffice(t)
	name := runner.CometChangeName("OFF-1")
	fakeCometArchivesForReal(t, name)
	o.agent.work = writes(filepath.Join(runner.CometChangeDirRel("OFF-1"), runner.FileBrief),
		"Цель: архивировать даже без forge.\n")
	o.agent.commit = "работа автора"
	task := o.approved(t, "OFF-1")
	project := o.Projects["OFF"]
	if project.Forge != "" {
		t.Fatalf("подготовка теста неверна: у проекта уже есть forge %q", project.Forge)
	}

	if err := o.openPR(task); err != nil {
		t.Fatalf("openPR: %v", err)
	}

	ws, err := o.Workspaces.Ensure(task.Ref(), project)
	if err != nil {
		t.Fatalf("рабочая папка не открыта для проверки: %v", err)
	}
	defer ws.Unlock()

	subject, err := exec.Command("git", "-C", ws.Dir, "log", "-1", "--format=%s").Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	wantSubject := "chore: archive Comet Native change " + name
	if got := strings.TrimSpace(string(subject)); got != wantSubject {
		t.Errorf("архивный коммит не найден на проекте без forge: получено %q, ожидалось %q", got, wantSubject)
	}
}

// Независимое ревью (второй проход, после фикса #1): фикс однопроходного
// случая не закрывал повторный проход — если архивирование состоялось в
// одном проходе, а pull request не открылся (сеть, forge, отказ человека)
// и задача вернулась в очередь, следующий проход промахивался мимо обоих
// корней brief.md (новый переименован, старого никогда не было) и снова
// откатывался на сырой текст тикета. Этот тест воспроизводит именно
// повторный проход: первый архивирует, но не открывает PR (сеть), второй
// обязан найти brief уже в docs/comet/archive/**.
func TestPRPassBodyFindsBriefInArchiveOnRetriedPass(t *testing.T) {
	o := newOffice(t)
	f := o.withForge(&fakeForge{
		url: "https://github.test/kao73/client/pull/22", state: forge.Open,
		openErr: errors.New("сеть недоступна"),
	})
	name := runner.CometChangeName("OFF-1")
	fakeCometArchivesForReal(t, name)
	o.agent.work = writes(filepath.Join(runner.CometChangeDirRel("OFF-1"), runner.FileBrief),
		"Цель: найти brief в архиве на повторном проходе.\n")
	o.agent.commit = "работа автора"
	task := o.approved(t, "OFF-1")

	if err := o.openPR(task); err != nil {
		t.Fatalf("openPR (первый проход, сеть недоступна): %v", err)
	}
	if len(f.opened) != 1 {
		t.Fatalf("попыток открыть после первого прохода: %d, ожидалась одна (неудачная)", len(f.opened))
	}

	f.openErr = nil
	if err := o.openPR(task); err != nil {
		t.Fatalf("openPR (второй проход): %v", err)
	}
	if len(f.opened) != 2 {
		t.Fatalf("попыток открыть: %d, ожидалось две", len(f.opened))
	}
	got := f.opened[1].body
	if !strings.Contains(got, "Цель: найти brief в архиве на повторном проходе") {
		t.Errorf("brief не найден в docs/comet/archive на повторном проходе:\n%s", got)
	}
}

// Независимое ревью (раунд 2): суффиксное совпадение (strings.HasSuffix,
// первая версия фикса) находило бы и ЧУЖОЙ архивный каталог, чьё имя
// случайно оканчивается тем же хвостом — "median" совпал бы и с
// "2026-08-30-stats-median". Полный якорь по дате обязан отвергнуть такой
// декой: у него нет собственного архива вовсе, и тело обязано откатиться
// на текст тикета, а не подхватить чужую постановку.
func TestPRPassBodyArchiveLookupRejectsSuffixCollision(t *testing.T) {
	o := newOffice(t)
	f := o.withForge(&fakeForge{url: "https://github.test/kao73/client/pull/23", state: forge.Open})
	o.agent.work = func(req Request) {
		writes(runner.CometCurrentChangeFile, `{"change":"median"}`+"\n")(req)
		writes(filepath.Join(runner.CometChangeDirForName("stats-median"), runner.FileBrief),
			"Цель: чужая постановка stats-median — подхватывать её нельзя.\n")(req)
	}
	o.agent.commit = "работа автора"
	// "median" (наше изменение) в docs/comet/changes не заводим вовсе —
	// его нет ни в новом, ни в старом корне, ни в архиве: только decoy
	// "stats-median" рядом, чьё имя оканчивается тем же хвостом "-median".
	// Переносим "stats-median" прямо в архив, минуя настоящее архивирование —
	// нужен только сам факт совпадения суффикса, а не то, как оно туда попало.
	o.agent.act = nil
	task := o.approved(t, "OFF-1")

	ws, err := o.Workspaces.Ensure(task.Ref(), o.Projects["OFF"])
	if err != nil {
		t.Fatalf("рабочая папка не открыта: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(ws.Dir, cometArchiveScope, "archive"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(
		filepath.Join(ws.Dir, runner.CometChangeDirForName("stats-median")),
		filepath.Join(ws.Dir, cometArchiveScope, "archive", "2026-08-30-stats-median"),
	); err != nil {
		t.Fatalf("decoy не перемещён в архив: %v", err)
	}
	gitIn(ws.Dir, "add", "-A")
	gitIn(ws.Dir, "commit", "-q", "-m", "имитация архивирования decoy")
	if _, err := o.Workspaces.Push(ws); err != nil {
		t.Fatalf("ветка не опубликована: %v", err)
	}
	if err := ws.Unlock(); err != nil {
		t.Fatal(err)
	}

	o.pass(t)

	if len(f.opened) != 1 {
		t.Fatalf("открыто pull request: %d", len(f.opened))
	}
	got := f.opened[0].body
	if strings.Contains(got, "чужая постановка stats-median") {
		t.Errorf("подхвачен чужой архивный каталог по совпадению суффикса:\n%s", got)
	}
	if !strings.Contains(got, "Сделать что-нибудь полезное") {
		t.Errorf("тело не откатилось на текст тикета там, где своего архива нет:\n%s", got)
	}
}

// Запись об открытии без адреса второго pull request не порождает: комментарий
// могли поправить руками, и молча удвоить PR офис не вправе.
func TestPRPassKeepsSilenceOnRecordWithoutURL(t *testing.T) {
	o := newOffice(t)
	f := o.withForge(&fakeForge{url: "https://github.test/kao73/client/pull/14", state: forge.Open})
	o.agent.commit = "работа автора"
	task := o.approved(t, "OFF-1")

	marker := tracker.Marker{RunID: "порченый", Role: "office", Event: tracker.EventPROpened, ConfigSHA: "5bc6a3b0"}
	if err := o.tasks.Comment(task.Key, tracker.BySystem(),
		tracker.NoticeBody(marker, "Pull request открыт: адрес потерялся при правке.")); err != nil {
		t.Fatalf("запись не добавлена: %v", err)
	}

	o.pass(t)

	if len(f.opened) != 0 {
		t.Errorf("открыт второй pull request: %+v", f.opened)
	}
	if got := o.get(t, "OFF-1").Status; got != "Approved" {
		t.Errorf("статус %q: задача остаётся на месте", got)
	}
}

// Ветка без собственных коммитов — аномалия, а не провал прогона: сливать
// нечего, и разговаривать об этом надо с человеком.
func TestPRPassEmptyBranchAsksHuman(t *testing.T) {
	o := newOffice(t)
	f := o.withForge(&fakeForge{url: "https://github.test/kao73/client/pull/3"})
	// Агент ничего не коммитит: ветка останется без собственных коммитов.
	o.approved(t, "OFF-1")

	o.pass(t)

	if len(f.opened) != 0 {
		t.Errorf("pull request открыт из ветки без коммитов: %+v", f.opened)
	}
	task := o.get(t, "OFF-1")
	if task.Status != "Blocked" {
		t.Errorf("статус %q, ожидался Blocked", task.Status)
	}
	if !task.HumanFlag {
		t.Error("задача не помечена ожиданием человека")
	}
	if !tracker.HasEvent(task.Comments, tracker.EventPRClosed) {
		t.Error("в переписке нет записи о несостоявшемся pull request")
	}
	if task.Attempts != 0 {
		t.Errorf("счётчик попыток %d: прогон не провалился", task.Attempts)
	}
}

// Слитый pull request заканчивает жизнь задачи.
func TestPRPassMergedFinishesTask(t *testing.T) {
	o := newOffice(t)
	f := o.withForge(&fakeForge{url: "https://github.test/kao73/client/pull/4", state: forge.Open})
	o.agent.commit = "работа автора"
	o.approved(t, "OFF-1")
	o.pass(t) // PR открыт

	f.state = forge.Merged
	o.pass(t)

	task := o.get(t, "OFF-1")
	if task.Status != "Done" {
		t.Fatalf("статус %q, ожидался Done", task.Status)
	}
	if !tracker.HasEvent(task.Comments, tracker.EventMerged) {
		t.Error("в переписке нет записи о слиянии")
	}
	if task.HumanFlag {
		t.Error("слитая работа зовёт человека")
	}
}

// Закрытый без слияния pull request — разговор с человеком, а не конец задачи.
func TestPRPassClosedAsksHuman(t *testing.T) {
	o := newOffice(t)
	f := o.withForge(&fakeForge{url: "https://github.test/kao73/client/pull/5", state: forge.Open})
	o.agent.commit = "работа автора"
	o.approved(t, "OFF-1")
	o.pass(t)

	f.state = forge.Closed
	o.pass(t)

	task := o.get(t, "OFF-1")
	if task.Status != "Blocked" {
		t.Fatalf("статус %q, ожидался Blocked", task.Status)
	}
	if !task.HumanFlag {
		t.Error("задача не помечена ожиданием человека")
	}
	if !tracker.HasEvent(task.Comments, tracker.EventPRClosed) {
		t.Error("в переписке нет записи о закрытии")
	}
}

// Ответ человека возвращает задачу в очередь прохода, и pull request
// открывается заново. Судить по факту `pr-opened` в истории значило бы
// не открыть второй никогда.
func TestPRPassReopensAfterHumanReply(t *testing.T) {
	o := newOffice(t)
	f := o.withForge(&fakeForge{url: "https://github.test/kao73/client/pull/6", state: forge.Open})
	o.agent.commit = "работа автора"
	o.approved(t, "OFF-1")
	o.pass(t)

	f.state = forge.Closed
	o.pass(t)
	if task := o.get(t, "OFF-1"); task.Status != "Blocked" {
		t.Fatalf("статус %q, ожидался Blocked", task.Status)
	}

	if err := o.tasks.AddComment("OFF-1", "human", "Открывай заново, PR закрыли по ошибке."); err != nil {
		t.Fatalf("ответ человека не записан: %v", err)
	}
	if _, err := o.HumanReplies(context.Background()); err != nil {
		t.Fatalf("ответы человека не разобраны: %v", err)
	}
	if task := o.get(t, "OFF-1"); task.Status != "Approved" {
		t.Fatalf("после ответа статус %q, ожидался Approved", task.Status)
	}

	f.state = forge.Open
	o.pass(t)
	if len(f.opened) != 2 {
		t.Fatalf("открыто pull request: %d, ожидалось два", len(f.opened))
	}
}

// Конфликт — это работа, а не провал: задача возвращается разработчику,
// попытка не тратится, pull request не открывается.
func TestPRPassConflictReturnsWork(t *testing.T) {
	o := newOffice(t)
	f := o.withForge(&fakeForge{url: "https://github.test/kao73/client/pull/8", state: forge.Open})
	o.agent.work = writes("shared.txt", "строка ветки\n")
	o.agent.commit = "работа автора"
	o.approved(t, "OFF-1")
	pushDefault(t, o.origin, "shared.txt", "строка ветки по умолчанию\n")

	o.pass(t)

	if len(f.opened) != 0 {
		t.Errorf("pull request открыт при конфликте: %+v", f.opened)
	}
	task := o.get(t, "OFF-1")
	if task.Status != "Ready" {
		t.Fatalf("статус %q, ожидался Ready", task.Status)
	}
	if task.Attempts != 0 {
		t.Errorf("счётчик попыток %d: конфликт не провал", task.Attempts)
	}
	if !tracker.HasEvent(task.Comments, tracker.EventMergeConflict) {
		t.Error("в переписке нет записи о конфликте")
	}
	// Про открытый pull request записи говорить нечего: его не открывали.
	// Поймано живой проверкой на GitHub.
	if body := lastComment(t, task).Body; strings.Contains(body, "остаётся открытым") {
		t.Errorf("запись обещает pull request, которого нет:\n%s", body)
	}
}

// Конфликт у уже открытого pull request его не закрывает: задача уходит
// на доработку, и вернувшись, подхватывает тот же PR.
func TestPRPassReusesPullRequestAfterConflict(t *testing.T) {
	o := newOffice(t)
	f := o.withForge(&fakeForge{url: "https://github.test/kao73/client/pull/9", state: forge.Open})
	o.agent.work = writes("shared.txt", "строка ветки\n")
	o.agent.commit = "работа автора"
	o.approved(t, "OFF-1")
	o.pass(t)
	if len(f.opened) != 1 {
		t.Fatalf("pull request не открыт: %+v", f.opened)
	}

	pushDefault(t, o.origin, "shared.txt", "строка ветки по умолчанию\n")
	o.pass(t)
	task := o.get(t, "OFF-1")
	if task.Status != "Ready" {
		t.Fatalf("статус %q, ожидался Ready", task.Status)
	}
	// А здесь pull request есть, и запись обязана назвать именно его: человеку,
	// читающему тикет, важно, что второго не будет.
	if body := lastComment(t, task).Body; !strings.Contains(body, f.url) {
		t.Errorf("запись о конфликте не назвала открытый pull request:\n%s", body)
	}

	// Разработчик слил ветку по умолчанию и разрешил конфликт, ревьюер принял.
	o.agent.act = func(req Request, _ int) runner.Result {
		if req.Role.Name != "implementer" {
			return runner.Result{Outcome: runner.OutcomeDone, Summary: "Принято.", NextOwner: "human"}
		}
		// Слияние конфликтует — на то оно и конфликт: git выйдет с ошибкой,
		// и разрешает её роль, а не обвязка.
		merge := exec.Command("git", "-C", req.Workdir, "merge", "--no-edit", "origin/master")
		merge.Env = gitEnv()
		_ = merge.Run()
		writes("shared.txt", "строка ветки\nстрока ветки по умолчанию\n")(req)
		gitIn(req.Workdir, "commit", "-q", "-m", "конфликт разрешён")
		return runner.Result{Outcome: runner.OutcomeDone, Summary: "Слил.", NextOwner: "reviewer"}
	}
	o.tickRoleOnce(t, "implementer")
	o.tickRoleOnce(t, "reviewer")
	if task := o.get(t, "OFF-1"); task.Status != "Approved" {
		t.Fatalf("после доработки статус %q, ожидался Approved", task.Status)
	}

	o.pass(t)
	if len(f.opened) != 1 {
		t.Errorf("открыт второй pull request: %+v", f.opened)
	}
}

// Повторный проход по той же задаче не открывает второго pull request
// и не пишет дублей.
func TestPRPassIsIdempotent(t *testing.T) {
	o := newOffice(t)
	f := o.withForge(&fakeForge{url: "https://github.test/kao73/client/pull/10", state: forge.Open})
	o.agent.commit = "работа автора"
	o.approved(t, "OFF-1")

	o.pass(t)
	before := len(o.get(t, "OFF-1").Comments)
	o.pass(t)

	if len(f.opened) != 1 {
		t.Errorf("открыто pull request: %d, ожидался один", len(f.opened))
	}
	if got := len(o.get(t, "OFF-1").Comments); got != before {
		t.Errorf("второй проход дописал %d записей", got-before)
	}
}

// Сбой связи — беда обвязки, а не ответ о задаче: задача остаётся на месте,
// и следующий проход попробует снова.
func TestPRPassKeepsTaskWhenForgeUnreachable(t *testing.T) {
	o := newOffice(t)
	f := o.withForge(&fakeForge{openErr: errors.New("сеть недоступна")})
	o.agent.commit = "работа автора"
	o.approved(t, "OFF-1")

	o.pass(t)

	if task := o.get(t, "OFF-1"); task.Status != "Approved" {
		t.Errorf("статус %q: сбой связи задачу не двигает", task.Status)
	}
	f.openErr = nil
	f.url = "https://github.test/kao73/client/pull/11"
	o.pass(t)
	if task := o.get(t, "OFF-1"); task.Status != "Approved" {
		t.Errorf("статус %q, ожидался Approved", task.Status)
	}
	if len(f.opened) != 2 {
		t.Errorf("попыток открыть: %d, ожидалось две", len(f.opened))
	}
}

// Отказ forge — окончательный ответ о задаче, и разговаривать о нём надо
// с человеком.
func TestPRPassRefusalAsksHuman(t *testing.T) {
	o := newOffice(t)
	o.withForge(&fakeForge{openErr: forge.ErrRefused})
	o.agent.commit = "работа автора"
	o.approved(t, "OFF-1")

	o.pass(t)

	task := o.get(t, "OFF-1")
	if task.Status != "Blocked" || !task.HumanFlag {
		t.Errorf("статус %q, ожидание человека %v — ожидался разговор с человеком", task.Status, task.HumanFlag)
	}
}

// Уборка идёт от папок и трогает только свои проекты: в хозяйстве раннера
// лежат клоны обоих полигонов.
func TestSweepLeavesForeignProjects(t *testing.T) {
	o := newOffice(t)
	o.withForge(&fakeForge{url: "https://github.test/kao73/client/pull/12", state: forge.Merged})
	o.agent.commit = "работа автора"
	o.approved(t, "OFF-1")
	workdir := o.agent.seen.Workdir

	// Проект уехал к другому трекеру: этот раннер про него больше не знает.
	o.Projects = tracker.Projects{}

	o.pass(t)
	if _, err := os.Stat(workdir); err != nil {
		t.Errorf("уборка снесла папку чужого проекта: %v", err)
	}
}

// Грязную папку уборка не сносит: снос — это `worktree remove --force`,
// а незакоммиченной работе другого места нет.
func TestSweepKeepsDirtyWorktree(t *testing.T) {
	o := newOffice(t)
	f := o.withForge(&fakeForge{url: "https://github.test/kao73/client/pull/13", state: forge.Open})
	o.agent.commit = "работа автора"
	o.approved(t, "OFF-1")
	workdir := o.agent.seen.Workdir
	o.pass(t)

	if err := os.WriteFile(filepath.Join(workdir, "недоделка.txt"), []byte("не потерять"), 0o644); err != nil {
		t.Fatalf("файл не записан: %v", err)
	}
	f.state = forge.Merged
	o.pass(t)

	if task := o.get(t, "OFF-1"); task.Status != "Done" {
		t.Fatalf("статус %q, ожидался Done", task.Status)
	}
	if _, err := os.Stat(workdir); err != nil {
		t.Errorf("грязная папка снесена: %v", err)
	}
}

// Уборка покрывает и задачу, которую человек закрыл руками: она идёт от папок,
// а не от задач, и о том, кто двигал задачу, не спрашивает.
func TestSweepRemovesManuallyClosedTask(t *testing.T) {
	o := newOffice(t)
	o.agent.commit = "работа автора"
	o.approved(t, "OFF-1")
	workdir := o.agent.seen.Workdir

	if err := o.tasks.Transition("OFF-1", tracker.BySystem(), "Done"); err != nil {
		t.Fatalf("задача не переведена: %v", err)
	}
	o.pass(t)

	if _, err := os.Stat(workdir); !os.IsNotExist(err) {
		t.Errorf("папка закрытой руками задачи осталась: %v", err)
	}
}
