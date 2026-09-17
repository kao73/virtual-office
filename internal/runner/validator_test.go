package runner

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// configRoot — корень конфиг-репозитория из тестов пакета runner.
func configRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("корень репозитория не определён: %v", err)
	}
	return root
}

// cloneOffice — офис из этого репозитория в режиме клона: ограждение
// собирается go build, как у обёрток bin/*.
func cloneOffice(t *testing.T) Office {
	t.Helper()
	return Office{Root: configRoot(t), Source: SourceClone}
}

func TestEnsureValidatorBuildsExecutableForHost(t *testing.T) {
	home := t.TempDir()
	t.Setenv(HomeEnv, home)

	path, err := EnsureValidator(cloneOffice(t), HostPlatform())
	if err != nil {
		t.Fatalf("валидатор не собран: %v", err)
	}

	want := filepath.Join(home, BinDir, "validate-result-"+HostPlatform().OS+"-"+HostPlatform().Arch)
	if path != want {
		t.Errorf("валидатор в %s, ожидался %s", path, want)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("валидатор не найден: %v", err)
	}
	// Тот же урок, что с хуком: без бита исполняемости запуск даёт код 126,
	// а всё, кроме 2, ограждением не считается.
	if fi.Mode()&0o111 == 0 {
		t.Errorf("валидатор %s не исполняемый: ограждение промолчит", path)
	}
}

// Собранный бинарник — копия контракта на момент сборки. Если оставлять уже
// лежащий, правка контракта не доедет до ограждения, и оно снова начнёт
// проверять не то, что раннер.
func TestEnsureValidatorReplacesStaleBinary(t *testing.T) {
	home := t.TempDir()
	t.Setenv(HomeEnv, home)
	root := cloneOffice(t)

	path, err := EnsureValidator(root, HostPlatform())
	if err != nil {
		t.Fatalf("валидатор не собран: %v", err)
	}
	if err := os.WriteFile(path, []byte("устаревший мусор"), 0o755); err != nil {
		t.Fatalf("подмена валидатора не удалась: %v", err)
	}

	if _, err := EnsureValidator(root, HostPlatform()); err != nil {
		t.Fatalf("валидатор не пересобран: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("валидатор не прочитан: %v", err)
	}
	if bytes.Equal(raw, []byte("устаревший мусор")) {
		t.Error("остался старый бинарник: правка контракта до ограждения не доедет")
	}
}

// В песочнице хук исполняется на Linux, а раннер живёт на хосте. Кросс-сборка
// обязана давать ELF, а не хостовый Mach-O, иначе ограждение не запустится вовсе.
func TestEnsureValidatorCrossCompilesForSandbox(t *testing.T) {
	t.Setenv(HomeEnv, t.TempDir())

	path, err := EnsureValidator(cloneOffice(t), Platform{OS: "linux", Arch: HostPlatform().Arch})
	if err != nil {
		t.Fatalf("валидатор под песочницу не собран: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("валидатор не прочитан: %v", err)
	}
	if !bytes.HasPrefix(raw, []byte("\x7fELF")) {
		t.Errorf("%s не ELF: под Linux собралось что-то другое", path)
	}
}

// Главная проверка шага: ограждение отвергает ровно то же, что раннер, и
// объясняет причину. Раньше хук смотрел только наличие поля outcome — и агент
// уходил с результатом, который раннер потом отвергал, теряя сделанную работу.
func TestValidatorRejectsWhatRunnerRejects(t *testing.T) {
	t.Setenv(HomeEnv, t.TempDir())
	validator, err := EnsureValidator(cloneOffice(t), HostPlatform())
	if err != nil {
		t.Fatalf("валидатор не собран: %v", err)
	}

	cases := []struct {
		name     string
		content  string // пустая строка означает «файла нет»
		wantCode int
		wantPart string
	}{
		{name: "валидный результат", content: `{"outcome":"done","summary":"с.","next_owner":"none"}`},
		{name: "нет next_owner", content: `{"outcome":"done","summary":"с."}`, wantCode: 2, wantPart: "next_owner"},
		{name: "неизвестное поле", content: `{"outcome":"done","summary":"с.","next_owner":"none","ещё":1}`, wantCode: 2, wantPart: "ещё"},
		{name: "хвост после объекта", content: `{"outcome":"done","summary":"с.","next_owner":"none"} лишнее`, wantCode: 2, wantPart: "лишнее"},
		{name: "вопросы не при том исходе", content: `{"outcome":"done","summary":"с.","next_owner":"none","questions":[{"text":"?"}]}`, wantCode: 2, wantPart: "questions"},
		{name: "файла нет", wantCode: 2, wantPart: "не прочитан"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "result.json")
			if tc.content != "" {
				if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
					t.Fatalf("файл результата не записан: %v", err)
				}
			}

			var stderr bytes.Buffer
			cmd := exec.Command(validator, path)
			cmd.Stderr = &stderr
			code := 0
			var exitErr *exec.ExitError
			switch err := cmd.Run(); {
			case errors.As(err, &exitErr):
				code = exitErr.ExitCode()
			case err != nil:
				t.Fatalf("валидатор не запустился: %v", err)
			}

			if code != tc.wantCode {
				t.Errorf("код %d, ожидался %d; stderr: %s", code, tc.wantCode, stderr.String())
			}
			if tc.wantPart != "" && !strings.Contains(stderr.String(), tc.wantPart) {
				t.Errorf("причина %q не названа агенту: %s", tc.wantPart, stderr.String())
			}
		})
	}
}

// Ограждение вызывают шеллом, и любая ошибка вызова обязана блокировать выход:
// код, отличный от 2, Claude Code считает неблокирующей ошибкой и молча идёт дальше.
func TestValidatorBlocksOnMisuse(t *testing.T) {
	t.Setenv(HomeEnv, t.TempDir())
	validator, err := EnsureValidator(cloneOffice(t), HostPlatform())
	if err != nil {
		t.Fatalf("валидатор не собран: %v", err)
	}

	var stderr bytes.Buffer
	cmd := exec.Command(validator)
	cmd.Stderr = &stderr
	var exitErr *exec.ExitError
	if err := cmd.Run(); !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 {
		t.Errorf("вызов без пути дал %v, ожидался код 2; stderr: %s", err, stderr.String())
	}
	if stderr.Len() == 0 {
		t.Error("валидатор блокирует молча")
	}
}

// Поставка без встроенных ограждений — сборка без -tags release. Отказ
// называет платформу и оба выхода и не подсовывает ограждение другой платформы.
func TestEnsureValidatorPayloadRefusesWhenNothingEmbedded(t *testing.T) {
	root := t.TempDir()
	o := Office{Root: root, Identity: "v0.7.0", Source: SourcePayload}
	_, err := EnsureValidator(o, Platform{OS: "linux", Arch: "amd64"})
	if err == nil {
		t.Fatal("ограждения нет, а отказа нет")
	}
	for _, want := range []string{"linux/amd64", "-tags release", ConfigRootEnv} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("отказ не называет %q: %v", want, err)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(root, BinDir)); len(entries) != 0 {
		t.Errorf("в bin/ что-то появилось: %v", entries)
	}
}
