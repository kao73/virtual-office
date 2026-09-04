package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCaseParsesAllCheckKinds(t *testing.T) {
	root := t.TempDir()
	caseDir := filepath.Join(root, "implementer", "sample-case")
	if err := os.MkdirAll(caseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	expect := `role: implementer
checks:
  - kind: outcome
    expect: done
  - kind: diff_scope
    allow: ["src/**"]
  - kind: fixture_tests
    command: "go test ./..."
  - kind: llm_judge
    criteria: "разбор глубокий"
    judge_role: reviewer
`
	if err := os.WriteFile(filepath.Join(caseDir, "expect.yaml"), []byte(expect), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := LoadCase(caseDir)
	if err != nil {
		t.Fatalf("case не разобран: %v", err)
	}
	if c.Role != "implementer" || len(c.Checks) != 4 {
		t.Errorf("case = %+v", c)
	}
	if c.Checks[3].Kind != "llm_judge" || c.Checks[3].Criteria != "разбор глубокий" || c.Checks[3].JudgeRole != "reviewer" {
		t.Errorf("llm_judge не разобран: %+v", c.Checks[3])
	}
}

func TestLoadCaseRejectsRoleMismatch(t *testing.T) {
	root := t.TempDir()
	caseDir := filepath.Join(root, "implementer", "sample-case")
	if err := os.MkdirAll(caseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	expect := "role: analyst\nchecks:\n  - kind: outcome\n    expect: done\n"
	if err := os.WriteFile(filepath.Join(caseDir, "expect.yaml"), []byte(expect), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCase(caseDir); err == nil {
		t.Error("несовпадение role/каталог не замечено")
	}
}

// fixture_tests-проверка с пустым command молча PASS'ила бы (`sh -c ""`
// выходит с 0) — это нужно отвергать при загрузке, а не оставлять пустой
// проверке зеленеть впустую.
func TestLoadCaseRejectsFixtureTestsWithoutCommand(t *testing.T) {
	root := t.TempDir()
	caseDir := filepath.Join(root, "implementer", "sample-case")
	if err := os.MkdirAll(caseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	expect := "role: implementer\nchecks:\n  - kind: fixture_tests\n"
	if err := os.WriteFile(filepath.Join(caseDir, "expect.yaml"), []byte(expect), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCase(caseDir); err == nil {
		t.Error("fixture_tests без command не замечен")
	}
}

// Неизвестный kind (опечатка вроде "outcom") без этой проверки всплыл бы
// только после платного прогона роли, в dispatchCheck, и выглядел бы в
// сводке как обычный FAIL — то есть как регресс роли, а не сломанный
// expect.yaml.
func TestLoadCaseRejectsUnknownKind(t *testing.T) {
	root := t.TempDir()
	caseDir := filepath.Join(root, "implementer", "sample-case")
	if err := os.MkdirAll(caseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(caseDir, "expect.yaml"), []byte("role: implementer\nchecks:\n  - kind: outcom\n    expect: done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCase(caseDir); err == nil {
		t.Error("неизвестный kind не замечен")
	}
}

// Пустой expect у outcome-проверки: got != want никогда не совпадёт с "", так
// что проверка технически безопасна (не зазеленеет впустую, как fixture_tests
// с пустым command) — но платить прогоном роли за то, что было предрешено
// опечаткой в expect.yaml, всё равно не стоит.
func TestLoadCaseRejectsOutcomeWithoutExpect(t *testing.T) {
	root := t.TempDir()
	caseDir := filepath.Join(root, "implementer", "sample-case")
	if err := os.MkdirAll(caseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(caseDir, "expect.yaml"), []byte("role: implementer\nchecks:\n  - kind: outcome\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCase(caseDir); err == nil {
		t.Error("outcome без expect не замечен")
	}
}

// expect: "don" (опечатка) прошёл бы загрузку и всплыл бы только после
// платного прогона роли, в outcomeChecker, как обычный FAIL — то есть как
// регресс роли, а не сломанный expect.yaml. `expect` — фиксированный список
// из пяти исходов, в отличие от next_owner (там роль может стоять любым
// именем, и docs/contracts/agent-io.md сознательно не проверяет её
// существование «на этапе 1» — LoadCase этому не противоречит).
func TestLoadCaseRejectsUnknownExpect(t *testing.T) {
	root := t.TempDir()
	caseDir := filepath.Join(root, "implementer", "sample-case")
	if err := os.MkdirAll(caseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(caseDir, "expect.yaml"), []byte("role: implementer\nchecks:\n  - kind: outcome\n    expect: don\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCase(caseDir); err == nil {
		t.Error("неизвестный expect не замечен")
	}
}

func TestLoadCaseRejectsEmptyChecks(t *testing.T) {
	root := t.TempDir()
	caseDir := filepath.Join(root, "implementer", "sample-case")
	if err := os.MkdirAll(caseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(caseDir, "expect.yaml"), []byte("role: implementer\nchecks: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCase(caseDir); err == nil {
		t.Error("пустой checks не замечен")
	}
}
