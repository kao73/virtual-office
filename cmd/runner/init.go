package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	payload "github.com/kao73/virtual-office"
	"github.com/kao73/virtual-office/internal/runner"
	"github.com/kao73/virtual-office/internal/tracker"
	"github.com/kao73/virtual-office/internal/tracker/jira"
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

// initCommand заводит хозяйство раннера: каталог ${OFFICE_HOME} и два образца
// рядом с местом, где будут лежать рабочие файлы. Образцы — из поставки
// в бинарнике в любом режиме, в том числе из клона: init не разрешает офис
// и ничего не распаковывает.
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
	for _, s := range samples {
		path := filepath.Join(home, s.example)
		raw, err := fs.ReadFile(payload.Payload, s.example)
		if err != nil {
			return fmt.Errorf("образец %s не найден в поставке: %w", s.example, err)
		}
		// O_EXCL, а не «проверить и записать»: существующий образец не
		// трогается ни при какой гонке, а недописанный (кончилось место)
		// не остаётся лежать под видом оставленного — он убирается.
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		switch {
		case errors.Is(err, fs.ErrExist):
			fmt.Fprintf(out, "оставлен %s\n", path)
			continue
		case err != nil:
			return fmt.Errorf("образец не записан: %w", err)
		}
		if err := writeAndClose(f, raw); err != nil {
			os.Remove(path) // недописанный образец не должен сойти за оставленный
			return fmt.Errorf("образец %s не записан: %w", path, err)
		}
		fmt.Fprintf(out, "создан   %s\n", path)
	}
	fmt.Fprintln(out, "дальше: скопируйте каждый образец под рабочее имя и поправьте значения:")
	for _, s := range samples {
		fmt.Fprintf(out, "  cp %s %s   # %s\n", filepath.Join(home, s.example), filepath.Join(home, s.working), s.edit)
	}
	return nil
}
