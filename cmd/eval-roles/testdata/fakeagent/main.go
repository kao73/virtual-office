// Command fakeagent stands in for cmd/run-agent in cmd/eval-roles tests: it
// accepts the same flag shape and writes a scripted result.json instead of
// making a real LLM call. Behavior is controlled either by environment
// variables (FAKE_AGENT_RESULT, FAKE_AGENT_EXIT) or by per-fixture control
// files (<workdir>/.fake-result.json, <workdir>/.fake-exit), so different
// cases invoked in the same process can still behave differently.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
