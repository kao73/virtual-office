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
	policyTimeout = 1 * time.Minute
	removeTimeout = 2 * time.Minute
	killGrace     = 5 * time.Second
)

// Run создаёт песочницу, исполняет в ней подготовленный запуск и сносит её.
// Весь вывод агента, и stdout, и stderr, уходит в logPath.
// Возвращает код выхода агента; ошибка означает, что запуск не состоялся
// или был прерван по таймауту.
func Run(ctx context.Context, l *runner.Launch, logPath string) (int, error) {
	name := sandboxName(l.ID)

	if err := prepare(ctx, name, l, sbxRun); err != nil {
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

// step — один вызов sbx. Подменяется в тестах: порядок шагов подготовки важен,
// а настоящего демона для его проверки не нужно.
type step func(ctx context.Context, args ...string) error

// sbxRun зовёт настоящий CLI.
func sbxRun(ctx context.Context, args ...string) error {
	out, err := exec.CommandContext(ctx, Executable, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("sbx %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return nil
}

// prepare поднимает песочницу и настраивает её сеть.
//
// Порядок обязателен: правило сети привязывается к песочнице по имени, и до её
// создания привязывать не к чему. Позже нельзя тоже — агент к тому времени уже
// работает и часть запросов сделает вслепую.
//
// Каталог материалов роли отдаётся только на чтение — это проверяется ядром,
// а не соглашением.
func prepare(ctx context.Context, name string, l *runner.Launch, run step) error {
	if len(l.Workspaces) == 0 {
		return errors.New("нечего отдать песочнице: список рабочих пространств пуст")
	}

	createCtx, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()
	if err := run(createCtx, createArgs(name, l)...); err != nil {
		return fmt.Errorf("песочница %s не создана: %w", name, err)
	}

	if len(l.NetworkAllow) == 0 {
		return nil
	}

	policyCtx, cancelPolicy := context.WithTimeout(ctx, policyTimeout)
	defer cancelPolicy()
	if err := run(policyCtx, policyArgs(name, l.NetworkAllow)...); err != nil {
		// Дальше идти нельзя: роль пойдёт работать без сети, которую просила,
		// и провалится непонятно почему. Лучше не начинать.
		return fmt.Errorf("песочнице %s не разрешены хосты роли (%s): %w",
			name, strings.Join(l.NetworkAllow, ", "), err)
	}
	return nil
}

// policyArgs — правило сети на одну песочницу.
//
// Правило именно разрешающее и именно локальное. Запрещающее правило сильнее
// разрешающего, поэтому «закрыть всё и открыть нужное» на уровне песочницы
// не собирается вовсе: закрыв всё, закроешь и то, что нужно самому агенту.
// Базовая политика машины поэтому глобальная и ставится человеком один раз
// (`sbx policy init deny-all`), а роль добавляет к ней своё — см. docs/notes/sbx.md.
//
// Хосты уезжают как их написала роль: подстановки, порты и подсети разбирает sbx.
func policyArgs(name string, hosts []string) []string {
	return []string{"policy", "allow", "network", "--sandbox", name, strings.Join(hosts, ",")}
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

// Sandboxes — уборка песочниц, переживших свой прогон.
//
// Обычный прогон сносит песочницу сам, но раннера могли убить — тогда microVM
// остаётся работать и держать память и диск, а убрать её некому. Зовёт уборщика
// reaper: он знает run_id мёртвой аренды, а имя песочницы из него собирается
// тем же правилом, что и при создании.
type Sandboxes struct {
	// run — вызов sbx. Поле нужно тестам: настоящий CLI подменяется подделкой.
	run func(args ...string) (string, error)
}

// Remove сносит песочницу прогона и отвечает, нашлось ли что сносить.
//
// Сначала список, потом снос: `sbx rm` несуществующей песочницы отвечает
// отказом, а её отсутствие — обычное дело. Reap мог быть запущен не на той
// машине, где шёл прогон, и чинить ему там нечего.
//
// Отвечать «убрана» в этом случае нельзя: на первой живой проверке лог именно
// так и соврал — сообщил об уборке трёх песочниц, которых не существовало.
//
// Метём точечно, по одному имени. Всё остальное в списке — чужая собственность:
// песочница соседнего процесса или другой роли, и она может быть жива.
func (s Sandboxes) Remove(runID string) (bool, error) {
	name := sandboxName(runID)

	out, err := s.exec("ls", "--quiet")
	if err != nil {
		return false, fmt.Errorf("список песочниц не получен: %w", err)
	}
	if !slices.Contains(strings.Fields(out), name) {
		return false, nil
	}

	if _, err := s.exec("rm", "--force", name); err != nil {
		return false, fmt.Errorf("песочница %s не снесена: %w", name, err)
	}
	return true, nil
}

// exec зовёт sbx: настоящий CLI или подделку из теста.
func (s Sandboxes) exec(args ...string) (string, error) {
	if s.run != nil {
		return s.run(args...)
	}

	ctx, cancel := context.WithTimeout(context.Background(), removeTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, Executable, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("sbx %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return string(out), nil
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
