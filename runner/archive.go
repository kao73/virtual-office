package runner

import (
	"fmt"
	"os"
	"path/filepath"
)

// HomeEnv — переменная, задающая каталог хозяйства раннера: архив прогонов,
// собранные бинарники ограждений, позже — mock-трекер и bare-клоны.
const HomeEnv = "OFFICE_HOME"

// RunsDir — подкаталог архива прогонов внутри хозяйства раннера.
const RunsDir = "runs"

// Home — каталог хозяйства раннера. Без OFFICE_HOME — предсказуемое место
// в домашнем каталоге: раннер и оператор должны искать прогон там же.
func Home() (string, error) {
	if home := os.Getenv(HomeEnv); home != "" {
		return home, nil
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("каталог офиса не определён: %s не задан и домашний каталог неизвестен: %w", HomeEnv, err)
	}
	return filepath.Join(userHome, ".office"), nil
}

// Archive уносит каталог обмена в архив прогонов и возвращает путь к копии.
//
// Каталог обмена эфемерен: он лежит в рабочей папке, а на этапе 2 рабочей папкой
// становится worktree, который удаляют. Без копии постановка, контекст, лог и
// результат исчезают вместе с ним, и разбирать прогон становится не по чему.
func Archive(workdir, runID string) (string, error) {
	src := filepath.Join(workdir, Dir)
	fi, err := os.Stat(src)
	switch {
	case err != nil:
		return "", fmt.Errorf("каталог обмена не заархивирован: %w", err)
	case !fi.IsDir():
		return "", fmt.Errorf("%s не каталог: архивировать нечего", src)
	}

	home, err := Home()
	if err != nil {
		return "", err
	}
	dest := filepath.Join(home, RunsDir, runID)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", fmt.Errorf("каталог архива не создан: %w", err)
	}
	if err := os.CopyFS(dest, os.DirFS(src)); err != nil {
		return "", fmt.Errorf("прогон %s не заархивирован: %w", runID, err)
	}
	return dest, nil
}
