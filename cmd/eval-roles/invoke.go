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
//
// taskKey, если не пуст, идёт в --task-key: без него run-agent, как при
// обычном ручном запуске, не подставит трекерный ключ, а composeContext
// (internal/runner/input.go) без него не отличит каталог изменения фикстуры
// от «его нет» — строка «Каталог изменения» в context.md не появится вовсе,
// даже если фикстура его честно завела (см. discoverFixtureTaskKey).
//
// clone включает --clone бэкенда sbx (см. run-agent --clone): фикстура —
// уже обычный git-репозиторий, не bare и не worktree, поэтому её можно
// клонировать в песочницу как есть, без отдельной подготовки.
func runRoleAgent(binPath, repoRoot, role, workdir, taskPath, taskKey string, clone bool) (runner.Result, int, error) {
	args := []string{"--role", role, "--workdir", workdir, "--task", taskPath, "--eval"}
	if taskKey != "" {
		args = append(args, "--task-key", taskKey)
	}
	if clone {
		args = append(args, "--clone")
	}
	cmd := exec.Command(binPath, args...)
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
