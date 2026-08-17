// Package budget — политика расхода поверх реестра прогонов.
//
// Учёт и ограничение разведены намеренно. Реестр (пакет ledger) ведётся всегда
// и ничего не решает; бюджеты — необязательная политика над ним. Нет файла —
// нет лимитов, и это естественное состояние офиса, а не недонастроенное:
// раннер считает прогоны и берёт задачи. Тем и отличается от workflow.yaml,
// без которого раннеру нечем решать вовсе.
package budget

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"gopkg.in/yaml.v3"
)

// File — имя файла бюджетов в корне конфиг-репозитория.
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

// Load читает бюджеты. Отсутствие файла — не ошибка: пределы необязательны.
//
// Разбор строгий, как у графа, и по той же причине: опечатка в имени лимита
// или режима молча снимает ограничение, а узнать об этом человек может только
// по счёту. Умолчание здесь одно — режим warn.
func Load(path string) (Budgets, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Budgets{}, nil
	}
	if err != nil {
		return Budgets{}, fmt.Errorf("%s не прочитан: %w", filepath.Base(path), err)
	}

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
