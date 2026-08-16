package sbx

import (
	"slices"
	"strings"
	"testing"

	"github.com/kao73/virtual-office/runner"
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
		"create", "--name", "office-550e8400", "claude",
		"/tmp/client",
		"/tmp/office-run-1/role:ro",
		"/tmp/office-run-1/config",
	}
	if !slices.Equal(got, want) {
		t.Errorf("команда создания\nполучена:  %q\nожидалась: %q", got, want)
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
