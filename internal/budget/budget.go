// Package budget — политика расхода поверх реестра прогонов.
//
// Учёт и ограничение разведены намеренно. Реестр (пакет ledger) ведётся всегда
// и ничего не решает; бюджеты — необязательная политика над ним. Файлов у неё два —
// дефолты офиса и накладка машины, — и оба необязательны: нет ни одного — нет
// лимитов, и это естественное состояние офиса, а не недонастроенное:
// раннер считает прогоны и берёт задачи. Тем и отличается от workflow.yaml,
// без которого раннеру нечем решать вовсе.
package budget

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"

	"gopkg.in/yaml.v3"
)

// File — имя файла бюджетов. Имя одно, а файлов два: дефолты офиса лежат
// в конфиг-репозитории, перекрытие этой машины — в ${OFFICE_HOME}. Оба
// необязательны.
const File = "budgets.yaml"

// Режимы срабатывания лимита.
const (
	// Warn — сказать и продолжить. Режим по умолчанию: тот, кто вписал число,
	// не должен обнаружить, что раннер молча перестал брать задачи.
	Warn = "warn"
	// Stop — не начинать работу, упирающуюся в этот предел.
	Stop = "stop"
)

var modes = []string{Warn, Stop}

// Limit — предел и то, что делать при его достижении.
type Limit struct {
	USD      float64 `yaml:"usd"`
	OnExceed string  `yaml:"on_exceed"`
}

// Set — задан ли предел вовсе. Ноль означает «не ограничивать».
func (l Limit) Set() bool { return l.USD > 0 }

// Stops — останавливает ли предел работу.
func (l Limit) Stops() bool { return l.OnExceed == Stop }

// Exceeded — исчерпан ли предел потраченным.
//
// Равенство считается исчерпанием: потратив ровно бюджет, дальше тратить нечего.
func (l Limit) Exceeded(spent float64) bool { return l.Set() && spent >= l.USD }

// Budgets — все пределы офиса.
//
// Их три, и они разные по природе. per_task — сумма по задаче за всё время:
// задача, в которую упёрлись роли, дорожает кругами. per_role_daily — расход
// роли за календарные сутки: страховка от раннера, зациклившегося на очереди.
// per_run — цена одного прогона: сигнал, что роль ведёт себя не так, как ждали.
type Budgets struct {
	PerTask      Limit `yaml:"per_task"`
	PerRoleDaily Limit `yaml:"per_role_daily"`
	PerRun       Limit `yaml:"per_run"`
}

// Any — настроен ли хоть один предел. Пусто означает «только учёт».
func (b Budgets) Any() bool {
	return b.PerTask.Set() || b.PerRoleDaily.Set() || b.PerRun.Set()
}

// Load читает бюджеты из дефолтов офиса и накладки этой машины.
//
// Файлов два и оба необязательны: пределы — политика поверх учёта, и офис без них
// не «недонастроен», а настроен так. Накладка перекрывает дефолт **по имени
// предела и целиком**: названный в ней предел заменяет дефолтный вместе с режимом,
// не названный остаётся как был. Половинчатое слияние (сумма отсюда, режим оттуда)
// давало бы предел, которого не писал никто.
//
// Разбор строгий, как у графа, и по той же причине: опечатка в имени лимита
// или режима молча снимает ограничение, а узнать об этом человек может только
// по счёту. Умолчание здесь одно — режим warn.
func Load(officePath, machinePath string) (Budgets, error) {
	office, err := loadFile(officePath)
	if err != nil {
		return Budgets{}, err
	}
	machine, named, err := loadOverlay(machinePath)
	if err != nil {
		return Budgets{}, err
	}
	return office.overlay(machine, named), nil
}

// overlay накладывает пределы машины на дефолты офиса.
//
// Решает **названность в файле, а не значение**. Разница не теоретическая:
// `usd: 0` означает «предела нет», и если бы накладка узнавалась по ненулевой
// сумме, снять дефолтный предел на этой машине было бы нечем — накладка молча
// не сработала бы.
func (b Budgets) overlay(with Budgets, named map[string]bool) Budgets {
	if named["per_task"] {
		b.PerTask = with.PerTask
	}
	if named["per_role_daily"] {
		b.PerRoleDaily = with.PerRoleDaily
	}
	if named["per_run"] {
		b.PerRun = with.PerRun
	}
	return b
}

