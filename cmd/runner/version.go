package main

import (
	"fmt"
	"io"

	"github.com/kao73/virtual-office/internal/runner"
)

// versionCommand говорит, что установлено: личность раннера и каталог офиса
// этой личности. Ничего не распаковывает и не открывает ни одного файла
// конфигурации: команда обязана отвечать и там, где хозяйства ещё нет, —
// install.sh зовёт её сразу после установки как доказательство, что
// бинарник вообще запускается на этой машине.
func versionCommand(args []string, out io.Writer) error {
	if err := flags("version").Parse(args); err != nil {
		return err
	}
	office, err := runner.ResolveOffice(runner.Resolve{Unpack: false})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "runner %s\n", office.Identity)
	if office.Source == runner.SourceClone {
		fmt.Fprintf(out, "офис: %s (клон, %s)\n", office.Root, runner.ConfigRootEnv)
		return nil
	}
	fmt.Fprintf(out, "офис: %s\n", office.Root)
	return nil
}
