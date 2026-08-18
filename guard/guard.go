// Package guard — детерминированные ограждения прогона.
//
// Роль объявляет их по имени в `role.yaml: guards`, а исполняет один и тот же
// код в двух местах: в Stop-хуке, пока агент жив и может починить сделанное,
// и в раннере после прогона — там чинить уже некому, зато позвать некому и
// помешать. Прогон, убитый таймаутом или пределом шагов, до хука не доживает
// вовсе, и без второй точки входа ограждение было бы необязательным.
//
// Имён ролей здесь нет и быть не может: ограждение знает про каталог изменения
// и точку отсчёта, а кто и зачем их объявил — дело спецификации роли.
package guard

import (
	"fmt"
	"os"
	"strings"

	"github.com/kao73/virtual-office/runner"
)

// Env — обстановка прогона, по которой судит ограждение. Всё это наблюдения
// раннера: агент своей базы не называет, иначе показания давал бы подсудимый.
type Env struct {
	// Workdir — рабочая папка агента, она же корень git-репозитория прогона.
	Workdir string
	// ChangeDir — каталог изменения задачи, абсолютным путём.
	ChangeDir string
	// BaseCommit — HEAD на старте прогона. Пусто в репозитории без коммитов:
	// сравнивать не с чем, остаётся снимок статуса.
	BaseCommit string
	// BaseStatus — путь к снимку `git status --porcelain` на старте.
	BaseStatus string
	// Ignore — что не считать работой роли.
	Ignore []string
	// ResultFile — файл результата: по нему ограждение узнаёт исход, если тот
	// уже записан. Часть проверок спрашивается только с `done`.
	ResultFile string
	// Outcome — исход, уже разобранный вызывающим. Заполняет его раннер: у него
	// результат в руках, и именно по нему поедет задача — в том числе когда он
	// синтетический и файлу не соответствует. Хуку заполнить это поле нечем,
	// и тогда исход читается из ResultFile.
	Outcome runner.Outcome
}

// EnvFrom собирает обстановку из значений переменных прогона.
func EnvFrom(vars map[string]string) Env {
	env := Env{
		ChangeDir:  vars[runner.EnvChangeDir],
		BaseCommit: vars[runner.EnvBaseCommit],
		BaseStatus: vars[runner.EnvBaseStatus],
		ResultFile: vars[runner.EnvResultFile],
	}
	for _, name := range strings.Split(vars[runner.EnvWriteIgnore], ",") {
		if name = strings.TrimSpace(name); name != "" {
			env.Ignore = append(env.Ignore, name)
		}
	}
	// Рабочая папка выводится из пути снимка, а не приходит седьмой переменной:
	// снимок лежит в каталоге обмена, а тот — в корне рабочей папки.
	env.Workdir = runner.WorkdirOf(env.BaseStatus)
	return env
}

// EnvFromOS — то же из окружения процесса. Этой формой пользуется бинарник,
// которого зовёт Stop-хук: больше ему знать неоткуда.
func EnvFromOS() Env {
	vars := make(map[string]string, len(runner.RunVars()))
	for _, name := range runner.RunVars() {
		vars[name] = os.Getenv(name)
	}
	return EnvFrom(vars)
}

// Check исполняет одно ограждение. Ошибка — это непройденная проверка, и текст
// её адресован агенту: он читает его из stderr хука и обязан понять, что делать.
func Check(name string, env Env) error {
	switch name {
	case runner.GuardChangeDirOnly:
		return changeDirOnly(env)
	case runner.GuardPlanMarksOnly:
		return planMarksOnly(env)
	}
	// Загрузка роли неизвестных имён не пропускает, так что сюда попадают только
	// расхождения между списком контракта и этим кодом. Молчать о них нельзя:
	// молчание выглядело бы как пройденная проверка.
	return fmt.Errorf("ограждение %q не реализовано", name)
}

// CheckAll исполняет ограждения роли по порядку и останавливается на первом
// непройденном: чинить их агенту всё равно по одному, а три претензии разом
// читаются хуже одной.
func CheckAll(names []string, env Env) (string, error) {
	for _, name := range names {
		if err := Check(name, env); err != nil {
			return name, err
		}
	}
	return "", nil
}
