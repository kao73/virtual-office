package runner

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"

	payload "github.com/kao73/virtual-office"
	"github.com/kao73/virtual-office/internal/office"
)

// ConfigRootEnv — переключатель разработчика: корень клона, из которого брать
// офис вместо поставки в бинарнике. Его выставляют обёртки bin/*.
const ConfigRootEnv = "OFFICE_CONFIG_ROOT"

// OfficeDir — подкаталог хозяйства с распакованными версиями офиса:
// ${OFFICE_HOME}/office/<версия>/. Старые версии не убираются: чужой
// работающий раннер мог быть собран из любой из них.
const OfficeDir = "office"

// Source — откуда взят офис.
type Source string

const (
	SourceClone   Source = "clone"   // OFFICE_CONFIG_ROOT: клон, ограждение собирает go build
	SourcePayload Source = "payload" // поставка бинарника, распакованная под ${OFFICE_HOME}/office/
)

// Office — где лежит офис и чем подписывать его прогоны.
type Office struct {
	// Root — каталог с roles/, skills/, hooks/, workflow.yaml, budgets.yaml и
	// китом песочницы: sbx-kits/ в поставке, bootstrap/sbx-kits/ в клоне.
	Root string
	// Identity — версия релиза (v0.7.0) или commit, с -dirty при незакоммиченных
	// правках. У грязных сборок одного commit она одна; различает их Root.
	Identity string
	Source   Source
}

// Describe — строка для раскладки конфигурации: что за офис и где он.
func (o Office) Describe() string {
	if o.Source == SourceClone {
		return fmt.Sprintf("клон %s (git %s)", o.Root, o.Identity)
	}
	return fmt.Sprintf("%s → %s", o.Identity, o.Root)
}

// Resolve — параметры ResolveOffice.
type Resolve struct {
	// Unpack — распаковать поставку, если её каталога ещё нет. Всё, что
	// читает роли, просит распаковку; `runner version` только считает путь.
	Unpack bool
}

// readBuildInfo — debug.ReadBuildInfo за переменной: тесты подставляют
// build info с нужным vcs.revision.
var readBuildInfo = debug.ReadBuildInfo

// payloadFS — поставка за переменной ради тестов; nil означает настоящую.
// Нарочно не «= payload.Payload»: инициализатор пакета удержал бы 13 МБ
// поставки в каждом бинарнике, импортирующем runner, включая ограждение
// validate-result. Функция ниже линкуется только туда, где её зовут.
var payloadFS fs.FS

func payloadOrDefault() fs.FS {
	if payloadFS != nil {
		return payloadFS
	}
	return payload.Payload
}

// errNoIdentity — четвёртая ветка: подписывать прогоны нечем.
var errNoIdentity = errors.New("раннер собран без личности: нет ни версии релиза, ни commit в build info. " +
	"Соберите его из git-клона (go build без -buildvcs=false) или задайте " + ConfigRootEnv)

// ResolveOffice отвечает, где офис и как его звать. Четыре ветки, первая
// подошедшая выигрывает: OFFICE_CONFIG_ROOT — клон и его commit; версия
// из ldflags — релиз; vcs.revision из build info — сборка из клона без
// обёртки; иначе отказ. Незакоммиченные правки в дереве сборки — с версией
// или без — дают -dirty (payloadIdentity). Текущий каталог офисом не
// считается никогда.
func ResolveOffice(opts Resolve) (Office, error) {
	if root := os.Getenv(ConfigRootEnv); root != "" {
		identity, err := ConfigSHA(root)
		if err != nil {
			return Office{}, err
		}
		return Office{Root: root, Identity: identity, Source: SourceClone}, nil
	}
	identity, dir, err := payloadIdentity()
	if err != nil {
		return Office{}, err
	}
	home, err := Home()
	if err != nil {
		return Office{}, err
	}
	officeDir := filepath.Join(home, OfficeDir)
	o := Office{Root: filepath.Join(officeDir, dir), Identity: identity, Source: SourcePayload}
	if !opts.Unpack {
		return o, nil
	}
	switch fi, err := os.Stat(o.Root); {
	case err == nil && fi.IsDir():
		return o, nil
	case err == nil:
		return Office{}, fmt.Errorf("на месте каталога офиса %s лежит файл: уберите его, раннер распакует поставку", o.Root)
	case !errors.Is(err, fs.ErrNotExist):
		return Office{}, fmt.Errorf("каталог офиса не проверен: %w", err)
	}
	if _, err := office.Unpack(payloadOrDefault(), officeDir, dir); err != nil {
		return Office{}, err
	}
	return o, nil
}

// payloadIdentity — личность поставки и имя её каталога. Версия из ldflags
// выигрывает у commit из build info: релиз собирается в клоне и несёт оба.
// Грязная сборка — с версией или без — получает -dirty в личность и хеш
// содержимого в имя каталога: две сборки одного commit могут нести разные
// роли, а распакованная версия не переписывается (D3). Снапшот GoReleaser
// на незакоммиченном дереве — ровно этот случай: тот же HEAD, та же версия,
// другое содержимое.
func payloadIdentity() (identity, dir string, err error) {
	rev, modified := vcsState()
	if payload.Version != "" {
		if !modified {
			return payload.Version, payload.Version, nil
		}
		return dirtyIdentity(payload.Version, payload.Version)
	}
	if rev == "" {
		return "", "", errNoIdentity
	}
	short := rev
	if len(short) > 12 {
		short = short[:12]
	}
	if !modified {
		return rev, short, nil
	}
	return dirtyIdentity(rev, short)
}

// vcsState — commit и признак незакоммиченных правок из build info.
// Пустой commit: build info нет или сборка без VCS (-buildvcs=false, go test).
func vcsState() (rev string, modified bool) {
	info, ok := readBuildInfo()
	if !ok {
		return "", false
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	return rev, modified
}

// dirtyIdentity — личность и каталог грязной сборки. Хеш берёт и поставку,
// и ограждения: чекер лежит в каталоге офиса и не переписывается, так что
// другой validate-result при тех же ролях обязан дать другой каталог.
func dirtyIdentity(identity, dir string) (string, string, error) {
	hash, err := office.Hash(payloadOrDefault(), validatorsOrDefault())
	if err != nil {
		return "", "", err
	}
	return identity + "-dirty", dir + "-dirty-" + hash[:8], nil
}
