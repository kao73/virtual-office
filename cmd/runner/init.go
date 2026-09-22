package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/kao73/virtual-office/internal/runner"
	"github.com/kao73/virtual-office/internal/tracker"
	"github.com/kao73/virtual-office/internal/tracker/jira"
	payload "github.com/kao73/virtual-office/office"
)

// sample — образец из поставки, рабочее имя, под которым его ждёт раннер,
// и что в нём править.
type sample struct{ example, working, edit string }

var samples = []sample{
	{tracker.ProjectsLocalExampleFile, tracker.ProjectsLocalFile, "ключ проекта, repo_url, default_branch, tracker"},
	{jira.ExampleFile, jira.TrackerFile, "base_url и четыре customfield_*; нужен только проектам с tracker: jira"},
}

// writeAndClose — запись и закрытие как одна неудача: оборванная на середине
// (кончилось место) и незакрытая запись значат для вызывающего одно и то же.
func writeAndClose(f *os.File, raw []byte) error {
	if _, err := f.Write(raw); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// schedulerDir — подкаталог заданий планировщика. Имя одно и то же в поставке
// и в хозяйстве: искать образец там же, где он лежит в клоне, проще, чем
// помнить два имени.
const schedulerDir = "scheduler"

// schedulerSamples — задания планировщика из поставки. Отдельным списком,
// а не в samples: рабочего имени у них нет — их не копируют, а ставят
// в launchd или systemd, и правят в них учётку и путь ${OFFICE_HOME},
// а не четыре значения конфигурации. Кладутся все три на любой платформе:
// машина, на которой юнит правят, не всегда та, на которой он работает.
var schedulerSamples = []string{
	"local.office.runner.plist",
	"office-runner.service",
	"office-runner.timer",
}

// place кладёт один файл поставки в хозяйство, не трогая уже лежащий там.
// Одна функция на оба вида образцов нарочно: разойдись у них семантика
// перезаписи, узнали бы мы об этом чужим правленым юнитом.
func place(out io.Writer, payloadPath, dst string) error {
	raw, err := fs.ReadFile(payload.Payload, payloadPath)
	if err != nil {
		return fmt.Errorf("образец %s не найден в поставке: %w", payloadPath, err)
	}
	// O_EXCL, а не «проверить и записать»: существующий образец не
	// трогается ни при какой гонке, а недописанный (кончилось место)
	// не остаётся лежать под видом оставленного — он убирается.
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	switch {
	case errors.Is(err, fs.ErrExist):
		// Не обычный файл на месте образца — не наша правка и не чужой
		// правленый юнит, а что-то третье (каталог, сокет...). «Оставлен»
		// сказал бы про него то же самое, что про честно правленый файл.
		info, statErr := os.Lstat(dst)
		if statErr != nil {
			return fmt.Errorf("%s занят, но не читается: %w", dst, statErr)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s занят не обычным файлом (%s) — уберите вручную и повторите init", dst, info.Mode())
		}
		fmt.Fprintf(out, "оставлен %s\n", dst)
		return nil
	case err != nil:
		return fmt.Errorf("образец не записан: %w", err)
	}
	if err := writeAndClose(f, raw); err != nil {
		return cleanupPartialWrite(dst, err)
	}
	fmt.Fprintf(out, "создан   %s\n", dst)
	return nil
}

// cleanupPartialWrite убирает недописанный образец после неудачной записи.
// Ошибку самого Remove не глотаем: не убравшийся огрызок на следующем init
// попадёт в ветку fs.ErrExist и сойдёт за оставленный — вторая беда спрячется
// за первой, а разбираться придётся с отказавшим юнитом планировщика, а не
// с исходной причиной.
func cleanupPartialWrite(dst string, writeErr error) error {
	if rmErr := os.Remove(dst); rmErr != nil {
		return fmt.Errorf("образец %s не записан (%w), и недописанный файл не убран (%v) — уберите его вручную и повторите init", dst, writeErr, rmErr)
	}
	return fmt.Errorf("образец %s не записан: %w", dst, writeErr)
}

// initCommand заводит хозяйство раннера: каталог ${OFFICE_HOME}, два образца
// конфигурации рядом с местом, где будут лежать рабочие файлы, и подкаталог
// scheduler/ с тремя заданиями планировщика. Всё — из поставки в бинарнике
// в любом режиме, в том числе из клона: init не разрешает офис и ничего
// не распаковывает.
//
// Рабочие файлы — projects.local.yaml, tracker.yaml, budgets.yaml — команда
// не трогает никогда: они свойство инстанса, и переписать их значило бы
// снести настройку машины одной командой. Существующий образец тоже
// остаётся: его могли править как черновик.
func initCommand(args []string, out io.Writer) error {
	if err := flags("init").Parse(args); err != nil {
		return err
	}
	home, err := runner.Home()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		return fmt.Errorf("хозяйство раннера не создано: %w", err)
	}
	// Права явно: под строгим umask хозяйство вышло бы 0700, и в офис под ним
	// не вошёл бы ни чужой uid песочницы, ни второй пользователь машины.
	if err := os.Chmod(home, 0o755); err != nil {
		return fmt.Errorf("права хозяйства раннера не выставлены: %w", err)
	}
	for _, s := range samples {
		if err := place(out, s.example, filepath.Join(home, s.example)); err != nil {
			return err
		}
	}

	schedHome := filepath.Join(home, schedulerDir)
	if err := os.MkdirAll(schedHome, 0o755); err != nil {
		return fmt.Errorf("каталог заданий планировщика не создан: %w", err)
	}
	// Права явно — по той же причине, что у самого хозяйства выше: под строгим
	// umask каталог вышел бы 0700, и в него не вошёл бы ни чужой uid песочницы,
	// ни второй пользователь машины. Не избыточно: MkdirAll отдаёт права umask'у.
	if err := os.Chmod(schedHome, 0o755); err != nil {
		return fmt.Errorf("права каталога заданий планировщика не выставлены: %w", err)
	}
	for _, name := range schedulerSamples {
		// Путь внутри поставки — всегда через косую черту: так устроен embed.FS.
		if err := place(out, schedulerDir+"/"+name, filepath.Join(schedHome, name)); err != nil {
			return err
		}
	}
	fmt.Fprintln(out, "дальше: скопируйте каждый образец под рабочее имя и поправьте значения:")
	for _, s := range samples {
		fmt.Fprintf(out, "  cp %s %s   # %s\n", filepath.Join(home, s.example), filepath.Join(home, s.working), s.edit)
	}
	// Вторая подсказка отдельной: задания не копируют под рабочее имя — их
	// ставят в планировщик, и правят в них учётку и путь, а не значения
	// конфигурации. Слить обе в одну фразу значило бы получить фразу, неверную
	// для обеих.
	fmt.Fprintf(out, "задания планировщика — в %s: поставьте нужное в launchd или systemd,\n", schedHome)
	fmt.Fprintln(out, "поправив в нём учётку и путь ${OFFICE_HOME}:")
	fmt.Fprintf(out, "  %s   # macOS, launchd\n", filepath.Join(schedHome, "local.office.runner.plist"))
	fmt.Fprintf(out, "  %s\n", filepath.Join(schedHome, "office-runner.service"))
	fmt.Fprintf(out, "  %s   # Linux, systemd: пара service + timer\n", filepath.Join(schedHome, "office-runner.timer"))
	return nil
}
