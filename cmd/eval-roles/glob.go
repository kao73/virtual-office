package main

import (
	"path/filepath"
	"strings"
)

// globMatch сообщает, подходит ли path под pattern: "**" совпадает с нулём или
// более целых сегментов пути (в том числе ни с одного), а любой другой
// сегмент — по семантике filepath.Match для одного сегмента ("*" не
// пересекает "/"). Сломанный паттерн (filepath.ErrBadPattern) возвращается
// ошибкой, а не сворачивается в «не совпало» — это значит, что сломан сам
// паттерн, а не что путь вне области.
func globMatch(pattern, path string) (bool, error) {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(path, "/"))
}

func matchSegments(pat, name []string) (bool, error) {
	if len(pat) == 0 {
		return len(name) == 0, nil
	}
	if pat[0] == "**" {
		ok, err := matchSegments(pat[1:], name)
		if err != nil || ok {
			return ok, err
		}
		if len(name) == 0 {
			return false, nil
		}
		return matchSegments(pat, name[1:])
	}
	if len(name) == 0 {
		return false, nil
	}
	ok, err := filepath.Match(pat[0], name[0])
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	return matchSegments(pat[1:], name[1:])
}
