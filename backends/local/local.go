// Package local исполняет подготовленный запуск прямо на хосте.
// Изоляции здесь нет никакой, кроме заданной адаптером: этот бэкенд нужен,
// чтобы отлаживать контур без контейнера.
package local

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/kao73/virtual-office/runner"
)

// killGrace — сколько ждать после сигнала, прежде чем добивать процесс.
const killGrace = 5 * time.Second

// Run запускает агента в рабочей папке с окружением и пределом времени из Launch.
// Весь вывод агента, и stdout, и stderr, уходит в logPath.
// Возвращает код выхода агента; ошибка означает, что запуск не состоялся
// или был прерван по таймауту.
func Run(ctx context.Context, l *runner.Launch, logPath string) (int, error) {
	log, err := os.Create(logPath)
	if err != nil {
		return -1, fmt.Errorf("%s не создан: %w", logPath, err)
	}
	defer log.Close()

	ctx, cancel := context.WithTimeout(ctx, l.Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, l.Argv[0], l.Argv[1:]...)
	cmd.Dir = l.Workdir
	// Изоляции здесь нет, поэтому окружение хоста воспроизводится целиком,
	// а поверх ложится то, что задал сам запуск.
	cmd.Env = append(append(make([]string, 0, len(l.HostEnv)+len(l.Env)), l.HostEnv...), l.Env...)
	cmd.Stdin = strings.NewReader(l.Stdin)
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.WaitDelay = killGrace

	runErr := cmd.Run()

	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		// Обёрнутая runner.ErrRunTimeout, а не просто текст: вышедшее время
		// означает «работал и не успел», а прочие беды этой функции — «прогона
		// не было». По коду -1 они неразличимы, по errors.Is — да.
		return -1, runner.RunTimeout(l.Timeout, "")
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	if runErr != nil {
		return -1, fmt.Errorf("агент не запущен: %w", runErr)
	}
	return 0, nil
}

// Platform — хост как он есть: изоляции нет, агент бежит в системе оператора.
func Platform() runner.Platform {
	return runner.HostPlatform()
}
