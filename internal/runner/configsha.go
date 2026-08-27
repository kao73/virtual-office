package runner

import (
	"fmt"
	"os/exec"
	"strings"
)

// ConfigSHA — отпечаток конфигурации, ушедшей агенту.
//
// Незакоммиченная правка в роли, промпте или ограждении меняет то, что получит
// агент, а SHA не меняет. Без пометки паспорт прогона утверждал бы, что агенту
// достался коммит, которого агент не видел, — и разбор «после какого коммита роль
// стала косячить» опёрся бы на враньё. Пометка не восстанавливает правку, а лишь
// запрещает доверять SHA; сам материал прогона хранит архив.
func ConfigSHA(repo string) (string, error) {
	out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("не прочитан commit конфигурации в %s: %w", repo, err)
	}
	sha := strings.TrimSpace(string(out))

	// Неотслеживаемые файлы считаются наравне с правками: они тоже не описаны SHA.
	status, err := exec.Command("git", "-C", repo, "status", "--porcelain").Output()
	if err != nil {
		return "", fmt.Errorf("не прочитано состояние конфигурации в %s: %w", repo, err)
	}
	if strings.TrimSpace(string(status)) != "" {
		sha += "-dirty"
	}
	return sha, nil
}
