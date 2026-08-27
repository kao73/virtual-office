package local

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kao73/virtual-office/internal/runner"
)

// launch собирает запуск поддельного «агента»: настоящий здесь не нужен,
// бэкенд отвечает только за процесс, лог и предел времени.
func launch(t *testing.T, script string, timeout time.Duration) (*runner.Launch, string) {
	t.Helper()
	workdir := t.TempDir()
	return &runner.Launch{
		Argv:    []string{"/bin/sh", "-c", script},
		Env:     []string{"PATH=" + os.Getenv("PATH")},
		Workdir: workdir,
		Timeout: timeout,
		Cleanup: func() error { return nil },
	}, filepath.Join(t.TempDir(), "run.log")
}

func TestRunReturnsExitCode(t *testing.T) {
	l, logPath := launch(t, "exit 3", time.Minute)

	code, err := Run(context.Background(), l, logPath)
	if err != nil {
		t.Fatalf("запуск не состоялся: %v", err)
	}
	if code != 3 {
		t.Errorf("код выхода %d, ожидался 3", code)
	}
}

func TestRunCapturesBothStreams(t *testing.T) {
	l, logPath := launch(t, "echo в-stdout; echo в-stderr >&2", time.Minute)

	if _, err := Run(context.Background(), l, logPath); err != nil {
		t.Fatalf("запуск не состоялся: %v", err)
	}

	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("лог не прочитан: %v", err)
	}
	for _, want := range []string{"в-stdout", "в-stderr"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("в логе нет %q:\n%s", want, raw)
		}
	}
}

// Стартовое сообщение агент получает через stdin — бэкенд обязан его доставить.
func TestRunDeliversStdin(t *testing.T) {
	l, logPath := launch(t, "cat", time.Minute)
	l.Stdin = "начни с чтения задачи"

	if _, err := Run(context.Background(), l, logPath); err != nil {
		t.Fatalf("запуск не состоялся: %v", err)
	}

	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("лог не прочитан: %v", err)
	}
	if !strings.Contains(string(raw), l.Stdin) {
		t.Errorf("на stdin ничего не пришло:\n%s", raw)
	}
}

func TestRunUsesWorkdir(t *testing.T) {
	l, logPath := launch(t, "pwd", time.Minute)

	if _, err := Run(context.Background(), l, logPath); err != nil {
		t.Fatalf("запуск не состоялся: %v", err)
	}

	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("лог не прочитан: %v", err)
	}
	// На macOS /var — симлинк на /private/var, поэтому сравниваем по хвосту пути.
	if !strings.Contains(string(raw), filepath.Base(l.Workdir)) {
		t.Errorf("агент работал не в workdir %s:\n%s", l.Workdir, raw)
	}
}

// Зависший агент обязан быть убит, а не держать раннер вечно.
func TestRunStopsAgentOnTimeout(t *testing.T) {
	l, logPath := launch(t, "sleep 30", 200*time.Millisecond)

	started := time.Now()
	_, err := Run(context.Background(), l, logPath)
	elapsed := time.Since(started)

	if err == nil {
		t.Fatal("агент превысил предел времени, но запуск признан успешным")
	}
	// Таймаут обязан отличаться типом, а не текстом и не кодом возврата: -1 этот
	// бэкенд возвращает ещё из двух мест, и по коду «работал и не успел»
	// неотличимо от «прогона не было». Разница в цене задачи — попытка.
	if !errors.Is(err, runner.ErrRunTimeout) {
		t.Errorf("таймаут не типизирован, errors.Is его не узнаёт: %v", err)
	}
	if !strings.Contains(err.Error(), "не уложился") {
		t.Errorf("ошибка не объясняет причину: %v", err)
	}
	if elapsed > 10*time.Second {
		t.Errorf("процесс убивали %s: предел времени не работает", elapsed)
	}
}
