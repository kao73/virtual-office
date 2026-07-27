"""Ошибки адаптера: стабильные коды и коды выхода процесса (спека, «Обработка ошибок»)."""
from __future__ import annotations


class AdapterError(Exception):
    code = "tracker_error"
    exit_code = 2

    def __init__(self, message: str, **details: object) -> None:
        super().__init__(message)
        self.message = message
        self.details = details

    def to_json(self) -> dict:
        return {"error": self.code, "message": self.message, "details": self.details}


class UsageError(AdapterError):
    code = "usage_error"
    exit_code = 1


class ProfileError(AdapterError):
    code = "profile_error"
    exit_code = 1


class TrackerError(AdapterError):
    code = "tracker_error"
    exit_code = 2


class VerificationFailed(AdapterError):
    code = "verification_failed"
    exit_code = 3


class FenceViolation(AdapterError):
    code = "fence_violation"
    exit_code = 4


class CapabilityMissing(AdapterError):
    code = "capability_missing"
    exit_code = 5
