// Package sbx исполняет запуск в песочнице Docker Sandboxes.
//
// Песочница создаётся на один прогон и сносится после него: свежая файловая
// система каждый раз, никакого состояния между задачами. Создание стоит около
// пяти секунд при закэшированном образе — при прогоне в десятки секунд это
// приемлемо. Если накладные вырастут, схема меняется на долгоживущую песочницу
// на роль, как предполагает DESIGN.md §2.6.
//
// Пути внутри песочницы совпадают с хостовыми, поэтому командная строка
// из Launch идёт без единой правки.
package sbx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/kao73/virtual-office/runner"
)

// Executable — CLI Docker Sandboxes.
const Executable = "sbx"

// Agent — вид песочницы в терминах sbx: он определяет, что внутри стоит.
const Agent = "claude"

const (
	createTimeout = 5 * time.Minute
	removeTimeout = 2 * time.Minute
	killGrace     = 5 * time.Second
)

// Run создаёт песочницу, исполняет в ней подготовленный запуск и сносит её.
// Весь вывод агента, и stdout, и stderr, уходит в logPath.
// Возвращает код выхода агента; ошибка означает, что запуск не состоялся
// или был прерван по таймауту.
func Run(ctx context.Context, l *runner.Launch, logPath string) (int, error) {
	name := sandboxName(l.ID)

	if err := create(ctx, name, l); err != nil {
		return -1, err
	}
	// Песочницу сносим в любом исходе: брошенная песочница держит ресурсы
	// и путается под ногами у следующего прогона.
	defer remove(name)

	log, err := os.Create(logPath)
	if err != nil {
		return -1, fmt.Errorf("%s не создан: %w", logPath, err)
	}
	defer log.Close()

	execCtx, cancel := context.WithTimeout(ctx, l.Timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, Executable, execArgs(name, l)...)
	cmd.Env = runEnv(l)
	cmd.Stdin = strings.NewReader(l.Stdin)
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.WaitDelay = killGrace

	runErr := cmd.Run()

	if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
		return -1, fmt.Errorf("агент не уложился в %s и был остановлен", l.Timeout)
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	if runErr != nil {
		return -1, fmt.Errorf("агент не запущен в песочнице %s: %w", name, runErr)
	}
	return 0, nil
}

// create поднимает песочницу с рабочими пространствами из Launch.
// Каталог материалов роли отдаётся только на чтение — это проверяется ядром,
// а не соглашением.
func create(ctx context.Context, name string, l *runner.Launch) error {
	if len(l.Workspaces) == 0 {
		return errors.New("нечего отдать песочнице: список рабочих пространств пуст")
	}

	ctx, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, Executable, createArgs(name, l)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("песочница %s не создана: %w\n%s", name, err, out)
	}
	return nil
}

// remove сносит песочницу. Контекст берётся свой: прогон мог закончиться
// по таймауту, но убрать за собой надо всё равно.
func remove(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), removeTimeout)
	defer cancel()

	// В неинтерактивном режиме sbx требует --force: подтверждать некому.
	out, err := exec.CommandContext(ctx, Executable, "rm", "--force", name).CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "песочница %s не снесена, уберите вручную: %v\n%s\n", name, err, out)
	}
}

// createArgs собирает команду создания песочницы. Рабочие пространства идут
// после имени агента; суффикс :ro означает монтирование только на чтение.
func createArgs(name string, l *runner.Launch) []string {
	args := make([]string, 0, 4+len(l.Workspaces))
	args = append(args, "create", "--name", name, Agent)
	for _, ws := range l.Workspaces {
		path := ws.Path
		if ws.ReadOnly {
			path += ":ro"
		}
		args = append(args, path)
	}
	return args
}

// execArgs собирает команду запуска агента внутри песочницы.
// В окружение попадает только то, что задал сам запуск: хостовые PATH и HOME
// внутри указывают в пустоту, и передавать их нельзя.
//
// Секреты передаются одним именем, без значения: командная строка процесса sbx
// видна в `ps` любому пользователю хоста. По форме `--env VAR` sbx берёт значение
// из собственного окружения, которое готовит runEnv.
func execArgs(name string, l *runner.Launch) []string {
	args := make([]string, 0, 6+2*len(l.Env)+len(l.Argv))
	args = append(args, "exec", "--interactive", "--workdir", l.Workdir)
	for _, kv := range l.Env {
		if varName, _, _ := strings.Cut(kv, "="); slices.Contains(l.SecretVars, varName) {
			args = append(args, "--env", varName)
			continue
		}
		args = append(args, "--env", kv)
	}
	args = append(args, name)
	return append(args, l.Argv...)
}

// runEnv — окружение самого процесса sbx. Он бежит на хосте, поэтому наследует
// хостовое окружение, а поверх кладёт значения секретов: из командной строки они
// убраны, и взять их песочнице больше неоткуда.
//
// Окружение неизолированного запуска (l.HostEnv) сюда не входит: оно описывает
// агента на хосте, а не sbx.
func runEnv(l *runner.Launch) []string {
	env := os.Environ()
	for _, kv := range l.Env {
		if varName, _, _ := strings.Cut(kv, "="); slices.Contains(l.SecretVars, varName) {
			env = append(env, kv)
		}
	}
	return env
}

// sandboxName делает из идентификатора запуска имя, пригодное для sbx.
func sandboxName(id string) string {
	short := strings.Map(func(r rune) rune {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z':
			return r
		default:
			return -1
		}
	}, strings.ToLower(id))

	if len(short) > 8 {
		short = short[:8]
	}
	if short == "" {
		short = "run"
	}
	return "office-" + short
}

// Platform — где исполняется всё, что запущено в песочнице: сам агент и
// ограждения роли. Внутри Linux, а раннер может стоять на чём угодно, поэтому
// бинарник ограждения собирается именно под эту платформу, а не под хостовую.
//
// Архитектура берётся хостовая: песочница — microVM на том же железе.
// Значение проверено `uname` внутри, см. docs/notes/sbx.md.
func Platform() runner.Platform {
	return runner.Platform{OS: "linux", Arch: runtime.GOARCH}
}
