"""Провайдер YouGile: REST API v2, Bearer-токен из YOUGILE_API_KEY."""
from __future__ import annotations

import os
from datetime import datetime, timezone
from pathlib import Path

import requests

from office_adapter.errors import CapabilityMissing, TrackerError, UsageError
from office_adapter.interface import Capabilities, Card, CardComment
from office_adapter.marking import detect, mark
from office_adapter.profile import Profile

API = "https://yougile.com/api-v2"


def _message_to_comment(msg: dict) -> CardComment:
    ts = msg.get("timestamp", 0) / 1000
    created = datetime.fromtimestamp(ts, tz=timezone.utc).isoformat()
    body = msg.get("text", "")
    return CardComment(author=str(msg.get("fromUserId", "")), created=created,
                       body=body, office_marker=detect(body))


def _task_to_card(task: dict, column_to_state: dict[str, str], fence_board: str,
                  columns_board: dict[str, str], comments: list[CardComment]) -> Card:
    column_id = task.get("columnId", "")
    board_id = columns_board.get(column_id, "")
    attachments = [line for c in comments if c.office_marker == "office"
                   for line in c.body.splitlines() if line.startswith("http")]
    return Card(
        id=task["id"], key=task["id"][:8], title=task.get("title", ""),
        description=task.get("description", ""),
        state=column_to_state.get(column_id), raw_state=column_id,
        url="", labels=[board_id] if board_id else [],
        comments=comments, links=[], attachments=attachments)


class YougileProvider:
    name = "yougile"

    def __init__(self, profile: Profile) -> None:
        key = os.environ.get("YOUGILE_API_KEY")
        if not key:
            raise UsageError("YOUGILE_API_KEY is not set in the environment")
        self._profile = profile
        _, self._board_id = profile.fence
        self._states = profile.tracker["states"]
        self._column_to_state = {r["column"]: s for s, r in self._states.items()}
        self._columns_board: dict[str, str] = {}
        self._session = requests.Session()
        self._session.headers["Authorization"] = f"Bearer {key}"

    # --- транспорт: один ретрай на сеть/5xx (Global Constraints) ---
    def _request(self, method: str, path: str, **kwargs):
        url = f"{API}/{path}"
        last_error: Exception | None = None
        for _ in range(2):
            try:
                response = self._session.request(method, url, timeout=30, **kwargs)
                if response.status_code >= 500:
                    last_error = TrackerError(
                        f"yougile {method} {path}: HTTP {response.status_code}")
                    continue
                if response.status_code >= 400:
                    raise TrackerError(
                        f"yougile {method} {path}: HTTP {response.status_code}",
                        body=response.text[:500])
                return response.json() if response.text else {}
            except requests.RequestException as exc:
                last_error = TrackerError(f"yougile {method} {path}: {exc}")
        raise last_error  # type: ignore[misc]

    def _board_of_column(self, column_id: str) -> str:
        if column_id not in self._columns_board:
            column = self._request("GET", f"columns/{column_id}")
            self._columns_board[column_id] = column.get("boardId", "")
        return self._columns_board[column_id]

    def _comments_of(self, task_id: str) -> list[CardComment]:
        data = self._request("GET", f"chats/{task_id}/messages")
        return [_message_to_comment(m) for m in data.get("content", [])]

    # --- контракт Provider ---
    def capabilities(self) -> Capabilities:
        return Capabilities(attach=True, link=False)

    def list_cards(self, state: str) -> list[Card]:
        column = self._profile.state_repr(state)["column"]
        data = self._request("GET", f"task-list?columnId={column}&limit=1000")
        cards = []
        for task in data.get("content", []):
            self._board_of_column(task.get("columnId", ""))
            cards.append(_task_to_card(
                task, self._column_to_state, self._board_id,
                self._columns_board, self._comments_of(task["id"])))
        return cards

    def read_card(self, card_id: str) -> Card:
        task = self._request("GET", f"tasks/{card_id}")
        self._board_of_column(task.get("columnId", ""))
        return _task_to_card(task, self._column_to_state, self._board_id,
                             self._columns_board, self._comments_of(card_id))

    def move(self, card_id: str, state: str) -> None:
        column = self._profile.state_repr(state)["column"]
        self._request("PUT", f"tasks/{card_id}", json={"columnId": column})

    def comment(self, card_id: str, body: str) -> None:
        self._request("POST", f"chats/{card_id}/messages", json={"text": body})

    def create_card(self, title: str, body: str, state: str) -> str:
        column = self._profile.state_repr(state)["column"]
        created = self._request("POST", "tasks", json={
            "title": title, "columnId": column, "description": body})
        task_id = created.get("id")
        if not task_id:
            raise TrackerError("yougile create task: no id in response",
                               response=created)
        return task_id

    def link(self, card_id: str, other_id: str) -> None:
        raise CapabilityMissing("yougile provider does not support card links")

    def attach(self, card_id: str, file_path: str) -> None:
        path = Path(file_path)
        with path.open("rb") as fh:
            uploaded = self._request("POST", "upload-file",
                                     files={"file": (path.name, fh)})
        url = uploaded.get("url") or uploaded.get("fileUrl")
        if not url:
            raise TrackerError("yougile upload-file: no url in response",
                               response=uploaded)
        self.comment(card_id, mark("office", url))

    def in_fence(self, card: Card) -> bool:
        return self._board_id in card.labels
