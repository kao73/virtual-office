package sbx

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kao73/virtual-office/internal/runner"
)

func fixtureLaunch() *runner.Launch {
	return &runner.Launch{
		ID:         "550e8400-e29b-41d4-a716-446655440000",
		Argv:       []string{"claude", "--print", "--max-turns", "50"},
		Workdir:    "/tmp/client",
		Env:        []string{"CLAUDE_CONFIG_DIR=/tmp/office-run-1/config", "CLAUDE_CODE_OAUTH_TOKEN=секрет"},
		HostEnv:    []string{"PATH=/opt/homebrew/bin", "HOME=/tmp/office-run-1/home"},
		SecretVars: []string{"CLAUDE_CODE_OAUTH_TOKEN"},
		Workspaces: []runner.Workspace{
			{Path: "/tmp/client"},
			{Path: "/tmp/office-run-1/role", ReadOnly: true},
			{Path: "/tmp/office-run-1/config"},
		},
	}
}

func TestCreateArgs(t *testing.T) {
	got := createArgs("office-550e8400", fixtureLaunch())

	want := []string{
		"create", "--name", "office-550e8400", "--template", Template, "claude",
		"/tmp/client",
		"/tmp/office-run-1/role:ro",
		"/tmp/office-run-1/config",
	}
	if !slices.Equal(got, want) {
		t.Errorf("команда создания\nполучена:  %q\nожидалась: %q", got, want)
	}
}

// Скрипт печёт образ под тегом, который сам же и держит в отдельной переменной —
// ничто не мешает ему разъехаться с Template, кроме дисциплины README. Тест хотя бы
// ловит разъезд механически: без него TestCreateArgs зелёный при любом значении
// константы, потому что сравнивает только с самой Template, а не с реальным тегом.
func TestBakeScriptTagMatchesTemplate(t *testing.T) {
	path := "../../../office/sbx-kits/bake-comet-template.sh"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("скрипт печи не прочитан: %v", err)
	}
	if !strings.Contains(string(data), `TAG="`+Template+`"`) {
		t.Errorf("bake-comet-template.sh не печёт тег %q — разъехался с internal/backends/sbx.Template", Template)
	}
}

func TestExecArgs(t *testing.T) {
	l := fixtureLaunch()
	got := execArgs("office-550e8400", l)

	// Имя песочницы обязано стоять ровно перед командой агента: всё, что после
	// него, sbx отдаёт агенту как есть.
	i := slices.Index(got, "office-550e8400")
	if i < 0 {
		t.Fatalf("в команде нет имени песочницы: %q", got)
	}
	if !slices.Equal(got[i+1:], l.Argv) {
		t.Errorf("команда агента искажена\nполучена:  %q\nожидалась: %q", got[i+1:], l.Argv)
	}

	if !slices.Contains(got[:i], "--interactive") {
		t.Error("нет --interactive: стартовое сообщение идёт через stdin и не дойдёт")
	}
	if j := slices.Index(got, "--workdir"); j < 0 || got[j+1] != l.Workdir {
		t.Errorf("рабочая директория не задана: %q", got)
	}

	for _, kv := range l.Env {
		name, _, _ := strings.Cut(kv, "=")
		// Секрет передаётся одним именем: значение sbx наследует из своего окружения.
		want := kv
		if slices.Contains(l.SecretVars, name) {
			want = name
		}
		if !slices.Contains(got[:i], want) {
			t.Errorf("переменная запуска %q не передана в песочницу как %q", kv, want)
		}
	}
	// Хостовое окружение внутри песочницы вредно: тамошний PATH указывает
	// не туда, а HOME принадлежит другому пользователю.
	for _, kv := range l.HostEnv {
		if slices.Contains(got, kv) {
			t.Errorf("хостовая переменная %q просочилась в песочницу", kv)
		}
	}
}

