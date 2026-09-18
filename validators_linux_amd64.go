//go:build release

package office

import "embed"

// Validators — ограждение под платформу, которая нужна этому раннеру: сам
// linux/amd64, он же хост и его песочница sbx. Файл кладёт
// scripts/build-validators.sh; без него релизная сборка не компилируется —
// нарочно: неполный релиз не должен собираться.
//
//go:embed payload/validators/validate-result-linux-amd64
var Validators embed.FS

const releaseBuild = true
