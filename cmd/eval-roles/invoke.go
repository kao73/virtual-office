package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/kao73/virtual-office/internal/runner"
)

// runAgentBinEnv lets tests substitute a fake run-agent stand-in instead of
// building the real cmd/run-agent (which would require real credentials and
// spend real money on every test run).
const runAgentBinEnv = "EVAL_ROLES_RUN_AGENT_BIN"

// resolveRunAgentBin returns the run-agent binary to invoke: the override
// named by runAgentBinEnv if set, otherwise a freshly built cmd/run-agent.
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

// runRoleAgent invokes binPath as run-agent against workdir, then reads and
// parses the result.json it left behind. Exit code 2 (run-agent's own
// infra-failure convention) is reported as an error; exit 0 or 1 both
// proceed to reading the result — exit 1 is a legitimate outcome=failed run
// that an outcome check might itself be asserting against.
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
