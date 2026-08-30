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

// Реальный Native CLI принимает <name> изменения только по своему паттерну
// (native-paths.js: NATIVE_CHANGE_NAME_PATTERN, обнаружено живым запуском
// comet при ревью Задачи 5) — обычный ключ трекера после lower-case уже
// подходит, а то, что legacy changeName считает «безопасным» для файловой
// системы (точки, подчёркивания), для Native не годится.
func TestCometChangeName(t *testing.T) {
	cases := map[string]string{
		"OFF-1":     "off-1",
		"PROJ-123":  "proj-123",
		"":          "manual",
		"../../etc": "etc",
	}
	for key, want := range cases {
		got := CometChangeName(key)
		if got != want {
			t.Errorf("CometChangeName(%q) = %q, ожидалось %q", key, got, want)
		}
		// Само свойство, которое чинит этот фикс: результат обязан пройти
		// тот же паттерн, что и реальный CLI, — не просто совпасть со
		// значением из таблицы.
		if !cometNativeNamePattern.MatchString(got) {
			t.Errorf("CometChangeName(%q) = %q не соответствует паттерну реального Native CLI", key, got)
		}
	}
}

func TestCometChangeDirRel(t *testing.T) {
	cases := map[string]string{
		"OFF-1":     "docs/comet/changes/off-1",
		"PROJ-123":  "docs/comet/changes/proj-123",
		"":          "docs/comet/changes/manual",
		"../../etc": "docs/comet/changes/etc",
	}
	for key, want := range cases {
		if got := CometChangeDirRel(key); got != want {
			t.Errorf("CometChangeDirRel(%q) = %q, ожидалось %q", key, got, want)
		}
	}
}
