package office

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
)

// Hash — sha256 по содержимому деревьев: путь, длина и байты каждого файла
// в порядке обхода (он лексический, значит устойчивый); деревья идут в
// порядке аргументов, каждое за своей меткой. Считается по раскладке embed,
// с префиксом bootstrap/, — распакованный каталог им не сверить. Нужен
// грязным сборкам: две сборки одного commit могут нести разные роли или
// разные ограждения, и каталог распаковки у них должен различаться
// (internal/runner.ResolveOffice).
func Hash(srcs ...fs.FS) (string, error) {
	h := sha256.New()
	for i, src := range srcs {
		// Метка дерева — граница между деревьями: без неё файл, переехавший
		// из одного в другое, хеша бы не изменил.
		fmt.Fprintf(h, "fs:%d\x00", i)
		err := walkFiles(src, func(path string, data []byte) error {
			// Длина между путём и байтами — граница: без неё «a»+«bc» и «ab»+«c»
			// дали бы один хеш.
			fmt.Fprintf(h, "%s\x00%d\x00", path, len(data))
			h.Write(data)
			return nil
		})
		if err != nil {
			return "", fmt.Errorf("дерево %d из %d не прочитано: %w", i+1, len(srcs), err)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
