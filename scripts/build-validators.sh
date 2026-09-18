#!/bin/sh
# Кросс-сборка ограждения validate-result под три платформы релиза в
# payload/validators/ — оттуда их встраивает сборка с -tags release
# (validators_<os>_<arch>.go в корне). Зовёт GoReleaser (before.hooks);
# руками — перед `go build -tags release`.
set -eu
cd "$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
for target in darwin/arm64 linux/amd64 linux/arm64; do
  os=${target%/*}; arch=${target#*/}
  GOOS=$os GOARCH=$arch CGO_ENABLED=0 go build -trimpath \
    -o "payload/validators/validate-result-$os-$arch" ./cmd/validate-result
  echo "payload/validators/validate-result-$os-$arch"
done
