package sbx

import (
	"slices"
	"strings"
	"testing"

	"github.com/kao73/virtual-office/runner"
)

func fixtureLaunch() *runner.Launch {
	return &runner.Launch{
		ID:      "550e8400-e29b-41d4-a716-446655440000",
		Argv:    []string{"claude", "--print", "--max-turns", "50"},
		Workdir: "/tmp/client",
		Env:     []string{"CLAUDE_CONFIG_DIR=/tmp/office-run-1/config", "CLAUDE_CODE_OAUTH_TOKEN=секрет"},
		HostEnv: []string{"PATH=/opt/homebrew/bin", "HOME=/Users/kao"},
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
		if !slices.Contains(got[:i], kv) {
			t.Errorf("переменная запуска %q не передана в песочницу", kv)
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
