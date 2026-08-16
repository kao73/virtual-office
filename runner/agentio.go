// Package runner реализует контракт «раннер ↔ агент».
// Описание контракта — docs/contracts/agent-io.md.
package runner

import (
	"bytes"
	"crypto/rand"
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
	return ReadResultFile(filepath.Join(workdir, Dir, FileResult))
}

// ReadResultFile — то же по явному пути к файлу. Этой формой пользуется ограждение:
// команду ему собирает адаптер, и рабочей папки в ней нет — только путь к результату.
//
// Разбор у ограждения и у раннера обязан быть один и тот же. Пока ограждение
// проверяло лишь наличие поля outcome, агент успевал завершиться с результатом,
// который раннер потом отвергал, и сделанная работа уходила в failed.
func ReadResultFile(path string) (Result, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("%s не прочитан: %w", path, err)
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()

	var r Result
	if err := dec.Decode(&r); err != nil {
		return Result{}, fmt.Errorf("%s не разобран: %w", path, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Result{}, fmt.Errorf("%s: после объекта есть лишнее содержимое", path)
	}
	if err := r.Validate(); err != nil {
		return Result{}, fmt.Errorf("%s нарушает контракт: %w", path, err)
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

// ResultSpec — спецификация файла результата для системного промпта агента.
// Живёт рядом с типом Result, чтобы текст и проверка не разъезжались.
func ResultSpec(resultFile string) string {
	return `## Файл результата

Завершая работу, запиши ` + "`" + resultFile + "`" + ` — один JSON-объект и ничего кроме него:

    {
      "outcome": "done | needs_human | blocked | failed",
      "summary": "суть в 1-3 предложениях",
      "details_md": "необязательно: подробности в markdown",
      "artifacts": ["необязательно: пути, commit SHA, имя ветки"],
      "questions": [{"text": "вопрос", "options": ["вариант"]}],
      "blocker": "кто или что блокирует",
      "next_owner": "human | имя роли | none"
    }

- ` + "`outcome`, `summary`, `next_owner`" + ` обязательны всегда.
- ` + "`questions`" + ` — только при ` + "`outcome=needs_human`" + `, непустым списком; ` + "`options`" + ` необязателен.
- ` + "`blocker`" + ` — только при ` + "`outcome=blocked`" + `.
- Полей сверх перечисленных быть не должно: файл с лишним полем считается невалидным,
  и запуск засчитывается как провалившийся.
`
}

// ResultAdvice — что делать агенту, упёршемуся в ограждение. Живёт рядом
// со спецификацией и проверкой: упереться и не знать выхода — прямой путь
// к циклу до предела шагов.
const ResultAdvice = "Перезапиши файл по схеме из системного промпта. Завершиться без валидного результата нельзя, " +
	"но выход есть всегда: если задача не вышла — outcome=failed с описанием того, что уже проверено; " +
	"если мешает внешнее обстоятельство — outcome=blocked с полем blocker."

// NewRunID выдаёт идентификатор запуска — UUID версии 4. Именно UUID потому,
// что этим же значением помечается сессия агента.
func NewRunID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("не сгенерирован run_id: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // версия 4
	b[8] = (b[8] & 0x3f) | 0x80 // вариант RFC 4122
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
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
