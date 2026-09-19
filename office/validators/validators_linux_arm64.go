//go:build release

package validators

import "embed"

// Validators — ограждение под платформу, которая нужна этому раннеру: сам
// linux/arm64, он же хост и его песочница sbx. Файл кладёт
// scripts/build-validators.sh; без него релизная сборка не компилируется —
// нарочно: неполный релиз не должен собираться.
//
//go:embed validate-result-linux-arm64
var Validators embed.FS
