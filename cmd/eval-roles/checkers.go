package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/kao73/virtual-office/internal/runner"
)

// checkers сопоставляет CheckSpec.Kind с её Checker'ом. "llm_judge" в карте
// намеренно нет: промах диспетчера — это и есть исполнение требования спеки
// «Unimplemented check kind» (см. dispatchCheck в run.go, Task 11).
var checkers = map[string]Checker{
	"outcome":       outcomeChecker{},
	"diff_scope":    diffScopeChecker{},
	"fixture_tests": fixtureTestsChecker{timeout: 5 * time.Minute},
}

// outcomeChecker сверяет Result.Outcome со spec.Expect, а когда expect —
// needs_human и задан questions_not_empty — ещё и что Questions не пуст.
// Когда задан spec.NextOwner, сверяет и его с Result.NextOwner: это
// обязательное, типизированное поле роли (см. roles/*/role.md, «Выход»), и
// оно надёжнее любого разбора свободного текста summary/details_md —
// тот способ спутать «нашёл дефект» с «одобрил» уже подвёл один раз
// (evals/reviewer/capability-spot-defect).
type outcomeChecker struct{}

func (outcomeChecker) Run(ctx CheckContext) CheckResult {
	want := runner.Outcome(ctx.Spec.Expect)
	got := ctx.Result.Outcome
	if got != want {
		detail := fmt.Sprintf("outcome=%q, expected %q", got, want)
		if ctx.Result.Summary != "" {
			detail += fmt.Sprintf(" (summary: %s)", ctx.Result.Summary)
		}
		return CheckResult{Pass: false, Detail: detail}
	}
	if want == runner.OutcomeNeedsHuman && ctx.Spec.QuestionsNotEmpty && len(ctx.Result.Questions) == 0 {
		return CheckResult{Pass: false, Detail: "outcome=needs_human but questions is empty"}
	}
	if ctx.Spec.NextOwner != "" && ctx.Result.NextOwner != ctx.Spec.NextOwner {
		return CheckResult{Pass: false, Detail: fmt.Sprintf("next_owner=%q, expected %q", ctx.Result.NextOwner, ctx.Spec.NextOwner)}
	}
	return CheckResult{Pass: true, Detail: fmt.Sprintf("outcome=%q as expected", got)}
}

// diffScopeChecker проваливается, если хоть один путь, изменившийся с
// InitialCommit, не подпадает ни под один glob из spec.Allow. Пустой diff
// проходит всегда, даже против пустого Allow.
type diffScopeChecker struct{}

func (diffScopeChecker) Run(ctx CheckContext) CheckResult {
	paths, err := changedPaths(ctx.FixtureDir, ctx.InitialCommit)
	if err != nil {
		return CheckResult{Err: err}
	}

	var outside []string
	for _, path := range paths {
		matched, err := matchesAny(path, ctx.Spec.Allow)
		if err != nil {
			return CheckResult{Err: err}
		}
		if !matched {
			outside = append(outside, path)
		}
	}
	if len(outside) > 0 {
		return CheckResult{Pass: false, Detail: "changed outside allow: " + strings.Join(outside, ", ")}
	}
	return CheckResult{Pass: true, Detail: "all changed paths within allow"}
}

// changedPaths возвращает все пути, отличающиеся от initialCommit: отслеживаемые
// файлы, изменённые или добавленные (закоммиченные или нет, через `git diff`),
// плюс неотслеживаемые файлы, оставленные ролью без git add (через `git
// ls-files --others`). Один `git diff` не видит вторую группу — а именно
// оставленный без добавления неотслеживаемый файл и есть та запись вне
// области, которую эта проверка призвана ловить.
func changedPaths(fixtureDir, initialCommit string) ([]string, error) {
	// .Output() (только stdout), не .CombinedOutput(): CombinedOutput сливает
	// stderr в те же байты, которые мы потом разбираем как список путей, так
	// что любая болтовня git в stderr (предупреждения о локали, заметки про
	// core.autocrlf, …) попала бы в вывод как призрачный «изменённый путь» и
	// провалила бы diff_scope-проверку на машине, где git решил предупредить.
	// Сообщения об ошибках ниже вместо этого достают stderr из
	// *exec.ExitError.Stderr.
	// core.quotePath=false: без него git C-квотирует любой не-ASCII байт в
	// пути (например, кириллические имена файлов — собственная конвенция
	// проекта для документов) в восьмеричноэкранированную строку в кавычках,
	// которая никогда не совпадёт ни с одним glob'ом из allow.
	tracked, err := exec.Command("git", "-C", fixtureDir, "-c", "core.quotePath=false", "diff", "--name-only", initialCommit).Output()
	if err != nil {
		return nil, fmt.Errorf("git diff не выполнен: %w: %s", err, exitStderr(err))
	}
	untracked, err := exec.Command("git", "-C", fixtureDir, "-c", "core.quotePath=false", "ls-files", "--others", "--exclude-standard").Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files не выполнен: %w: %s", err, exitStderr(err))
	}

	seen := map[string]bool{}
	var paths []string
	for _, raw := range [][]byte{tracked, untracked} {
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			if line == "" || seen[line] {
				continue
			}
			seen[line] = true
			paths = append(paths, line)
		}
	}
	sort.Strings(paths)
	return paths, nil
}

// exitStderr достаёт текст stderr из неудачи команды для сообщений об ошибке.
// exec.Command(...).Output() оставляет ExitError.Stderr заполненным, пока
// поле Stderr самой команды осталось nil (здесь это так).
func exitStderr(err error) []byte {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Stderr
	}
	return nil
}

func matchesAny(path string, allow []string) (bool, error) {
	for _, pat := range allow {
		ok, err := globMatch(pat, path)
		if err != nil {
			return false, fmt.Errorf("allow-паттерн %q: %w", pat, err)
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// fixtureTestsChecker запускает spec.Command внутри FixtureDir через `sh -c`
// и проходит, если тот выходит с 0 в пределах timeout. Таймаут — это обычный
// провал проверки («timed out after ...»), не инфраструктурный Err.
type fixtureTestsChecker struct {
	timeout time.Duration
}

func (f fixtureTestsChecker) Run(ctx CheckContext) CheckResult {
	cmdCtx, cancel := context.WithTimeout(context.Background(), f.timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "sh", "-c", ctx.Spec.Command)
	cmd.Dir = ctx.FixtureDir
	out, err := cmd.CombinedOutput()

	if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
		return CheckResult{Pass: false, Detail: fmt.Sprintf("timed out after %s", f.timeout)}
	}
	if err != nil {
		// Вина роли — только если команда запустилась и вышла с ненулевым
		// кодом. Всё остальное (нет интерпретатора, FixtureDir не читается, …)
		// — поломка окружения самого харнесса, а не провал проверки.
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return CheckResult{Err: fmt.Errorf("command %q не запущена: %w", ctx.Spec.Command, err)}
		}
		return CheckResult{Pass: false, Detail: fmt.Sprintf("command %q failed: %v\n%s", ctx.Spec.Command, err, out)}
	}
	return CheckResult{Pass: true, Detail: fmt.Sprintf("command %q passed", ctx.Spec.Command)}
}
