package workspace

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// ErrBaseMissing — базовой ветки прохода (auto_merge.target_branch, а без
// него default_branch) нет в клоне. В отличие от прочих ошибок MergeCheck
// (сбой git, беда обвязки), это конфигурационная опечатка, которая сама
// не пройдёт: тикет должен об этом сказать, а не только лог раннера
// (внешнее ревью, pr-converge round 2).
var ErrBaseMissing = errors.New("базовой ветки прохода нет в клоне")

// Merge — что git думает о слиянии ветки задачи с базовой веткой.
type Merge struct {
	// Commits — коммитов ветки, которых нет в базовой. Ноль означает «сливать
	// нечего»: ветки нет вовсе или её работа уже в базе.
	Commits int
	// Conflict — слияние конфликтует.
	Conflict bool
}

// Empty — нечего сливать.
func (m Merge) Empty() bool { return m.Commits == 0 }

// MergeCheck считает слияемость ветки задачи **локально**, по bare-клону.
//
// Локально — не для экономии запроса. Так проверка не зависит ни от сети, ни
// от forge: проект без forge получает ту же проверку, а второй forge не обязан
// уметь отвечать про слияемость — интерфейс от этого не растёт.
//
// Обе стороны берутся из origin, а не из локальных веток. Локальной ветки задачи
// на этой машине может не быть вовсе — worktree живёт до слияния, но клон
// переживает и его, — а origin клон освежает на каждом обращении.
func (m *Manager) MergeCheck(repo, branch, base string) (Merge, error) {
	head, err := refExists(repo, "refs/remotes/origin/"+branch)
	if err != nil {
		return Merge{}, err
	}
	if !head {
		// Ветки в origin нет: агент ничего не закоммитил или пуш не удался.
		// Это не конфликт, а «сливать нечего».
		return Merge{}, nil
	}
	if exists, err := refExists(repo, "refs/remotes/origin/"+base); err != nil {
		return Merge{}, err
	} else if !exists {
		return Merge{}, fmt.Errorf("%w: origin/%s — не с чем сливать; офис её не создаёт "+
			"(auto_merge.target_branch заводят руками)", ErrBaseMissing, base)
	}

	out, err := git(repo, "rev-list", "--count", "origin/"+base+".."+"origin/"+branch)
	if err != nil {
		return Merge{}, err
	}
	commits, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return Merge{}, fmt.Errorf("число коммитов ветки %s не разобрано: %w", branch, err)
	}
	if commits == 0 {
		return Merge{}, nil
	}

	// merge-tree --write-tree сливает в память и ничего не трогает в рабочем
	// дереве — его у bare-клона и нет. Конфликт он показывает кодом возврата 1;
	// пробное слияние с worktree и откатом стоило бы дороже и оставляло бы следы.
	cmd := exec.Command("git", "-C", repo, "merge-tree", "--write-tree", "origin/"+base, "origin/"+branch)
	cmd.Env = gitEnv()
	switch out, err := cmd.CombinedOutput(); {
	case err == nil:
		return Merge{Commits: commits}, nil
	case isExitCode(err, 1):
		return Merge{Commits: commits, Conflict: true}, nil
	default:
		return Merge{}, fmt.Errorf("слияние %s с %s не проверено: %w\n%s", branch, base, err, out)
	}
}

// BaseAdvanced отвечает, обогнала ли база ветку задачи — есть ли в base
// коммиты, которых ветка задачи ещё не содержит. Отдельно от MergeCheck:
// это не про текстовый конфликт, а про то, устарел ли контекст, в котором
// implementer писал, а reviewer смотрел diff, — база могла уйти вперёд и
// без единого маркера конфликта.
func (m *Manager) BaseAdvanced(repo, branch, base string) (bool, error) {
	cmd := exec.Command("git", "-C", repo, "merge-base", "--is-ancestor",
		"origin/"+base, "origin/"+branch)
	cmd.Env = gitEnv()
	// CombinedOutput, а не Run, — как у MergeCheck выше и по более веской
	// причине: своей проверки «а есть ли такой ref» у BaseAdvanced нет, и
	// пропавшая или переименованная база приходит прямо сюда. Без вывода git
	// в ошибке остаётся голое `exit status 128`, по которому не видно даже,
	// какого ref не хватило.
	switch out, err := cmd.CombinedOutput(); {
	case err == nil:
		return false, nil // база уже целиком в предках ветки задачи
	case isExitCode(err, 1):
		return true, nil
	default:
		return false, fmt.Errorf("продвижение %s относительно %s не проверено: %w\n%s",
			base, branch, err, out)
	}
}
