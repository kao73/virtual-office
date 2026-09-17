#!/bin/sh
# Установка офиса на машину без клона и без Go: кладёт runner и run-agent
# из релиза в ${OFFICE_HOME:-$HOME/.office}/bin.
#
#   sh install.sh                              # последний релиз
#   sh install.sh v0.7.0                       # конкретный тег (или OFFICE_VERSION=v0.7.0)
#   OFFICE_INSTALL_FROM=dist sh install.sh     # из каталога артефактов, без сети
#
# Нужны: sh, curl или wget, tar, sha256sum или shasum. Контрольная сумма
# проверяется до распаковки; меняются только два бинарника — рабочие файлы
# и распакованные версии офиса в ${OFFICE_HOME} остаются как были.
set -eu

repo="kao73/virtual-office"
version="${1:-${OFFICE_VERSION:-latest}}"
home="${OFFICE_HOME:-$HOME/.office}"
bin="$home/bin"
supported="darwin/arm64 linux/amd64 linux/arm64"

die() { echo "install.sh: $*" >&2; exit 1; }

# 1. Платформа — до всего остального: чужой ничего не качать.
case "$(uname -s)" in Darwin) os=darwin ;; Linux) os=linux ;; *) os="$(uname -s | tr '[:upper:]' '[:lower:]')" ;; esac
case "$(uname -m)" in arm64|aarch64) arch=arm64 ;; x86_64|amd64) arch=amd64 ;; *) arch="$(uname -m)" ;; esac
case " $supported " in
  *" $os/$arch "*) ;;
  *) die "платформа $os/$arch не поддерживается; есть сборки для: $supported" ;;
esac

archive="virtual-office_${os}_${arch}.tar.gz"
sums="checksums.txt"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# 2. Источник: каталог артефактов или GitHub Releases.
fetch() {  # fetch <файл> — кладёт его в $tmp
  if [ -n "${OFFICE_INSTALL_FROM:-}" ]; then
    cp "$OFFICE_INSTALL_FROM/$1" "$tmp/$1" 2>/dev/null || die "$1 не найден в $OFFICE_INSTALL_FROM"
    return
  fi
  if [ "$version" = latest ]; then url="https://github.com/$repo/releases/latest/download/$1"
  else url="https://github.com/$repo/releases/download/$version/$1"; fi
  if command -v curl >/dev/null 2>&1; then curl -fsSL -o "$tmp/$1" "$url" || die "$1 не скачан: $url"
  elif command -v wget >/dev/null 2>&1; then wget -qO "$tmp/$1" "$url" || die "$1 не скачан: $url"
  else die "нужен curl или wget"; fi
}
fetch "$sums"
fetch "$archive"

# 3. Контрольная сумма — до распаковки.
if command -v sha256sum >/dev/null 2>&1; then actual="$(sha256sum "$tmp/$archive" | cut -d' ' -f1)"
elif command -v shasum >/dev/null 2>&1; then actual="$(shasum -a 256 "$tmp/$archive" | cut -d' ' -f1)"
else die "нужен sha256sum или shasum для проверки $archive"; fi
expected="$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/$sums")"
[ -n "$expected" ] || die "$archive не значится в $sums"
[ "$actual" = "$expected" ] || die "контрольная сумма $archive не совпала: ожидалась $expected, получена $actual; ничего не установлено"

# 4. Распаковка.
mkdir -p "$tmp/x"
tar -xzf "$tmp/$archive" -C "$tmp/x" || die "$archive не распакован"
for name in runner run-agent; do [ -f "$tmp/x/$name" ] || die "в $archive нет $name"; done

# 5. Установка: временное имя и mv — атомарно внутри каталога, и работающий
# бинарник заменять так безопасно.
mkdir -p "$bin"
for name in runner run-agent; do
  cp "$tmp/x/$name" "$bin/.$name.tmp"
  chmod 0755 "$bin/.$name.tmp"
  mv -f "$bin/.$name.tmp" "$bin/$name"
done

# 6. Отчёт: что установлено (и что бинарник запускается), PATH, следующий шаг.
echo "установлено в $bin:"
"$bin/runner" version
case ":$PATH:" in
  *":$bin:"*) echo "$bin уже в PATH" ;;
  *) echo "$bin не в PATH — добавьте в профиль оболочки:"; echo "  export PATH=\"$bin:\$PATH\"" ;;
esac
echo "дальше: runner init"