// loadOverlay читает накладку и заодно говорит, какие пределы она называет.
//
// Файл читается один раз и разбирается дважды: строгим декодером — ради значений
// и проверок, картой узлов — ради самих имён. Два чтения одного файла в одном
// вызове могли бы разойтись, а строгий разбор имён не отдаёт: он их проглатывает
// в поля структуры, где «названо» уже неотличимо от «оставлено нулём».
func loadOverlay(path string) (Budgets, map[string]bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Budgets{}, nil, nil
	}
	if err != nil {
		return Budgets{}, nil, fmt.Errorf("%s не прочитан: %w", path, err)
	}

	b, err := decode(path, raw)
	if err != nil {
		return Budgets{}, nil, err
	}

	var keys map[string]yaml.Node
	if err := yaml.Unmarshal(raw, &keys); err != nil {
		return Budgets{}, nil, fmt.Errorf("%s не разобран: %w", path, err)
	}
	named := make(map[string]bool, len(keys))
	for key := range keys {
		named[key] = true
	}
	return b, named, nil
}

func loadFile(path string) (Budgets, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Budgets{}, nil
	}
	if err != nil {
		return Budgets{}, fmt.Errorf("%s не прочитан: %w", path, err)
	}
	return decode(path, raw)
}

// decode разбирает прочитанное тело: строго, с проверкой и умолчанием режима.
func decode(path string, raw []byte) (Budgets, error) {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var b Budgets
	// Пустой документ — это io.EOF, и означает он то же, что отсутствие файла:
	// пределов нет. Файл из одних комментариев — обычный способ снять их на время,
	// не выбрасывая объяснений, и жаловаться тут не на что.
	if err := dec.Decode(&b); err != nil && !errors.Is(err, io.EOF) {
		return Budgets{}, fmt.Errorf("%s не разобран: %w", path, err)
	}

	if err := b.validate(); err != nil {
		return Budgets{}, fmt.Errorf("%s нарушает контракт бюджетов: %w", path, err)
	}
	b.PerTask.OnExceed = orWarn(b.PerTask.OnExceed)
	b.PerRoleDaily.OnExceed = orWarn(b.PerRoleDaily.OnExceed)
	b.PerRun.OnExceed = orWarn(b.PerRun.OnExceed)
	return b, nil
}

func orWarn(mode string) string {
	if mode == "" {
		return Warn
	}
	return mode
}

func (b Budgets) validate() error {
	errs := []error{
		b.PerTask.validate("per_task"),
		b.PerRoleDaily.validate("per_role_daily"),
		b.PerRun.validate("per_run"),
	}
	// Прогон не прерывается по цене: к моменту, когда она известна, работа уже
	// сделана и оплачена. Молча принять stop значило бы обещать несуществующее.
	if b.PerRun.Stops() {
		errs = append(errs, errors.New("per_run.on_exceed=stop: цена прогона известна только после него, "+
			"прерывать уже нечего; жёсткая граница прогона — limits.max_turns в role.yaml"))
	}
	return errors.Join(errs...)
}

func (l Limit) validate(name string) error {
	var errs []error
	if l.USD < 0 {
		errs = append(errs, fmt.Errorf("%s.usd=%v: ожидается положительное число", name, l.USD))
	}
	if l.OnExceed != "" && !slices.Contains(modes, l.OnExceed) {
		errs = append(errs, fmt.Errorf("%s.on_exceed=%q: допустимы %v", name, l.OnExceed, modes))
	}
	// Режим без значения — обычно недописанный лимит. Принять его молча значило бы
	// оставить человека в уверенности, что расход ограничен.
	if l.OnExceed != "" && !l.Set() {
		errs = append(errs, fmt.Errorf("%s.on_exceed=%q без usd: ограничивать нечем", name, l.OnExceed))
	}
	return errors.Join(errs...)
}
