//go:build release

package validators

import (
	"bytes"
	"io/fs"
	"runtime"
	"testing"
)

// Релизная сборка несёт ограждение под собственную платформу, и это
// настоящий бинарник: ELF под Linux, Mach-O под darwin.
func TestValidatorsCarryHostChecker(t *testing.T) {
	raw, err := fs.ReadFile(Validators, "validate-result-"+runtime.GOOS+"-"+runtime.GOARCH)
	if err != nil {
		t.Fatalf("ограждение хоста не встроено: %v", err)
	}
	if len(raw) <= 1<<20 {
		t.Errorf("ограждение хоста весит %d байт, ожидался настоящий бинарник (> 1 МиБ)", len(raw))
	}
	if !bytes.HasPrefix(raw, []byte("\x7fELF")) && !bytes.HasPrefix(raw, []byte{0xcf, 0xfa, 0xed, 0xfe}) {
		t.Errorf("ограждение хоста не похоже ни на ELF, ни на Mach-O: первые байты % x", raw[:4])
	}

	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		if _, err := fs.Stat(Validators, "validate-result-linux-arm64"); err != nil {
			t.Errorf("darwin/arm64 обязан нести ограждение своей песочницы sbx (linux/arm64): %v", err)
		}
	}
}
