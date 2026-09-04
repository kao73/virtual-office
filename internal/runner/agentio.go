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
	// FileBaseStatus — снимок `git status --porcelain` на старте прогона.
	// Точка отсчёта для ограждений: грязь, доставшаяся от прошлых прогонов
	// в переиспользуемой папке, — не работа этой роли, и судится дельта.
	FileBaseStatus = "base-status.txt"
)

// Outcome — исход запуска. Других значений контракт не допускает.
type Outcome string

const (
	OutcomeDone       Outcome = "done"
	OutcomeNeedsHuman Outcome = "needs_human"
	OutcomeBlocked    Outcome = "blocked"
	OutcomeFailed     Outcome = "failed"
	// OutcomeSplit — постановка описывает несколько независимых сущностей
	// или возможностей и её предлагается резать на подзадачи, а не вести
	// одним изменением Comet Native. Несёт questions (как needs_human) и
	// split.children[] — структурированное предложение разбивки; подзадачи
	// по-прежнему заводит человек, split их не создаёт (roles/analyst/role.md).
	OutcomeSplit Outcome = "split"
)

func (o Outcome) known() bool {
	switch o {
	case OutcomeDone, OutcomeNeedsHuman, OutcomeBlocked, OutcomeFailed, OutcomeSplit:
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
	// TaskKey — ключ задачи в трекере. Пуст при ручном запуске: трекера там нет.
	TaskKey string `json:"task_key,omitempty"`
	// BaseCommit — HEAD рабочей папки на момент, когда раннер собирает этот
	// паспорт, — **до** служебных коммитов, которые может сделать
	// PrepareInput (EnsureCometHookAllowPaths, internal/runner/input.go).
	// Точку отсчёта для LeftTrace вызывающий (internal/pipeline/pipeline.go,
	// cmd/run-agent/main.go) поэтому пересчитывает сам уже после
	// PrepareInput и не переписывает этим более свежим значением run.json
	// на диске — там остаётся снимок на момент подготовки, а не точка
	// сравнения для более позднего учёта. base-status.txt снимается позже
	// этого поля — последним шагом PrepareInput, — так что оба не обязаны
	// называть один и тот же HEAD. Необязателен — в репозитории без
	// коммитов его нет вовсе, и тогда сравнивать не с чем, остаётся один
	// снимок статуса.
	BaseCommit string `json:"base_commit,omitempty"`
}

// Зарезервированные значения next_owner: всё остальное трактуется как имя роли.
// Имена живут здесь, рядом с контрактом «раннер ↔ агент», а не заводятся заново
// там, где понадобились: два списка одних и тех же значений разъезжаются.
const (
	// NextOwnerHuman — дальше задачу ведёт человек.
	NextOwnerHuman = "human"
	// NextOwnerNone — передавать некому, работа закончена.
	NextOwnerNone = "none"
)

// Question — вопрос человеку.
//
// У вопроса есть идентификатор, и по нему человек отвечает одним словом:
// `Q1: b`. Без идентификатора ответ пришлось бы угадывать по тексту, а вопросов
// в одном отчёте бывает несколько.
//
// Варианты необязательны: вопрос без них — свободный. Отдельного поля «вид
// вопроса» нет и не нужно — вид следует из наличия вариантов.
type Question struct {
	// ID — метка вопроса, `Q1`, `Q2`. Даёт её агент, уникальна в результате.
	ID string `json:"id"`
	// Text — сам вопрос, одной строкой: он поедет в тикет строкой протокола,
	// и перевод строки внутри разрезал бы её пополам.
	Text    string   `json:"text"`
	Options []Option `json:"options,omitempty"`
}

// Option — вариант ответа. Идентификатор — то, что человек напишет в ответ,
// подпись — то, что он прочитает.
type Option struct {
	ID    string `json:"id"`
	Label string `json:"label"`
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
	// Split — предложение разбивки, только при outcome=split.
	Split *Split `json:"split,omitempty"`
}

// Split — структурированное предложение разбить постановку на подзадачи.
// Подзадачи заводит человек — split их не создаёт и не трогает трекер;
// это машиночитаемая замена прозе, которую пришлось бы разбирать вручную.
type Split struct {
	Children []SplitChild `json:"children"`
}

// SplitChild — одна предлагаемая подзадача.
//
// DependsOn в этой волне — чисто информационное поле для человека: он сам
// решает очерёдность заведения и запуска подзадач. Программно на него никто
// не опирается (docs/notes/analyst-task-splitting.md, «Волна 2»).
type SplitChild struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	DependsOn   []string `json:"depends_on,omitempty"`
}

