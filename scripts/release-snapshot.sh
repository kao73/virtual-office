#!/bin/sh
# Снапшот релиза без тега: тот же GoReleaser и тот же .goreleaser.yaml, что
# в .github/workflows/release.yml, но локально и без публикации. Результат —
# dist/: три архива, checksums.txt, бинарники по платформам. Версия
# GoReleaser закреплена: два разработчика должны собирать одинаковый dist/.
# Первый запуск скачивает и собирает GoReleaser — это минуты, не секунды.
set -eu
cd "$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
exec go run github.com/goreleaser/goreleaser/v2@v2.18.2 release --snapshot --clean
