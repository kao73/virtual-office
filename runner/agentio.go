// Package runner реализует контракт «раннер ↔ агент».
// Описание контракта — docs/contracts/agent-io.md.
package runner

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Dir — каталог обмена внутри workdir.
const Dir = ".agent"

// Файлы обмена.
const (
	FileTask    = "task.md"
	FileContext = "context.md"
	FileRun     = "run.json"
	FileResult  = "result.json"
	FileLog     = "run.log"
)

// Outcome — исход запуска. Других значений контракт не допускает.
type Outcome string

const (
	OutcomeDone       Outcome = "done"
	OutcomeNeedsHuman Outcome = "needs_human"
	OutcomeBlocked    Outcome = "blocked"
	OutcomeFailed     Outcome = "failed"
)

func (o Outcome) known() bool {
	switch o {
	case OutcomeDone, OutcomeNeedsHuman, OutcomeBlocked, OutcomeFailed:
		return true
	}
	return false
}

// Run — паспорт запуска, файл run.json. Его пишет раннер перед стартом агента.
type Run struct {
	RunID     string    `json:"run_id"`
	Role      string    `json:"role"`
	ConfigSHA string    `json:"config_sha"`
	StartedAt time.Time `json:"started_at"`
}

// Question — вопрос человеку. Варианты ответа необязательны: вопрос может быть открытым.
type Question struct {
	Text    string   `json:"text"`
	Options []string `json:"options,omitempty"`
}

// Result — исход запуска, файл result.json. Его пишет агент, и только агент.
type Result struct {
	Outcome   Outcome    `json:"outcome"`
	Summary   string     `json:"summary"`
	DetailsMD string     `json:"details_md,omitempty"`
	Artifacts []string   `json:"artifacts,omitempty"`
	Questions []Question `json:"questions,omitempty"`
	Blocker   string     `json:"blocker,omitempty"`
	NextOwner string     `json:"next_owner"`
}

// human и none зарезервированы, остальное — имя роли.
// Существование роли на этапе 1 не проверяется: графа переходов ещё нет.
var nextOwnerPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// ReadResult читает и проверяет result.json из каталога обмена внутри workdir.
// Любая беда — отсутствие файла, мусор в JSON, нарушение контракта — возвращается ошибкой;
// решение, что с ней делать, принимает вызывающий (см. FailedResult).
func ReadResult(workdir string) (Result, error) {
	rel := filepath.Join(Dir, FileResult)

	raw, err := os.ReadFile(filepath.Join(workdir, rel))
	if err != nil {
		return Result{}, fmt.Errorf("%s не прочитан: %w", rel, err)
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()

	var r Result
	if err := dec.Decode(&r); err != nil {
		return Result{}, fmt.Errorf("%s не разобран: %w", rel, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Result{}, fmt.Errorf("%s: после объекта есть лишнее содержимое", rel)
	}
	if err := r.Validate(); err != nil {
		return Result{}, fmt.Errorf("%s нарушает контракт: %w", rel, err)
	}
	return r, nil
}

// Validate проверяет результат по контракту. Возвращает все нарушения сразу,
// чтобы агенту не приходилось узнавать о них по одному за запуск.
func (r Result) Validate() error {
	var errs []error

	if !r.Outcome.known() {
		errs = append(errs, fmt.Errorf("outcome=%q: допустимы done, needs_human, blocked, failed", r.Outcome))
	}
	if strings.TrimSpace(r.Summary) == "" {
		errs = append(errs, errors.New("summary пуст"))
	}
	switch owner := strings.TrimSpace(r.NextOwner); {
	case owner == "":
		errs = append(errs, errors.New("next_owner пуст: допустимы human, none или имя роли"))
	case !nextOwnerPattern.MatchString(owner):
		errs = append(errs, fmt.Errorf("next_owner=%q: допустимы human, none или имя роли", r.NextOwner))
	}

	if r.Outcome == OutcomeNeedsHuman {
		if len(r.Questions) == 0 {
			errs = append(errs, errors.New("outcome=needs_human, но questions пуст"))
		}
	} else if len(r.Questions) > 0 {
		errs = append(errs, fmt.Errorf("questions заполнен при outcome=%q: вопросы только для needs_human", r.Outcome))
	}
	for i, q := range r.Questions {
		if strings.TrimSpace(q.Text) == "" {
			errs = append(errs, fmt.Errorf("questions[%d].text пуст", i))
		}
	}

	if r.Outcome == OutcomeBlocked {
		if strings.TrimSpace(r.Blocker) == "" {
			errs = append(errs, errors.New("outcome=blocked, но blocker пуст"))
		}
	} else if strings.TrimSpace(r.Blocker) != "" {
		errs = append(errs, fmt.Errorf("blocker заполнен при outcome=%q: блокер только для blocked", r.Outcome))
	}

	return errors.Join(errs...)
}

// FailedResult — синтетический исход на случай, когда агент не оставил валидного результата.
// В файл он не пишется: result.json принадлежит агенту, и следующий запуск должен отличать
// отчёт агента от домысла раннера.
func FailedResult(reason string) Result {
	return Result{
		Outcome:   OutcomeFailed,
		Summary:   reason,
		NextOwner: "human",
	}
}
