package runner

import "testing"

// Ключ задачи едет в путь, и приходит он снаружи: каталог не должен уезжать
// из проекта, а ручной прогон обязан выглядеть как работа.
func TestChangeDirRel(t *testing.T) {
	cases := map[string]string{
		"OFFICE-1":  "docs/changes/OFFICE-1",
		"":          "docs/changes/_manual",
		"../../etc": "docs/changes/.._.._etc",
	}
	for key, want := range cases {
		if got := ChangeDirRel(key); got != want {
			t.Errorf("ChangeDirRel(%q) = %q, ожидалось %q", key, got, want)
		}
	}
}

func TestCometChangeName(t *testing.T) {
	cases := map[string]string{
		"OFF-1":     "OFF-1",
		"":          "_manual",
		"../../etc": ".._.._etc",
	}
	for key, want := range cases {
		if got := CometChangeName(key); got != want {
			t.Errorf("CometChangeName(%q) = %q, ожидалось %q", key, got, want)
		}
	}
}

func TestCometChangeDirRel(t *testing.T) {
	cases := map[string]string{
		"OFFICE-1": "docs/comet/changes/OFFICE-1",
		"":         "docs/comet/changes/_manual",
	}
	for key, want := range cases {
		if got := CometChangeDirRel(key); got != want {
			t.Errorf("CometChangeDirRel(%q) = %q, ожидалось %q", key, got, want)
		}
	}
}
