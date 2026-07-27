import pytest

from office_adapter.errors import UsageError
from office_adapter.marking import detect, mark


def test_mark_prepends_marker_line():
    assert mark("clerk", "hello\nworld") == "[ai-office:clerk]\nhello\nworld"


def test_roundtrip():
    assert detect(mark("clerk", "any text")) == "clerk"


def test_detect_none_for_plain_text():
    assert detect("Обычный коммент владельца") is None
    assert detect("") is None


def test_detect_marker_must_be_first_line():
    assert detect("text\n[ai-office:clerk]") is None


def test_invalid_role_rejected():
    with pytest.raises(UsageError):
        mark("Clerk!", "body")


def test_role_with_trailing_newline_rejected():
    with pytest.raises(UsageError):
        mark("clerk\n", "body")


def test_empty_role_rejected():
    with pytest.raises(UsageError):
        mark("", "body")