// Командная строка процесса sbx видна в `ps` любому пользователю хоста, поэтому
// значение креда в неё попадать не должно. Форма `-e VAR` без значения велит sbx
// взять переменную из собственного окружения — см. docs/notes/sbx.md.
func TestExecArgsKeepsSecretOutOfCommandLine(t *testing.T) {
	l := fixtureLaunch()
	got := execArgs("office-550e8400", l)

	if line := strings.Join(got, " "); strings.Contains(line, "секрет") {
		t.Errorf("значение креда попало в командную строку, его видно в ps: %s", line)
	}
	if !slices.Contains(got, "CLAUDE_CODE_OAUTH_TOKEN") {
		t.Errorf("имя переменной с кредом не передано, агенту нечем авторизоваться: %q", got)
	}
	// Несекретное по-прежнему идёт значением: прятать его незачем.
	if !slices.Contains(got, "CLAUDE_CONFIG_DIR=/tmp/office-run-1/config") {
		t.Errorf("обычная переменная запуска потеряна: %q", got)
	}
}

// Раз значение ушло из командной строки, его обязан нести процесс sbx —
// иначе наследовать станет нечего и агент останется без авторизации.
func TestRunEnvCarriesSecretValue(t *testing.T) {
	got := runEnv(fixtureLaunch())

	if !slices.Contains(got, "CLAUDE_CODE_OAUTH_TOKEN=секрет") {
		t.Errorf("окружение sbx не несёт значения креда: наследовать нечего")
	}
	// Хостовое окружение процессу sbx нужно, чтобы он сам работал: он бежит на хосте.
	if !slices.ContainsFunc(got, func(kv string) bool { return strings.HasPrefix(kv, "PATH=") }) {
		t.Error("в окружении sbx нет PATH: он сам может не запуститься")
	}
	// А вот подменённый HOME и прочее хостовое окружение запуска внутрь не едут.
	if slices.Contains(got, "HOME=/tmp/office-run-1/home") {
		t.Error("окружение неизолированного запуска уехало в песочницу")
	}
}

func TestSandboxName(t *testing.T) {
	cases := map[string]string{
		"550e8400-e29b-41d4-a716-446655440000": "office-550e8400",
		"ABCDEF12-3456":                        "office-abcdef12",
		"ab":                                   "office-ab",
		"":                                     "office-run",
		"----":                                 "office-run",
	}

	for id, want := range cases {
		if got := sandboxName(id); got != want {
			t.Errorf("для %q получено %q, ожидалось %q", id, got, want)
		}
	}
}

// Имя песочницы попадает в командную строку и в вывод sbx ls: в нём не должно
// быть ничего, кроме букв, цифр и дефиса.
func TestSandboxNameIsSafe(t *testing.T) {
	got := sandboxName("../../ЗлоЙ/id; rm -rf /")

	for _, r := range got {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyz0123456789-", r) {
			t.Fatalf("в имени %q есть недопустимый символ %q", got, r)
		}
	}
	if !strings.HasPrefix(got, "office-") {
		t.Errorf("имя %q не помечено как наше", got)
	}
}

// fakeSbx — подделка CLI: запоминает вызовы и отвечает заготовленным.
type fakeSbx struct {
	list  string // что отвечает `sbx ls --quiet`
	fail  error  // чем отвечает `sbx rm`
	calls [][]string
}

func (f *fakeSbx) run(args ...string) (string, error) {
	f.calls = append(f.calls, args)
	if len(args) > 0 && args[0] == "rm" {
		return "", f.fail
	}
	return f.list, nil
}