// human и none зарезервированы, остальное — имя роли.
// Существование роли на этапе 1 не проверяется: графа переходов ещё нет.
var nextOwnerPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// questionIDPattern — форма метки вопроса. Строгая потому, что по ней человек
// отвечает, а раннер эти ответы разбирает: метка свободной формы («вопрос про
// кэш») не отличалась бы от прозы.
var questionIDPattern = regexp.MustCompile(`^Q\d+$`)

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
		errs = append(errs, fmt.Errorf("outcome=%q: допустимы done, needs_human, blocked, failed, split", r.Outcome))
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

	switch r.Outcome {
	case OutcomeNeedsHuman, OutcomeSplit:
		if len(r.Questions) == 0 {
			errs = append(errs, fmt.Errorf("outcome=%s, но questions пуст", r.Outcome))
		}
	default:
		if len(r.Questions) > 0 {
			errs = append(errs, fmt.Errorf("questions заполнен при outcome=%q: вопросы только для needs_human и split", r.Outcome))
		}
	}
	errs = append(errs, validateQuestions(r.Questions)...)

	if r.Outcome == OutcomeBlocked {
		if strings.TrimSpace(r.Blocker) == "" {
			errs = append(errs, errors.New("outcome=blocked, но blocker пуст"))
		}
	} else if strings.TrimSpace(r.Blocker) != "" {
		errs = append(errs, fmt.Errorf("blocker заполнен при outcome=%q: блокер только для blocked", r.Outcome))
	}

	if r.Outcome == OutcomeSplit {
		if r.Split == nil || len(r.Split.Children) == 0 {
			errs = append(errs, errors.New("outcome=split, но split.children пуст"))
		}
	} else if r.Split != nil {
		errs = append(errs, fmt.Errorf("split заполнен при outcome=%q: split только для outcome=split", r.Outcome))
	}
	if r.Split != nil {
		errs = append(errs, validateSplitChildren(r.Split.Children)...)
	}

	return errors.Join(errs...)
}

// validateQuestions проверяет вопросы к человеку.
//
// Строгость здесь не формальная: вопрос едет в тикет строкой протокола
// `Q1: текст`, а ответ человека раннер разбирает по этой же метке. Перевод
// строки внутри текста разрезал бы запись пополам, повторённая метка сделала бы
// ответ неадресуемым. Ловится это ограждением, пока агент жив и может починить,
// а не рендером, который молча сплющит.
func validateQuestions(questions []Question) []error {
	var errs []error

	seen := map[string]bool{}
	for i, q := range questions {
		switch {
		case q.ID == "":
			errs = append(errs, fmt.Errorf("questions[%d].id пуст: ожидается Q1, Q2, …", i))
		case !questionIDPattern.MatchString(q.ID):
			errs = append(errs, fmt.Errorf("questions[%d].id=%q: ожидается Q и число, например Q1", i, q.ID))
		case seen[q.ID]:
			errs = append(errs, fmt.Errorf("questions[%d].id=%q повторяется: по метке человек отвечает, и она обязана быть одна", i, q.ID))
		}
		seen[q.ID] = true

		switch {
		case strings.TrimSpace(q.Text) == "":
			errs = append(errs, fmt.Errorf("questions[%d].text пуст", i))
		case strings.ContainsAny(q.Text, "\r\n"):
			errs = append(errs, fmt.Errorf("questions[%d].text в несколько строк: вопрос едет в тикет одной строкой, подробности — в details_md", i))
		}

		options := map[string]bool{}
		for j, o := range q.Options {
			id := strings.ToLower(strings.TrimSpace(o.ID))
			switch {
			case id == "":
				errs = append(errs, fmt.Errorf("questions[%d].options[%d].id пуст: им человек отвечает", i, j))
			case strings.ContainsAny(o.ID, " \t\r\n"):
				errs = append(errs, fmt.Errorf("questions[%d].options[%d].id=%q с пробелом: человек пишет его в ответ целиком", i, j, o.ID))
			case options[id]:
				errs = append(errs, fmt.Errorf("questions[%d].options[%d].id=%q повторяется: выбор станет неоднозначным", i, j, o.ID))
			}
			options[id] = true

			switch {
			case strings.TrimSpace(o.Label) == "":
				errs = append(errs, fmt.Errorf("questions[%d].options[%d].label пуст: человеку нечего читать", i, j))
			case strings.ContainsAny(o.Label, "\r\n"):
				errs = append(errs, fmt.Errorf("questions[%d].options[%d].label в несколько строк: вариант печатается одной", i, j))
			}
		}
	}
	return errs
}

