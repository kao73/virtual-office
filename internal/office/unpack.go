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
)

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
// остальные — 0644. Свой обход, а не os.CopyFS: тот не умеет прав.
func Unpack(src fs.FS, officeDir, name string) (string, error) {
	target := filepath.Join(officeDir, name)
	if err := os.MkdirAll(officeDir, 0o755); err != nil {
		return "", fmt.Errorf("каталог офисов не создан: %w", err)
	}
	// Каталог версий — тоже из-под umask: 0700-родитель закрыл бы весь офис
	// не хуже, чем 0700 внутри него.
	if err := os.Chmod(officeDir, 0o755); err != nil {
		return "", fmt.Errorf("права каталога офисов не выставлены: %w", err)
	}
	// Уникальный суффикс, а не только pid: две горутины одного процесса
	// не должны писать в один временный каталог.
	tmp, err := os.MkdirTemp(officeDir, tempPrefix+name+"-")
	if err != nil {
		return "", fmt.Errorf("временный каталог распаковки не создан: %w", err)
	}
	if err := copyTree(src, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return "", fmt.Errorf("офис %s не распакован: %w", name, err)
	}
	// Каталоги — как и файлы: MkdirAll отдаёт их umask'у, и при строгом umask
	// офис вышел бы из 0700-каталогов, в которые чужой uid (песочница, общее
	// ${OFFICE_HOME}) не войдёт, хотя файлы в них читаемы.
	if err := chmodDirs(tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return "", fmt.Errorf("права каталогов офиса %s не выставлены: %w", name, err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.RemoveAll(tmp)
		// EEXIST и ENOTEMPTY: кто-то успел раньше, и это удача, а не отказ —
		// каталог на месте. syscall.Errno.Is сводит оба к fs.ErrExist.
		if !errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("офис %s не переименован из временного каталога: %w", name, err)
		}
	}
	return target, nil
}

// chmodDirs выставляет 0755 каждому каталогу внутри root, включая сам root.
// Лечение кончается на дереве поставки: ${OFFICE_HOME} заводит `runner init`,
// каталог версий — Unpack выше, bin/ с ограждением — runner.EnsureValidator,
// и каждый из них выставляет права сам.
func chmodDirs(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		return os.Chmod(path, 0o755)
	})
}

// walkFiles зовёт fn для каждого файла дерева в порядке обхода (он лексический,
// значит устойчивый). Один обход на весь пакет: Hash считает ключ каталога,
// который наполняет copyTree, и разойдись они в том, что считать файлом
// поставки, изменившееся дерево перестало бы менять ключ.
func walkFiles(src fs.FS, fn func(path string, data []byte) error) error {
	return fs.WalkDir(src, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(src, path)
		if err != nil {
			return err
		}
		return fn(path, data)
	})
}

// copyTree пишет каждый файл src под dst; каталоги создаются по пути.
func copyTree(src fs.FS, dst string) error {
	return walkFiles(src, func(path string, data []byte) error {
		out := filepath.Join(dst, filepath.FromSlash(path))
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
		// umask срезает биты при создании — выставляем явно, и не только
		// исполняемым: офис должен разложиться одинаково при любом umask.
		return os.Chmod(out, mode)
	})
}
