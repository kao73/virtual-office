package runner

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	validators "github.com/kao73/virtual-office/office/validators"
)

// BinDir — подкаталог бинарников: ${OFFICE_HOME}/bin у клона (ограждение
// собирается туда), <корень офиса>/bin у поставки (ограждение кладётся туда).
const BinDir = "bin"

// ValidatorName — имя бинарника ограждения. К нему приписывается платформа:
// хост и песочница разные, а лежат рядом.
const ValidatorName = "validate-result"

// validatorPkg — путь пакета команды от корня конфиг-репозитория.
const validatorPkg = "./cmd/validate-result"

// validatorsFS — встроенный набор за переменной ради тестов; nil — настоящий.
// Не «= validators.Validators»: см. payloadFS в office.go — инициализатор
// пакета удержал бы набор в validate-result.
var validatorsFS fs.FS

func validatorsOrDefault() fs.FS { return orDefault(validatorsFS, validators.Validators) }

// validatorName — имя файла ограждения под платформу: одно правило для
// поставки (имя в корне её embed-дерева) и для того, что кладётся на диск.
func validatorName(target Platform) string {
	return fmt.Sprintf("%s-%s-%s", ValidatorName, target.OS, target.Arch)
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
// и лежит в самом раннере; без него — отказ с адресом.
func EnsureValidator(o Office, target Platform) (string, error) {
	// Офис не от ResolveOffice: пустой Root значил бы «текущий каталог» — для
	// go build через cmd.Dir и для bin/ поставки одинаково, — а от этого
	// ResolveOffice и ушёл; неизвестный источник не назвал бы способ.
	if o.Root == "" || (o.Source != SourceClone && o.Source != SourcePayload) {
		return "", fmt.Errorf("офис не разрешён (root %q, source %q): ограждение под %s брать неоткуда", o.Root, o.Source, target)
	}
	name := validatorName(target)
	if o.Source == SourceClone {
		return buildValidator(o.Root, name, target)
	}
	return embeddedValidator(o.Root, name, target)
}

// noEmbeddedValidator — отказ поставки: раннер собран без ограждения под эту
// платформу. Подсунуть ограждение другой платформы нельзя — оно не запустится.
func noEmbeddedValidator(target Platform) error {
	return fmt.Errorf("раннер собран без ограждения под %s: соберите с `-tags release` после scripts/build-validators.sh или задайте %s", target, ConfigRootEnv)
}

// buildValidator — ветка клона: ${OFFICE_HOME}/bin/<name> собирается
// go build из корня клона с GOOS/GOARCH/CGO_ENABLED=0.
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
// распакованного офиса; но и не выдаётся, если это не исполняемый непустой
// файл: внутри песочницы такой чекер дал бы код 126, а хук считает его
// неблокирующим — ограждение перестало бы ограждать молча. Запись через
// временное имя и rename: два раннера, впервые готовящие прогон
// одновременно, не должны читать полуфайл.
func embeddedValidator(root, name string, target Platform) (string, error) {
	dir := filepath.Join(root, BinDir)
	path := filepath.Join(dir, name)
	switch fi, err := os.Stat(path); {
	case err == nil && fi.Mode().IsRegular() && fi.Size() > 0 && fi.Mode()&0o111 != 0:
		return path, nil
	case err == nil:
		return "", fmt.Errorf("на месте ограждения %s лежит не исполняемый файл (%s, %d байт): уберите его, раннер положит своё", path, fi.Mode(), fi.Size())
	case !errors.Is(err, fs.ErrNotExist):
		return "", fmt.Errorf("ограждение %s не проверено: %w", path, err)
	}
	raw, err := fs.ReadFile(validatorsOrDefault(), name)
	if errors.Is(err, fs.ErrNotExist) {
		return "", noEmbeddedValidator(target)
	}
	if err != nil {
		return "", fmt.Errorf("ограждение под %s не прочитано из поставки: %w", target, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("каталог ограждений не создан: %w", err)
	}
	// Права явно, как и самому ограждению: под строгим umask каталог вышел бы
	// 0700, и чекер, специально положенный 0755, остался бы недостижим для
	// песочницы — ровно того, ради чего он тут лежит.
	if err := os.Chmod(dir, 0o755); err != nil {
		return "", fmt.Errorf("права каталога ограждений не выставлены: %w", err)
	}
	if err := writeAndPublish(dir, path, raw); err != nil {
		return "", fmt.Errorf("ограждение под %s не записано: %w", target, err)
	}
	return path, nil
}

// writeAndPublish пишет содержимое во временный файл рядом, сбрасывает на
// диск, ставит 0755 и переименовывает на итоговый путь: rename на той же
// файловой системе атомарен, и читатель никогда не увидит недописанный или
// без-прав файл. Sync до rename — чтобы после сбоя питания под итоговым
// именем не оказался пустой файл, которому следующий запуск поверил бы.
// Временный файл заводится и убирается здесь же: после удачного rename
// удалять нечего, и отложенная уборка становится пустой операцией.
func writeAndPublish(dir, path string, raw []byte) error {
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer func() {
		tmp.Close()
		os.Remove(tmp.Name())
	}()
	if _, err := tmp.Write(raw); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
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
