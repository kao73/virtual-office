"""Провайдер Jira Server/DC: REST v2, basic auth (Bearer на инстансе не работает)."""
from __future__ import annotations

import os
from pathlib import Path

import requests

from office_adapter.errors import TrackerError, UsageError
from office_adapter.interface import Capabilities, Card, CardComment
from office_adapter.marking import detect
from office_adapter.profile import Profile

_FIELDS = "summary,description,status,labels,comment,attachment,issuelinks"


def _issue_to_card(issue: dict, states: dict, fence_label: str,
                   base_url: str) -> Card:
    fields = issue.get("fields", {})
    status = fields.get("status", {}).get("name", "")
    labels = fields.get("labels", [])

    candidates = [s for s, r in states.items() if r.get("status") == status]
    if len(candidates) > 1:
        candidates = [s for s in candidates
                      if states[s].get("label") in labels]
    state = candidates[0] if len(candidates) == 1 else None

    comments = []
    for c in fields.get("comment", {}).get("comments", []):
        body = c.get("body", "")
        comments.append(CardComment(
            author=c.get("author", {}).get("name", ""),
            created=c.get("created", ""), body=body,
            office_marker=detect(body)))

    links = []
    for link in fields.get("issuelinks", []):
        target = link.get("outwardIssue") or link.get("inwardIssue") or {}
        if target.get("key"):
            links.append(target["key"])

    return Card(
        id=issue["key"], key=issue["key"], title=fields.get("summary", ""),
        description=fields.get("description") or "",
        state=state, raw_state=status,
        url=f"{base_url}/browse/{issue['key']}",
        labels=labels, comments=comments, links=links,
        attachments=[a.get("filename", "") for a in fields.get("attachment", [])])


def _pick_transition(transitions: list[dict], target_status: str) -> str:
    for t in transitions:
        if t.get("to", {}).get("name", "").lower() == target_status.lower():
            return t["id"]
    raise TrackerError(
        f"no direct transition to status '{target_status}'",
        available=[t.get("to", {}).get("name") for t in transitions])


