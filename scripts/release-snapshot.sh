#!/bin/sh
# Снапшот релиза без тега: тот же GoReleaser и тот же .goreleaser.yaml, что
# в .github/workflows/release.yml, но локально и без публикации. Результат —
# dist/: три архива, checksums.txt, бинарники по платформам. Версия
# GoReleaser закреплена: два разработчика должны собирать одинаковый dist/.
# Первый запуск скачивает и собирает GoReleaser — это минуты, не секунды.
set -eu
cd "$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
sh scripts/check-host-is-target.sh
go run github.com/goreleaser/goreleaser/v2@v2.18.2 release --snapshot --clean
# Тот же гейт, что в release.yml: проверка личности обязана сработать на хосте.
ls dist/identity-checked-* >/dev/null 2>&1 || { echo "release-snapshot: проверка личности не сработала ни на одной цели (нет dist/identity-checked-*)" >&2; exit 1; }
