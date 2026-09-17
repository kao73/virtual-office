package runner

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	payload "github.com/kao73/virtual-office"
)

// BinDir — подкаталог с собранными бинарниками внутри хозяйства раннера.
const BinDir = "bin"

// ValidatorName — имя бинарника ограждения. К нему приписывается платформа:
// хост и песочница разные, а лежат рядом.
const ValidatorName = "validate-result"

// validatorPkg — путь пакета команды от корня конфиг-репозитория.
const validatorPkg = "./cmd/validate-result"

// validatorsDir — где внутри поставки лежат ограждения (validators_*.go в корне).
const validatorsDir = "payload/validators"

// validatorsFS — встроенный набор за переменной ради тестов; nil — настоящий.
// Не «= payload.Validators»: см. payloadFS в office.go — инициализатор
// пакета удержал бы набор в validate-result.
var validatorsFS fs.FS

func validatorsOrDefault() fs.FS {
	if validatorsFS != nil {
		return validatorsFS
	}
	return payload.Validators
}

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

// EnsureValidator отдаёт путь к бинарнику ограждения под платформу бэкенда.
//
// Клон: собирается заново каждый раз — бинарник есть слепок контракта на
// момент сборки, и оставленный от прошлой версии проверял бы не то, что
// проверяет раннер (этап 1 потерял на этом прогон); повторная сборка почти
// бесплатна, её кэширует сам go. Поставка: ограждение собрано при релизе
// и лежит в самом раннере (Task 7); без него — отказ с адресом.
func EnsureValidator(o Office, target Platform) (string, error) {
	name := fmt.Sprintf("%s-%s-%s", ValidatorName, target.OS, target.Arch)
	switch o.Source {
	case SourceClone:
		return buildValidator(o.Root, name, target)
	case SourcePayload:
		return embeddedValidator(o.Root, name, target)
	default:
		return "", fmt.Errorf("офис без источника: неоткуда взять ограждение под %s", target)
	}
}

// noEmbeddedValidator — отказ поставки: раннер собран без ограждения под эту
// платформу. Подсунуть ограждение другой платформы нельзя — оно не запустится.
func noEmbeddedValidator(target Platform) error {
	return fmt.Errorf("раннер собран без ограждения под %s: соберите с `-tags release` или задайте %s", target, ConfigRootEnv)
}

// buildValidator — сегодняшний код EnsureValidator: ${OFFICE_HOME}/bin/<name>
// собирается go build из корня клона с GOOS/GOARCH/CGO_ENABLED=0.
func buildValidator(configRoot, name string, target Platform) (string, error) {
	home, err := Home()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, BinDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("каталог бинарников не создан: %w", err)
	}
	path := filepath.Join(dir, name)

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

// embeddedValidator кладёт ограждение из поставки в <root>/bin/<name> один раз.
//
// Лежит оно рядом с офисом своей версии, а не в ${OFFICE_HOME}/bin: две
// версии не должны делить ограждение, а версия офиса и есть версия
// ограждения. Уже лежащее не переписывается — тот же контракт, что у
// распакованного офиса. Запись через временное имя и rename: два раннера,
// впервые готовящие прогон одновременно, не должны читать полуфайл.
func embeddedValidator(root, name string, target Platform) (string, error) {
	dir := filepath.Join(root, BinDir)
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	raw, err := fs.ReadFile(validatorsOrDefault(), validatorsDir+"/"+name)
	if errors.Is(err, fs.ErrNotExist) {
		return "", noEmbeddedValidator(target)
	}
	if err != nil {
		return "", fmt.Errorf("ограждение под %s не прочитано из поставки: %w", target, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("каталог ограждений не создан: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "."+name+"-*")
	if err != nil {
		return "", fmt.Errorf("ограждение не записано: %w", err)
	}
	if err := writeAndPublish(tmp, raw, path); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("ограждение под %s не записано: %w", target, err)
	}
	return path, nil
}

// writeAndPublish пишет содержимое во временный файл, закрывает, ставит 0755
// и переименовывает на итоговый путь: rename на той же файловой системе
// атомарен, и читатель никогда не увидит недописанный или без-прав файл.
func writeAndPublish(tmp *os.File, raw []byte, path string) error {
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
