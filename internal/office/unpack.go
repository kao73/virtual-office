// Package office — поведение над поставкой офиса: распаковать embed-дерево
// в ${OFFICE_HOME}/office/<имя>/ и посчитать его содержимое.
package office

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// bootstrapPrefix — каталог, который в репозитории оборачивает кит песочницы
// (bootstrap/sbx-kits/…), а в распакованном офисе не нужен: там кит лежит
// как sbx-kits/…, рядом с roles/ и hooks/.
const bootstrapPrefix = "bootstrap/"

// tempPrefix — как называется каталог распаковки, пока она не кончилась.
// Осиротевший после убитого процесса он остаётся лежать: чистить его
// некому — соседний tick мог начать свою распаковку в этот же момент.
const tempPrefix = ".unpack-"

// Unpack раскладывает src в <officeDir>/<name> и отдаёт путь.
//
// Сперва — во временный каталог рядом, потом одним переименованием: читатель
// либо не видит каталога вовсе, либо видит его целиком. Если к моменту
// переименования каталог уже есть (второй раннер успел раньше или офис
// распакован давно), временный удаляется, а существующий остаётся как есть —
// перезаписывать его нельзя: правка руками в нём законна. Пустой каталог
// тоже остаётся: os.Rename, в отличие от rename(2), отказывает и на нём.
//
// Права: embed их не хранит, поэтому файл, начинающийся с «#!», получает 0755,
// остальные — 0644. Свой обход, а не os.CopyFS: тот не умеет ни прав, ни
// переноса bootstrap/ на уровень выше.
func Unpack(src fs.FS, officeDir, name string) (string, error) {
	target := filepath.Join(officeDir, name)
	if err := os.MkdirAll(officeDir, 0o755); err != nil {
		return "", fmt.Errorf("каталог офисов не создан: %w", err)
	}
	// Уникальный суффикс, а не только pid: две горутины одного процесса
	// не должны писать в один временный каталог.
	tmp, err := os.MkdirTemp(officeDir, tempPrefix+name+"-")
	if err != nil {
		return "", fmt.Errorf("временный каталог распаковки не создан: %w", err)
	}
	if err := os.Chmod(tmp, 0o755); err != nil { // MkdirTemp даёт 0700
		_ = os.RemoveAll(tmp)
		return "", err
	}
	if err := copyTree(src, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return "", fmt.Errorf("офис %s не распакован: %w", name, err)
	}
	switch err := os.Rename(tmp, target); {
	case err == nil:
		return target, nil
	case errors.Is(err, fs.ErrExist):
		// EEXIST и ENOTEMPTY: кто-то успел раньше. syscall.Errno.Is сводит
		// оба к fs.ErrExist.
		_ = os.RemoveAll(tmp)
		return target, nil
	default:
		_ = os.RemoveAll(tmp)
		return "", fmt.Errorf("офис %s не переименован из временного каталога: %w", name, err)
	}
}

// copyTree пишет каждый файл src под dst; каталоги создаются по пути.
func copyTree(src fs.FS, dst string) error {
	return fs.WalkDir(src, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(src, path)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, filepath.FromSlash(strings.TrimPrefix(path, bootstrapPrefix)))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		mode := fs.FileMode(0o644)
		if bytes.HasPrefix(data, []byte("#!")) {
			mode = 0o755
		}
		if err := os.WriteFile(out, data, mode); err != nil {
			return err
		}
		// umask может срезать биты при создании — как copyExecutable в адаптере,
		// выставляем явно.
		if mode == 0o755 {
			return os.Chmod(out, mode)
		}
		return nil
	})
}
