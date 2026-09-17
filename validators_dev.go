//go:build !release

package office

import "embed"

// Validators — пуст: без -tags release ограждение собирается из клона
// (OFFICE_CONFIG_ROOT), а поставка отказывает с адресом. embed.FS без
// директивы — законная пустая файловая система.
var Validators embed.FS

const releaseBuild = false
