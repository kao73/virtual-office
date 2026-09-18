#!/usr/bin/env bash
set -euo pipefail

# Кит лежит рядом со скриптом — и в клоне (bootstrap/sbx-kits/comet-cli), и в
# распакованном офисе (${OFFICE_HOME}/office/<версия>/sbx-kits/comet-cli).
KIT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)/comet-cli"
TAG="office-claude-comet:0.4.0-beta.20"
PROBE="office-comet-bake-$$"

# Всегда убрать пробную песочницу — даже при отказе на середине, иначе она
# остаётся жить с открытым для установки registry.npmjs.org (docs/notes/sbx.md).
trap 'sbx rm --force "$PROBE" >/dev/null 2>&1 || true' EXIT

sbx create --name "$PROBE" --kit "$KIT_DIR" claude "$KIT_DIR" >&2
sbx exec "$PROBE" comet --version >&2
sbx exec "$PROBE" openspec --version >&2
sbx stop "$PROBE" >&2
sbx template save "$PROBE" "$TAG"

echo "Baked $TAG — internal/backends/sbx.Template must match this tag exactly."
