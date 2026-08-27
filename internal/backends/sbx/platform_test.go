package sbx

import (
	"runtime"
	"testing"
)

// Ограждение внутри песочницы исполняется её системой, а не хостовой: бинарник,
// собранный под macOS, там просто не запустится. Значение измерено, а не угадано —
// `uname` внутри песочницы, см. docs/notes/sbx.md.
func TestPlatformIsSandboxLinux(t *testing.T) {
	p := Platform()
	if p.OS != "linux" {
		t.Errorf("система песочницы %q, ожидался linux", p.OS)
	}
	// Песочница — microVM на том же железе, архитектура совпадает с хостовой.
	if p.Arch != runtime.GOARCH {
		t.Errorf("архитектура песочницы %q, ожидалась %q", p.Arch, runtime.GOARCH)
	}
}
