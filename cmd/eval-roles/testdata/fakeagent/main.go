// Команда fakeagent подменяет cmd/run-agent в тестах cmd/eval-roles: принимает
// те же флаги и пишет заскриптованный result.json вместо настоящего вызова
// LLM. Поведение управляется либо переменными окружения (FAKE_AGENT_RESULT,
// FAKE_AGENT_EXIT), либо файлами управления на кейс (<workdir>/.fake-result.json,
// <workdir>/.fake-exit) — так разные кейсы, вызванные в одном процессе, всё
// равно могут вести себя по-разному.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kao73/virtual-office/internal/runner"
)

func main() {
	workdir := flag.String("workdir", "", "")
	_ = flag.String("role", "", "")
	_ = flag.String("task", "", "")
	_ = flag.Bool("eval", false, "")
	flag.Parse()

	if *workdir == "" {
		fmt.Fprintln(os.Stderr, "fakeagent: --workdir обязателен")
		os.Exit(2)
	}

	// Настоящий run-agent прячет .agent/ от git через ExcludeAgentDir
	// (internal/runner/input.go) до того, как агент начинает писать в рабочую
	// папку — иначе .agent/result.json сам попадал бы в diff_scope как
	// изменение вне allow. fakeagent повторяет это: без этого golden-кейсы
	// с diff_scope никогда не проверялись бы по-настоящему в go test — только
	// вручную, реальным run-agent.
	if err := runner.ExcludeAgentDir(*workdir); err != nil {
		fmt.Fprintln(os.Stderr, "fakeagent:", err)
		os.Exit(2)
	}

	result := os.Getenv("FAKE_AGENT_RESULT")
	if raw, err := os.ReadFile(filepath.Join(*workdir, ".fake-result.json")); err == nil {
		result = string(raw)
	}

	code := 0
	if raw := os.Getenv("FAKE_AGENT_EXIT"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			fmt.Fprintln(os.Stderr, "fakeagent: FAKE_AGENT_EXIT некорректен:", err)
			os.Exit(2)
		}
		code = n
	}
	if raw, err := os.ReadFile(filepath.Join(*workdir, ".fake-exit")); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
			code = n
		}
	}

	if result != "" {
		agentDir := filepath.Join(*workdir, ".agent")
		if err := os.MkdirAll(agentDir, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "fakeagent:", err)
			os.Exit(2)
		}
		if err := os.WriteFile(filepath.Join(agentDir, "result.json"), []byte(result), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "fakeagent:", err)
			os.Exit(2)
		}
	}
	os.Exit(code)
}
