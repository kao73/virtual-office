package runner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// BinDir — подкаталог с собранными бинарниками внутри хозяйства раннера.
const BinDir = "bin"

// ValidatorName — имя бинарника ограждения. К нему приписывается платформа:
// хост и песочница разные, а лежат рядом.
const ValidatorName = "validate-result"

// validatorPkg — путь пакета команды от корня конфиг-репозитория.
const validatorPkg = "./cmd/validate-result"

// Platform — где будет исполняться агент, а с ним и ограждение. На бэкенде local
// это хост, в песочнице — Linux. Платформу задаёт бэкенд: гадать по хосту нельзя,
// собранный не под ту систему бинарник просто не запустится.
type Platform struct {
	OS   string
	Arch string
}

// HostPlatform — платформа машины, на которой работает раннер.
func HostPlatform() Platform {
	return Platform{OS: runtime.GOOS, Arch: runtime.GOARCH}
}

func (p Platform) String() string { return p.OS + "/" + p.Arch }

// EnsureValidator собирает бинарник ограждения под нужную платформу и возвращает
// путь к нему.
//
// Собирается он каждый раз заново, а не берётся готовым: бинарник — это слепок
// контракта на момент сборки, и оставленный от прошлой версии проверял бы не то,
// что проверяет раннер. Ровно эта развилка и стоила этапу 1 потерянного прогона.
// Повторная сборка почти бесплатна — её кэширует сам go.
func EnsureValidator(configRoot string, target Platform) (string, error) {
	home, err := Home()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, BinDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("каталог бинарников не создан: %w", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("%s-%s-%s", ValidatorName, target.OS, target.Arch))

	// go build отказывается писать поверх файла, который не выглядит бинарником
	// («already exists and is not an object file»), и мусор на этом пути заклинил бы
	// сборку навсегда. Убираем сами.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("прошлое ограждение не убрано: %w", err)
	}

	// CGO_ENABLED=0 — бинарник должен быть статическим: в песочнице чужая libc.
	cmd := exec.Command("go", "build", "-o", path, validatorPkg)
	cmd.Dir = configRoot
	cmd.Env = append(os.Environ(), "GOOS="+target.OS, "GOARCH="+target.Arch, "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("ограждение под %s не собрано: %w: %s", target, err, out)
	}
	return path, nil
}
