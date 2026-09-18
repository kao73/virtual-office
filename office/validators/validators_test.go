//go:build !release

package validators

import (
	"errors"
	"io/fs"
	"runtime"
	"testing"
)

// Сборка без -tags release ограждений не несёт: набор пуст, Open отвечает
// ErrNotExist, а не паникой и не чужим бинарником. На это опирается отказ
// EnsureValidator в режиме поставки. Под тегом файл не собирается вовсе —
// там своё утверждение, validators_release_test.go.
func TestValidatorsAbsentWithoutReleaseTag(t *testing.T) {
	_, err := Validators.Open("validate-result-" + runtime.GOOS + "-" + runtime.GOARCH)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("пустой набор ответил %v, ожидался fs.ErrNotExist", err)
	}
}
