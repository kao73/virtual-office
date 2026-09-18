#!/bin/sh
# Платформа этого раннера обязана быть среди целей релиза — проверка до сборки.
#
# Гейт личности (scripts/check-release-identity.sh) запускает собранный
# бинарник и потому пропускает чужие платформы. Если ни одна цель не совпадёт
# с машиной сборки, он промолчит на всех трёх — а маркер, которым это ловится,
# проверяется уже после публикации релиза. Поэтому совпадение проверяется
# заранее: здесь отказ означает, что релиза не будет вовсе.
set -eu
cd "$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"

targets="$(sed -n 's/.*targets: &build_targets \[\(.*\)\].*/\1/p' .goreleaser.yaml | tr -d ' ' | tr ',' ' ')"
[ -n "$targets" ] || { echo "check-host-is-target: в .goreleaser.yaml не найден якорь build_targets" >&2; exit 1; }

host="$(go env GOOS)_$(go env GOARCH)"
for target in $targets; do
  if [ "$target" = "$host" ]; then
    echo "check-host-is-target: $host среди целей ($targets)"
    exit 0
  fi
done
echo "check-host-is-target: машина сборки $host не входит в цели релиза ($targets): проверять личность собранного раннера будет негде" >&2
exit 1