// validateSplitChildren проверяет граф предложенных подзадач: уникальность
// id, ссылки depends_on только на существующие в этом же списке id, без
// циклов. depends_on в этой волне не исполняется программно (см. SplitChild),
// но граф уже присутствует в контракте и обязан быть валиден с самого начала —
// не проверенный сейчас цикл превратился бы в неотлаживаемую ловушку, когда
// волна 2 начнёт на него опираться.
func validateSplitChildren(children []SplitChild) []error {
	var errs []error

	ids := make(map[string]bool, len(children))
	for i, c := range children {
		// id едет в тот же буллет-заголовок, что title (SplitBlock:
		// "- **id** — title"), и человек ссылается на него в depends_on —
		// тем же приёмом, что options[].id, запрещающий даже пробел.
		switch {
		case strings.TrimSpace(c.ID) == "":
			errs = append(errs, fmt.Errorf("split.children[%d].id пуст", i))
		case strings.ContainsAny(c.ID, " \t\r\n"):
			errs = append(errs, fmt.Errorf("split.children[%d].id=%q с пробелом: на него ссылаются depends_on", i, c.ID))
		case ids[c.ID]:
			errs = append(errs, fmt.Errorf("split.children[%d].id=%q повторяется", i, c.ID))
		}
		ids[c.ID] = true

		// title/description едут в тикет одной строкой каждый (SplitBlock,
		// internal/tracker/report.go) — тот же буллет-список, что ломает
		// перевод строки у questions[].text/options[].label (validateQuestions
		// выше), и то же правило: длинное — в details_md, не сюда.
		switch {
		case strings.TrimSpace(c.Title) == "":
			errs = append(errs, fmt.Errorf("split.children[%d].title пуст", i))
		case strings.ContainsAny(c.Title, "\r\n"):
			errs = append(errs, fmt.Errorf("split.children[%d].title в несколько строк: заголовок едет в тикет одной строкой", i))
		}
		switch {
		case strings.TrimSpace(c.Description) == "":
			errs = append(errs, fmt.Errorf("split.children[%d].description пуст", i))
		case strings.ContainsAny(c.Description, "\r\n"):
			errs = append(errs, fmt.Errorf("split.children[%d].description в несколько строк: описание едет в тикет одной строкой, подробности — в details_md", i))
		}
	}

	for i, c := range children {
		for _, dep := range c.DependsOn {
			if !ids[dep] {
				errs = append(errs, fmt.Errorf("split.children[%d].depends_on=%q: такого id в списке нет", i, dep))
			}
		}
	}

	if cycle := findSplitCycle(children); cycle != "" {
		errs = append(errs, fmt.Errorf("split.children образуют цикл зависимостей: %s", cycle))
	}

	return errs
}

