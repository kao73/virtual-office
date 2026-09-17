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
		switch _, err := os.Stat(path); {
		case err == nil:
			fmt.Fprintf(out, "оставлен %s\n", path)
			continue
		case !errors.Is(err, fs.ErrNotExist):
			return fmt.Errorf("%s не проверен: %w", path, err)
		}
		raw, err := fs.ReadFile(payload.Payload, s.example)
		if err != nil {
			return fmt.Errorf("образец %s не найден в поставке: %w", s.example, err)
		}
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			return fmt.Errorf("образец не записан: %w", err)
		}
		fmt.Fprintf(out, "создан   %s\n", path)
	}
	fmt.Fprintln(out, "дальше: скопируйте каждый образец под рабочее имя и поправьте значения:")
	for _, s := range samples {
		fmt.Fprintf(out, "  cp %s %s   # %s\n", filepath.Join(home, s.example), filepath.Join(home, s.working), s.edit)
	}
	return nil
}