class JiraProvider:
    name = "jira"

    def __init__(self, profile: Profile) -> None:
        login = os.environ.get("JIRA_LOGIN")
        token = os.environ.get("JIRA_API_TOKEN")
        if not login or not token:
            raise UsageError("JIRA_LOGIN and JIRA_API_TOKEN must be set")
        tracker = profile.tracker
        self._profile = profile
        self._base_url = tracker["url"].rstrip("/")
        self._api = f"{self._base_url}/rest/api/2"
        self._project = tracker["project"]
        self._issue_type = tracker["issue_type"]
        _, self._fence_label = profile.fence
        self._states = tracker["states"]
        self._state_labels = {r["label"] for r in self._states.values()
                              if r.get("label")}
        self._session = requests.Session()
        self._session.auth = (login, token)

    def _request(self, method: str, path: str, **kwargs):
        url = f"{self._api}/{path}"
        last_error: Exception | None = None
        for _ in range(2):
            try:
                response = self._session.request(method, url, timeout=30, **kwargs)
                if response.status_code >= 500:
                    last_error = TrackerError(
                        f"jira {method} {path}: HTTP {response.status_code}")
                    continue
                if response.status_code >= 400:
                    raise TrackerError(
                        f"jira {method} {path}: HTTP {response.status_code}",
                        body=response.text[:500])
                return response.json() if response.text else {}
            except requests.RequestException as exc:
                last_error = TrackerError(f"jira {method} {path}: {exc}")
        raise last_error  # type: ignore[misc]

    # --- контракт Provider ---
    def capabilities(self) -> Capabilities:
        return Capabilities(attach=True, link=True)

    def list_cards(self, state: str) -> list[Card]:
        repr_ = self._profile.state_repr(state)
        jql = (f'project = {self._project} AND labels = "{self._fence_label}" '
               f'AND status = "{repr_["status"]}"')
        if repr_.get("label"):
            jql += f' AND labels = "{repr_["label"]}"'
        jql += " ORDER BY created ASC"
        data = self._request("GET", "search",
                             params={"jql": jql, "fields": _FIELDS,
                                     "maxResults": 100})
        return [_issue_to_card(i, self._states, self._fence_label, self._base_url)
                for i in data.get("issues", [])]

    def read_card(self, card_id: str) -> Card:
        issue = self._request("GET", f"issue/{card_id}",
                              params={"fields": _FIELDS})
        return _issue_to_card(issue, self._states, self._fence_label,
                              self._base_url)

    def move(self, card_id: str, state: str) -> None:
        repr_ = self._profile.state_repr(state)
        self._transition_to(card_id, repr_["status"])
        # Смена метки состояния: добавить новую, снять прочие состояние-метки.
        current = set(self.read_card(card_id).labels)
        target = {repr_["label"]} if repr_.get("label") else set()
        stale = (current & self._state_labels) - target
        update = ([{"add": lbl} for lbl in target - current]
                  + [{"remove": lbl} for lbl in stale])
        if update:
            self._request("PUT", f"issue/{card_id}",
                          json={"update": {"labels": update}})

    def comment(self, card_id: str, body: str) -> None:
        self._request("POST", f"issue/{card_id}/comment", json={"body": body})

    def create_card(self, title: str, body: str, state: str) -> str:
        repr_ = self._profile.state_repr(state)
        labels = [self._fence_label] + ([repr_["label"]] if repr_.get("label") else [])
        created = self._request("POST", "issue", json={"fields": {
            "project": {"key": self._project},
            "summary": title,
            "description": body,
            "issuetype": {"name": self._issue_type},
            "labels": labels,
        }})
        key = created.get("key")
        if not key:
            raise TrackerError("jira create issue: no key in response",
                               response=created)
        # Созданная карточка рождается в начальном статусе воркфлоу; довести до целевого.
        if self.read_card(key).raw_state.lower() != repr_["status"].lower():
            self._transition_to(key, repr_["status"])
        return key

    def link(self, card_id: str, other_id: str) -> None:
        self._request("POST", "issueLink", json={
            "type": {"name": "Relates"},
            "inwardIssue": {"key": card_id},
            "outwardIssue": {"key": other_id}})

    def attach(self, card_id: str, file_path: str) -> None:
        # Мультипарт с кастомным заголовком X-Atlassian-Token — нестандартный
        # транспорт, поэтому не через _request. Тело читаем в bytes один раз
        # (урок yougile: живой файловый хендл после первой попытки уже пуст,
        # повторная отправка шлёт пустое тело вместо ретрая).
        path = Path(file_path)
        payload = path.read_bytes()
        url = f"{self._api}/issue/{card_id}/attachments"
        last_error: Exception | None = None
        for _ in range(2):
            try:
                response = self._session.post(
                    url, files={"file": (path.name, payload)},
                    headers={"X-Atlassian-Token": "no-check"}, timeout=60)
                if response.status_code >= 500:
                    last_error = TrackerError(
                        f"jira attach: HTTP {response.status_code}")
                    continue
                if response.status_code >= 400:
                    raise TrackerError(
                        f"jira attach: HTTP {response.status_code}",
                        body=response.text[:500])
                return
            except requests.RequestException as exc:
                last_error = TrackerError(f"jira attach: {exc}")
        raise last_error  # type: ignore[misc]

    def in_fence(self, card: Card) -> bool:
        return self._fence_label in card.labels

    # --- сырые операции для конформанс-сьюта (teardown, посадка чужого) ---
    def _transition_to(self, card_id: str, status_name: str,
                       fields: dict | None = None) -> None:
        # Живой смоук против CRM3 (Task 12): некоторые транзишны воркфлоу несут
        # обязательные поля через post-function валидатор, не объявленные как
        # required в editmeta транзишна (например Cancelled требует resolution
        # и текстовое поле причины) — без них Jira отвечает HTTP 400. Профиль
        # клиента ими не управляет (это деталь конкретного воркфлоу трекера),
        # поэтому полигон/вызывающий код может передать их явно.
        data = self._request("GET", f"issue/{card_id}/transitions")
        transition_id = _pick_transition(data.get("transitions", []), status_name)
        payload: dict = {"transition": {"id": transition_id}}
        if fields:
            payload["fields"] = fields
        self._request("POST", f"issue/{card_id}/transitions", json=payload)

    def _set_labels(self, card_id: str, labels: list[str]) -> None:
        self._request("PUT", f"issue/{card_id}",
                      json={"fields": {"labels": labels}})

    def _raw_create(self, title: str, labels: list[str]) -> str:
        created = self._request("POST", "issue", json={"fields": {
            "project": {"key": self._project}, "summary": title,
            "description": "conformance foreign card",
            "issuetype": {"name": self._issue_type}, "labels": labels}})
        return created["key"]

    def _raw_comment(self, card_id: str, text: str) -> None:
        self._request("POST", f"issue/{card_id}/comment", json={"body": text})
