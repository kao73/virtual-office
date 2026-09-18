//go:build !release

package validators

import "embed"

// Validators — пуст: без -tags release ограждение собирается из клона
// (OFFICE_CONFIG_ROOT), а поставка отказывает с адресом. embed.FS без
// директивы — законная пустая файловая система (validators_test.go).
var Validators embed.FS
