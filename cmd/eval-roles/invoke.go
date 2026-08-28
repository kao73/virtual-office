package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/kao73/virtual-office/internal/runner"
)

// runAgentBinEnv позволяет тестам подставить фиктивный run-agent вместо
// сборки настоящего cmd/run-agent — тому нужны настоящие креды, и он тратил
// бы настоящие деньги при каждом прогоне тестов.
const runAgentBinEnv = "EVAL_ROLES_RUN_AGENT_BIN"

// resolveRunAgentBin возвращает бинарник run-agent для вызова: переопределение
// из runAgentBinEnv, если оно задано, иначе — свежесобранный cmd/run-agent.
func resolveRunAgentBin(repoRoot, binDir string) (string, error) {
	if bin := os.Getenv(runAgentBinEnv); bin != "" {
		return bin, nil
	}
	return buildRunAgent(repoRoot, binDir)
}

func buildRunAgent(repoRoot, binDir string) (string, error) {
	bin := filepath.Join(binDir, "run-agent")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/run-agent")
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("run-agent не собран: %w: %s", err, out)
	}
	return bin, nil
}

// runRoleAgent вызывает binPath как run-agent против workdir, затем читает и
// разбирает оставленный им result.json. Код выхода 2 (собственная конвенция
// run-agent для инфраструктурной беды) сообщается как ошибка; коды 0 и 1 оба
// идут дальше к чтению результата — 1 это законный прогон с outcome=failed,
// на который может как раз проверять outcome-проверка.
func runRoleAgent(binPath, repoRoot, role, workdir, taskPath string) (runner.Result, int, error) {
	cmd := exec.Command(binPath, "--role", role, "--workdir", workdir, "--task", taskPath, "--eval")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "OFFICE_CONFIG_ROOT="+repoRoot)
	out, runErr := cmd.CombinedOutput()

	code := 0
	var exitErr *exec.ExitError
	switch {
	case errors.As(runErr, &exitErr):
		code = exitErr.ExitCode()
	case runErr != nil:
		return runner.Result{}, 0, fmt.Errorf("run-agent не запущен: %w: %s", runErr, out)
	}
	if code == 2 {
		return runner.Result{}, code, fmt.Errorf("run-agent завершился инфраструктурной бедой (код 2): %s", out)
	}

	result, err := runner.ReadResult(workdir)
	if err != nil {
		return runner.Result{}, code, fmt.Errorf("result.json не прочитан: %w", err)
	}
	return result, code, nil
}
