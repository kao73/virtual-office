//go:build release

package validators

import "embed"

// Validators — ограждения под платформы, которые нужны этому раннеру: хост
// darwin/arm64 и его песочница sbx, linux/arm64. Файлы кладёт
// scripts/build-validators.sh; без них релизная сборка не компилируется —
// нарочно: неполный релиз не должен собираться.
//
//go:embed validate-result-darwin-arm64 validate-result-linux-arm64
var Validators embed.FS
