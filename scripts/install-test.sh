#!/bin/sh
# Проверка install.sh по четырём сценариям спецификации office-install без
# сети: установка, обновление на месте, битая контрольная сумма, чужая
# платформа. Артефакты — либо настоящий dist/ от scripts/release-snapshot.sh
# ($1), либо поддельный: два скрипта вместо бинарников, tar.gz под текущую
# платформу, checksums.txt.
#
#   sh scripts/install-test.sh          # поддельный dist
#   sh scripts/install-test.sh dist     # настоящий снапшот
set -eu

root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$(uname -m)" in arm64|aarch64) arch=arm64 ;; x86_64|amd64) arch=amd64 ;; *) arch="$(uname -m)" ;; esac
archive="virtual-office_${os}_${arch}.tar.gz"

sum() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1"; else shasum -a 256 "$1"; fi | cut -d' ' -f1; }
fail() { echo "FAIL: $*" >&2; exit 1; }

if [ $# -ge 1 ]; then
  dist="$(CDPATH= cd -- "$1" && pwd)"
else
  dist="$work/dist"; mkdir -p "$dist/bin"
  printf '#!/bin/sh\necho "runner v0.0.0-fake"\necho "офис: $OFFICE_HOME/office/v0.0.0-fake"\n' > "$dist/bin/runner"
  printf '#!/bin/sh\necho fake run-agent\n' > "$dist/bin/run-agent"
  chmod 0755 "$dist/bin/runner" "$dist/bin/run-agent"
  tar -czf "$dist/$archive" -C "$dist/bin" runner run-agent
  printf '%s  %s\n' "$(sum "$dist/$archive")" "$archive" > "$dist/checksums.txt"
fi

# 1. Установка на чистое хозяйство.
home="$work/home1"
OFFICE_INSTALL_FROM="$dist" OFFICE_HOME="$home" sh "$root/install.sh" > "$work/out1" 2>&1 || fail "установка: $(cat "$work/out1")"
[ -x "$home/bin/runner" ] && [ -x "$home/bin/run-agent" ] || fail "бинарники не установлены"
grep -q '^runner ' "$work/out1" || fail "runner version не напечатан"
grep -q "$home/bin" "$work/out1" || fail "каталог не назван"
[ "$(tail -1 "$work/out1")" = "дальше: runner init" ] || fail "последняя строка не про runner init"
echo "ok: установка"

# 2. Обновление на месте: рабочие файлы и старый офис не тронуты, бинарники заменены.
home="$work/home2"; mkdir -p "$home/bin" "$home/office/v0.0.0-old"
echo old > "$home/bin/runner"; echo old > "$home/bin/run-agent"
echo "PROJ: {}" > "$home/projects.local.yaml"; echo "base_url: x" > "$home/tracker.yaml"
echo "old office" > "$home/office/v0.0.0-old/workflow.yaml"
before="$(cat "$home/projects.local.yaml" "$home/tracker.yaml" "$home/office/v0.0.0-old/workflow.yaml")"
OFFICE_INSTALL_FROM="$dist" OFFICE_HOME="$home" sh "$root/install.sh" > "$work/out2" 2>&1 || fail "обновление: $(cat "$work/out2")"
[ "$(cat "$home/bin/runner")" != old ] && [ "$(cat "$home/bin/run-agent")" != old ] || fail "бинарники не заменены"
after="$(cat "$home/projects.local.yaml" "$home/tracker.yaml" "$home/office/v0.0.0-old/workflow.yaml")"
[ "$before" = "$after" ] || fail "обновление тронуло рабочие файлы или старый офис"
echo "ok: обновление на месте"

# 3. Битая контрольная сумма: код ≠ 0, причина названа, в bin/ пусто.
bad="$work/bad"; mkdir -p "$bad"; cp "$dist/$archive" "$bad/"
printf '%s  %s\n' 0000000000000000000000000000000000000000000000000000000000000000 "$archive" > "$bad/checksums.txt"
home="$work/home3"
if OFFICE_INSTALL_FROM="$bad" OFFICE_HOME="$home" sh "$root/install.sh" > "$work/out3" 2>&1; then fail "битая сумма принята"; fi
grep -q "контрольная сумма" "$work/out3" || fail "причина не названа: $(cat "$work/out3")"
[ ! -e "$home/bin/runner" ] || fail "runner установлен несмотря на битую сумму"
[ ! -e "$home/bin/run-agent" ] || fail "run-agent установлен несмотря на битую сумму"
[ ! -e "$home/bin" ] || fail "$home/bin создан несмотря на битую сумму"
echo "ok: битая контрольная сумма"

# 4. Чужая платформа: подменённый uname говорит darwin/amd64.
shim="$work/shim"; mkdir -p "$shim"
printf '#!/bin/sh\ncase "$1" in -s) echo Darwin ;; -m) echo x86_64 ;; *) exec /usr/bin/uname "$@" ;; esac\n' > "$shim/uname"
chmod 0755 "$shim/uname"
home="$work/home4"
if PATH="$shim:$PATH" OFFICE_INSTALL_FROM="$dist" OFFICE_HOME="$home" sh "$root/install.sh" > "$work/out4" 2>&1; then fail "darwin/amd64 принят"; fi
grep -q "darwin/amd64" "$work/out4" || fail "платформа не названа: $(cat "$work/out4")"
grep -q "linux/arm64" "$work/out4" || fail "поддерживаемые не перечислены: $(cat "$work/out4")"
[ ! -e "$home/bin" ] || fail "что-то установлено на чужой платформе"
echo "ok: чужая платформа"

echo "все четыре сценария прошли"
