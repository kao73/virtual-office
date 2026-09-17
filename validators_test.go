package office

import (
	"errors"
	"io/fs"
	"runtime"
	"testing"
)

// Сборка без -tags release ограждений не несёт: набор пуст, Open отвечает
// ErrNotExist, а не паникой и не чужим бинарником. На это опирается отказ
// EnsureValidator в режиме поставки.
func TestValidatorsAbsentWithoutReleaseTag(t *testing.T) {
	if releaseBuild {
		t.Skip("сборка с -tags release: см. validators_release_test.go")
	}
	_, err := Validators.Open("payload/validators/validate-result-" + runtime.GOOS + "-" + runtime.GOARCH)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("пустой набор ответил %v, ожидался fs.ErrNotExist", err)
	}
}
