package workspace

import (
	"fmt"
	"os"
	"os/exec"
)

// clonePrefix — префикс временных каталогов одноразового клона-источника,
// который заводит CloneSource для internal/pipeline/agent.go (SandboxAgent.Run,
// бэкенд sbx). Простой os.MkdirTemp вне хозяйства Manager: клон одноразовый,
// целиком свой одному прогону и не нуждается в общей уборке репозиториев/
// worktree'ев, которой занимается остальной этот пакет.
const clonePrefix = "pipeline-clone-*"

// CloneSource заводит одноразовый, обычный (не bare, не worktree) git-клон
// ветки branch рабочей папки dir в новый временный каталог. dir может быть
// как обычным репозиторием, так и worktree'ем — git разрешает оба как
// источник клона через .git-файл/-каталог одинаково; branch — уже выкаченная
// в dir ветка задачи (то же значение, что несёт ws.Branch/req.Branch дальше
// по конвейеру).
//
// `sbx create --clone` сам отказывает и на bare-репозитории, и на worktree
// как на своём первичном пути (см. internal/backends/sbx/clone.go, doc-
// комментарий excludeFile) — клон, сделанный здесь, всегда обычный
// репозиторий со своим .git-каталогом и годится туда без исключений.
//
// Возвращает путь к клону и функцию уборки, которую вызывающий обязан звать
// при любом исходе (успех, ошибка агента, таймаут) — тем же приёмом «cleanup
// всегда», каким cloneSyncOut убирает саму песочницу (internal/backends/sbx/
// sbx.go, defer remove(name)).
//
// Ошибка означает, что клон не состоялся: временный каталог в этом случае
// уже убран этой же функцией, и вызывающему чистить нечего — path и cleanup
// оба нулевые, звать cleanup(nil) было бы паникой.
func CloneSource(dir, branch string) (string, func() error, error) {
	tmp, err := os.MkdirTemp("", clonePrefix)
	if err != nil {
		return "", nil, fmt.Errorf("временный каталог клона-источника не заведён: %w", err)
	}
	cleanup := func() error { return os.RemoveAll(tmp) }

	cmd := exec.Command("git", "clone", "--quiet", "--branch", branch, dir, tmp)
	cmd.Env = gitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = cleanup()
		return "", nil, fmt.Errorf("клон-источник (%s, ветка %s) не заведён: %w\n%s", dir, branch, err, out)
	}
	return tmp, cleanup, nil
}