// Долг этапа 2: убитый раннер оставлял песочницу работающей, и убрать её было
// некому. Теперь reap сносит её по тому же run_id, каким её и называли.
func TestSandboxesRemoveDeletesSandboxOfRun(t *testing.T) {
	sbx := &fakeSbx{list: "office-11111111\noffice-550e8400\noffice-22222222\n"}
	s := Sandboxes{run: sbx.run}

	removed, err := s.Remove("550e8400-e29b-41d4-a716-446655440000")
	if err != nil {
		t.Fatalf("песочница не убрана: %v", err)
	}
	if !removed {
		t.Error("уборка не признаётся состоявшейся, а песочница была")
	}

	if len(sbx.calls) == 0 {
		t.Fatal("sbx не позван вовсе: песочница осталась жить")
	}
	last := sbx.calls[len(sbx.calls)-1]
	want := []string{"rm", "--force", "office-550e8400"}
	if !slices.Equal(last, want) {
		t.Errorf("команда уборки\nполучена:  %q\nожидалась: %q", last, want)
	}
	// Соседние песочницы — чужая собственность: их не касаются даже мимоходом.
	for _, call := range sbx.calls {
		if slices.Contains(call, "--all") || slices.Contains(call, "office-11111111") {
			t.Errorf("уборка вышла за пределы своей песочницы: %q", call)
		}
	}
}

// Песочница живёт на той машине, где шёл прогон. Reap на другой машине не найдёт
// её в списке — и это нормальный ход дел, а не беда: `sbx rm` несуществующей
// отвечает кодом 1 (проверено, docs/notes/sbx.md).
func TestSandboxesRemoveSkipsMissingSandbox(t *testing.T) {
	sbx := &fakeSbx{list: "office-11111111\n"}
	s := Sandboxes{run: sbx.run}

	removed, err := s.Remove("550e8400-e29b-41d4-a716-446655440000")
	if err != nil {
		t.Fatalf("отсутствие песочницы сочтено бедой: %v", err)
	}
	// Отвечать «убрана», когда убирать было нечего, — значит врать в логе:
	// именно так и вышло на первой живой проверке.
	if removed {
		t.Error("уборка признана состоявшейся, хотя песочницы не было")
	}
	for _, call := range sbx.calls {
		if len(call) > 0 && call[0] == "rm" {
			t.Errorf("уборка позвана для песочницы, которой нет: %q", call)
		}
	}
}

// Не убравшаяся песочница обязана быть слышна: reap объяснит её человеку,
// а молча потерянная — это ровно тот долг, который мы закрываем.
func TestSandboxesRemoveReportsFailure(t *testing.T) {
	sbx := &fakeSbx{list: "office-550e8400\n", fail: errors.New("sbx не отвечает")}
	s := Sandboxes{run: sbx.run}

	if _, err := s.Remove("550e8400-e29b-41d4-a716-446655440000"); err == nil {
		t.Error("отказ уборки потерян: песочница осталась, а никто не узнал")
	}
}

// Задача 6 (найдено живым прогоном Task 5): logPath = .../.agent/run.log —
// либо Clone.FetchInto/.agent под --clone, либо opts.Workdir/.agent без него
// (internal/runagent.resultWorkdir решает, какой из двух; см. её
// doc-комментарий). По причинам, не зависящим от того, какая именно это
// папка — свежий worktree, самый первый прогон задачи и так далее — .agent
// там на момент вызова может быть ещё не заведён. os.Create без MkdirAll
// падал на этом уже после того, как sbx create --clone завела песочницу —
// агент в ней так и не стартовал, а Run сразу сносил её обратно.
func TestCreateLogCreatesParentDirectory(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, ".agent", "run.log")

	log, err := createLog(logPath)
	if err != nil {
		t.Fatalf("createLog: %v", err)
	}
	defer log.Close()

	if _, err := os.Stat(logPath); err != nil {
		t.Errorf("run.log не заведён: %v", err)
	}
}

func TestCreateLogPropagatesRealMkdirFailure(t *testing.T) {
	root := t.TempDir()
	// Файл на месте будущего каталога — MkdirAll не может создать директорию
	// поверх обычного файла, настоящая беда, а не законное «уже есть».
	blocker := filepath.Join(root, ".agent")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := createLog(filepath.Join(blocker, "run.log")); err == nil {
		t.Fatal("MkdirAll поверх обычного файла прошёл без ошибки")
	}
}
