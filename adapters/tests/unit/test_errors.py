from office_adapter.errors import (
    AdapterError, UsageError, ProfileError, TrackerError,
    VerificationFailed, FenceViolation, CapabilityMissing,
)


def test_exit_codes_match_spec():
    assert UsageError("x").exit_code == 1
    assert ProfileError("x").exit_code == 1
    assert TrackerError("x").exit_code == 2
    assert VerificationFailed("x").exit_code == 3
    assert FenceViolation("x").exit_code == 4
    assert CapabilityMissing("x").exit_code == 5


def test_to_json_shape():
    err = FenceViolation("card outside fence", card_id="X-1")
    assert err.to_json() == {
        "error": "fence_violation",
        "message": "card outside fence",
        "details": {"card_id": "X-1"},
    }


def test_all_are_adapter_errors():
    for cls in (UsageError, ProfileError, TrackerError,
                VerificationFailed, FenceViolation, CapabilityMissing):
        assert issubclass(cls, AdapterError)
