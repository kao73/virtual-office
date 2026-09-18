#!/bin/sh
# Личность собранного раннера равна версии релиза — проверка после сборки:
# GoReleaser зовёт этот скрипт из builds[].hooks.post для каждой цели runner,
# ещё до публикации. Чужие платформы пропускаются (бинарник не запустится);
# для хоста запускается `runner version` в пустом OFFICE_HOME и первая строка
# сверяется с «runner v<версия>». Снапшоту с незакоммиченного дерева законно
# отвечать «…-dirty» — релизу нет: у него дерево чистое по построению.
#
#   sh scripts/check-release-identity.sh <бинарник> <версия без v> <os> <arch> [true|false: снапшот]
set -eu

bin=$1; version=$2; os=$3; arch=$4; snapshot=${5:-false}

host_os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$(uname -m)" in arm64|aarch64) host_arch=arm64 ;; x86_64|amd64) host_arch=amd64 ;; *) host_arch="$(uname -m)" ;; esac
if [ "$os/$arch" != "$host_os/$host_arch" ]; then
  echo "check-release-identity: $os/$arch не хост ($host_os/$host_arch), пропуск"
  exit 0
fi

home="$(mktemp -d)"
trap 'rm -rf "$home"' EXIT
got="$(OFFICE_HOME="$home" OFFICE_CONFIG_ROOT= "$bin" version | head -1)"
want="runner v$version"
if [ "$got" = "$want" ]; then
  echo "check-release-identity: $got"
  exit 0
fi
if [ "$snapshot" = true ] && [ "$got" = "$want-dirty" ]; then
  echo "check-release-identity: $got (снапшот с незакоммиченного дерева)"
  exit 0
fi
echo "check-release-identity: ${bin} называет себя «${got}», ожидалось «${want}»" >&2
exit 1