// findSplitCycle ищет цикл в depends_on обходом в глубину (цвета белый/серый/
// чёрный) и возвращает его как путь узлов через " -> ", либо "", если циклов
// нет. Висячие ссылки (id вне списка) здесь не считаются рёбрами — их уже
// поймала отдельная проверка в validateSplitChildren, и обрабатывать их же
// как потенциальный цикл значило бы дублировать диагностику одной и той же
// причины под двумя разными сообщениями.
func findSplitCycle(children []SplitChild) string {
	edges := make(map[string][]string, len(children))
	for _, c := range children {
		edges[c.ID] = c.DependsOn
	}

	const (
		white = iota
		gray
		black
	)
	color := make(map[string]int, len(children))
	var path []string

	var dfs func(id string) string
	dfs = func(id string) string {
		color[id] = gray
		path = append(path, id)
		for _, dep := range edges[id] {
			if _, exists := edges[dep]; !exists {
				continue // висячая ссылка — не ребро цикла, отдельная проверка
			}
			switch color[dep] {
			case gray:
				start := 0
				for i, n := range path {
					if n == dep {
						start = i
						break
					}
				}
				return strings.Join(append(append([]string{}, path[start:]...), dep), " -> ")
			case white:
				if cyc := dfs(dep); cyc != "" {
					return cyc
				}
			}
		}
		color[id] = black
		path = path[:len(path)-1]
		return ""
	}

	for _, c := range children {
		if color[c.ID] == white {
			if cyc := dfs(c.ID); cyc != "" {
				return cyc
			}
		}
	}
	return ""
}

// ResultSpec — спецификация файла результата для системного промпта агента.
// Живёт рядом с типом Result, чтобы текст и проверка не разъезжались.
func ResultSpec(resultFile string) string {
	return `## Файл результата

Завершая работу, запиши ` + "`" + resultFile + "`" + ` — один JSON-объект и ничего кроме него:

    {
      "outcome": "done | needs_human | blocked | failed | split",
      "summary": "суть в 1-3 предложениях",
      "details_md": "необязательно: подробности в markdown",
      "artifacts": ["необязательно: пути, commit SHA, имя ветки"],
      "questions": [
        {"id": "Q1", "text": "вопрос", "options": [{"id": "a", "label": "вариант"}, {"id": "b", "label": "другой"}]},
        {"id": "Q2", "text": "вопрос без вариантов"}
      ],
      "blocker": "кто или что блокирует",
      "next_owner": "human | имя роли | none",
      "split": {
        "children": [
          {"id": "short-slug", "title": "заголовок будущего тикета", "description": "текст будущего тикета", "depends_on": []},
          {"id": "another-slug", "title": "...", "description": "...", "depends_on": ["short-slug"]}
        ]
      }
    }

- ` + "`outcome`, `summary`, `next_owner`" + ` обязательны всегда.
- ` + "`questions`" + ` — только при ` + "`outcome=needs_human`" + ` или ` + "`outcome=split`" + `, непустым списком.
  ` + "`id`" + ` — метка вида ` + "`Q1`" + `, своя у каждого вопроса: по ней человек и отвечает
  (` + "`Q1: b`" + `). ` + "`options`" + ` необязателен — вопрос без вариантов свободный;
  ` + "`id`" + ` варианта человек пишет в ответ целиком, поэтому он короткий и без пробелов.
  Текст вопроса и подписи вариантов — **одной строкой**: они едут в тикет строками
  протокола. Длинное объяснение — в ` + "`details_md`" + `.
- ` + "`blocker`" + ` — только при ` + "`outcome=blocked`" + `.
- ` + "`split`" + ` — только при ` + "`outcome=split`" + `, с непустым ` + "`children`" + `.
  Каждый ребёнок — будущий тикет, который заведёт человек, не ты: ` + "`id`" + ` —
  свой короткий ключ без пробелов (не ключ трекера — его ещё нет), ` + "`title`" + ` — заголовок,
  ` + "`description`" + ` — суть в 1-3 предложения, как верхнеуровневый ` + "`summary`" + `.
  Оба — **одной строкой каждый**, как текст вопроса; подробный план для
  подзадачи сюда не входит. ` + "`depends_on`" + ` — список ` + "`id`" + ` других детей
  из этого же списка, от которых этот зависит (не циклически, и только на
  существующие ` + "`id`" + `).
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
