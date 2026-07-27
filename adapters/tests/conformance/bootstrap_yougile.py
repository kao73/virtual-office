"""Одноразовая закладка YouGile-полигона: проект + две доски + колонки.

Запуск: uv run python tests/conformance/bootstrap_yougile.py
Идемпотентен: если polygons.yaml уже содержит yougile-секцию — выходит без действий
(живость проекта не проверяется).
Пишет yougile-секцию polygons.yaml сам; ничего не печатает, кроме прогресса.
"""
from __future__ import annotations

import sys
from pathlib import Path

import yaml

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))

from office_adapter.profile import load_profile_data  # noqa: E402
from office_adapter.providers.yougile import YougileProvider  # noqa: E402

POLYGONS = Path(__file__).parent / "polygons.yaml"
PROJECT_TITLE = "office-polygon"
STATES = ("idea", "analysis", "done")


def main() -> None:
    existing = yaml.safe_load(POLYGONS.read_text()) if POLYGONS.exists() else {}
    if "yougile" in (existing or {}):
        print("polygons.yaml already has a yougile section — nothing to do")
        return

    # Провайдеру нужен формально валидный профиль — фенс уточним после создания доски.
    stub = load_profile_data({"tracker": {
        "provider": "yougile", "fence": "board:stub",
        "states": {"idea": {"column": "stub"}}}})
    api = YougileProvider(stub)

    created: dict[str, str] = {}
    try:
        project = api._request("POST", "projects", json={"title": PROJECT_TITLE})
        created["project"] = project_id = project["id"]
        fence_board = api._request("POST", "boards", json={
            "title": "polygon-fence", "projectId": project_id})["id"]
        created["fence_board"] = fence_board
        outside_board = api._request("POST", "boards", json={
            "title": "polygon-outside", "projectId": project_id})["id"]
        created["outside_board"] = outside_board

        columns = {}
        for state in STATES:
            columns[state] = api._request("POST", "columns", json={
                "title": state, "boardId": fence_board})["id"]
            created[f"column_{state}"] = columns[state]
        outside_column = api._request("POST", "columns", json={
            "title": "idea", "boardId": outside_board})["id"]
        created["outside_column"] = outside_column
    except Exception:
        # Частичный сбой: перечислить осиротевшие ресурсы, чтобы их можно было
        # найти и удалить руками — иначе повторный запуск наплодит дубликаты.
        print(f"BOOTSTRAP FAILED, orphaned resources: {created}", file=sys.stderr)
        raise

    section = {
        "yougile": {
            "profile": {
                "tracker": {
                    "provider": "yougile",
                    "fence": f"board:{fence_board}",
                    "states": {s: {"column": columns[s]} for s in STATES},
                }
            },
            "cleanup": {"mode": "delete"},
            "outside": {"board": outside_board, "column": outside_column},
        }
    }
    POLYGONS.write_text(yaml.safe_dump({**(existing or {}), **section},
                                       allow_unicode=True, sort_keys=False))
    print(f"polygon ready: project={project_id} fence={fence_board}")


if __name__ == "__main__":
    main()
