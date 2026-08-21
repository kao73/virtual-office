package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// Строка об источнике печатается **в момент разрешения пути**, а не в конце сборки.
//
// Тест на порядок, а не на текст, и он не педантизм: первая редакция копила строки
// и печатала их разом после всех загрузчиков — то есть при отказе не печатала
// ничего, ровно в том случае, ради которого печать и заведена.
//
// Проверяется здесь только configSources. Порядок вызовов внутри office() тест
// не держит, и сломать его можно двумя способами: вернуть накопление до конца
// сборки или поднять все вызовы sources.* в начало функции — тогда строка про
// tracker.yaml выйдет и под --tracker mock, где файл не открывается.
func TestConfigSourcesPrintImmediately(t *testing.T) {
	var out strings.Builder
	sources := configSources{out: &out}

	sources.office("/офис", "workflow.yaml")
	if !strings.Contains(out.String(), "workflow.yaml") {
		t.Fatalf("первая строка не напечатана сразу: %q", out.String())
	}

	// Вторая строка выходит следом, а шапка остаётся одна.
	sources.machine("/машина", "tracker.yaml")
	printed := out.String()
	if strings.Count(printed, "конфигурация:") != 1 {
		t.Errorf("шапка напечатана не один раз:\n%s", printed)
	}
	if !strings.Contains(printed, filepath.Join("/машина", "tracker.yaml")) {
		t.Errorf("вторая строка не напечатана:\n%s", printed)
	}

	// Файл, которого нет, называется тоже: «нет» — такой же ответ, как путь,
	// и для необязательных бюджетов он законный.
	if !strings.Contains(printed, "нет") {
		t.Errorf("отсутствующий файл не помечен:\n%s", printed)
	}
}
