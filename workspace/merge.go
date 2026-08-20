package workspace

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Merge — что git думает о слиянии ветки задачи с веткой по умолчанию.
type Merge struct {
	// Commits — коммитов ветки, которых нет в ветке по умолчанию. Ноль означает
	// «сливать нечего»: ветки нет вовсе или её работа уже в основной ветке.
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
// уметь отвечать про слияемость, и интерфейс у него остаётся из двух методов.
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
		return Merge{}, fmt.Errorf("в клоне нет ветки по умолчанию origin/%s: не с чем сливать", base)
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
