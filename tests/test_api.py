import base64
import hashlib

import httpx
import pytest
from fastapi.testclient import TestClient

from app.backend import CertificateMaterial
from app.config import Settings
from app.main import create_app


TEST_TOKEN = "test-token-0123456789abcdef0123456789abcdef"


class RecordingBackend:
    def __init__(self) -> None:
        self.sign_calls: list[tuple[bytes, str]] = []
        self.reset_calls = 0

    async def get_certificate(self) -> CertificateMaterial:
        return CertificateMaterial(type="mfi", protocol_major=3, certificate=b"certificate-bytes")

    async def sign(self, challenge: bytes, request_id: str) -> bytes:
        self.sign_calls.append((challenge, request_id))
        return b"hardware-signature"

    async def reset(self) -> str:
        self.reset_calls += 1
        return ""


def make_client(backend: RecordingBackend | None = None) -> tuple[TestClient, RecordingBackend]:
    selected = backend or RecordingBackend()
    settings = Settings(
        mfi_bearer_token=TEST_TOKEN,
        mfi_auth_required=True,
        idempotency_ttl_seconds=300,
        idempotency_max_entries=32,
    )
    return TestClient(create_app(settings=settings, backend=selected)), selected


def auth_headers() -> dict[str, str]:
    return {"Authorization": f"Bearer {TEST_TOKEN}"}


def test_health_is_available_without_bearer_token() -> None:
    client, _ = make_client()
    response = client.get("/healthz")
    assert response.status_code == 200
    assert response.json() == {"status": "ok"}


def test_mfi_route_rejects_missing_bearer_token_with_string_detail() -> None:
    client, _ = make_client()
    response = client.get("/mfi/certificate")
    assert response.status_code == 401
    assert response.json() == {"detail": "missing bearer token"}
    assert response.headers["www-authenticate"] == "Bearer"


def test_mfi_route_rejects_wrong_bearer_token() -> None:
    client, _ = make_client()
    response = client.get(
        "/mfi/certificate",
        headers={"Authorization": "Bearer wrong-token"},
    )
    assert response.status_code == 401
    assert response.json() == {"detail": "invalid bearer token"}


def test_short_configured_token_fails_closed() -> None:
    backend = RecordingBackend()
    settings = Settings(mfi_bearer_token="short", mfi_auth_required=True)
    client = TestClient(create_app(settings=settings, backend=backend))

    response = client.get(
        "/mfi/certificate",
        headers={"Authorization": "Bearer short"},
    )

    assert response.status_code == 503
    assert response.json() == {"detail": "server bearer token is not configured securely"}


def test_certificate_response_matches_xcertplay_contract() -> None:
    client, _ = make_client()
    response = client.get("/mfi/certificate", headers=auth_headers())
    assert response.status_code == 200
    assert response.json() == {
        "type": "mfi",
        "protocolMajor": 3,
        "certificate": base64.b64encode(b"certificate-bytes").decode("ascii"),
        "certificateSha256": hashlib.sha256(b"certificate-bytes").hexdigest(),
    }


def test_sign_response_matches_xcertplay_contract_and_is_idempotent() -> None:
    client, backend = make_client()
    payload = {
        "challenge": base64.b64encode(b"challenge").decode("ascii"),
        "requestId": "request-1",
    }
    first = client.post("/mfi/sign", headers=auth_headers(), json=payload)
    second = client.post("/mfi/sign", headers=auth_headers(), json=payload)
    assert first.status_code == 200
    assert second.status_code == 200
    expected = {"signature": base64.b64encode(b"hardware-signature").decode("ascii")}
    assert first.json() == expected
    assert second.json() == expected
    assert backend.sign_calls == [(b"challenge", "request-1")]


def test_request_id_reuse_with_a_different_challenge_returns_conflict() -> None:
    client, backend = make_client()
    first = {
        "challenge": base64.b64encode(b"first").decode("ascii"),
        "requestId": "same-id",
    }
    changed = {
        "challenge": base64.b64encode(b"changed").decode("ascii"),
        "requestId": "same-id",
    }
    assert client.post("/mfi/sign", headers=auth_headers(), json=first).status_code == 200
    response = client.post("/mfi/sign", headers=auth_headers(), json=changed)
    assert response.status_code == 409
    assert response.json() == {"detail": "requestId was already used with another challenge"}
    assert backend.sign_calls == [(b"first", "same-id")]


def test_invalid_base64_is_rejected_before_hardware_is_called() -> None:
    client, backend = make_client()
    response = client.post(
        "/mfi/sign",
        headers=auth_headers(),
        json={"challenge": "not-base64!", "requestId": "bad-input"},
    )
    assert response.status_code == 422
    assert response.json() == {"detail": "challenge must be valid standard Base64"}
    assert backend.sign_calls == []


def test_oversized_body_is_rejected_before_authentication_or_parsing() -> None:
    client, backend = make_client()

    response = client.post(
        "/mfi/sign",
        headers={"Content-Type": "application/json"},
        content=b"{" + b" " * 5000 + b"}",
    )

    assert response.status_code == 413
    assert response.json() == {"detail": "request body exceeds 4096 bytes"}
    assert backend.sign_calls == []


@pytest.mark.asyncio
async def test_streamed_oversized_body_without_content_length_is_rejected() -> None:
    backend = RecordingBackend()
    settings = Settings(mfi_bearer_token=TEST_TOKEN, max_request_body_bytes=4096)
    application = create_app(settings=settings, backend=backend)

    async def oversized_stream():
        yield b"{" + b" " * 3000
        yield b" " * 3000 + b"}"

    async with httpx.AsyncClient(
        transport=httpx.ASGITransport(app=application),
        base_url="http://test",
    ) as client:
        response = await client.post(
            "/mfi/sign",
            headers={"Content-Type": "application/json"},
            content=oversized_stream(),
        )

    assert response.status_code == 413
    assert response.json() == {"detail": "request body exceeds 4096 bytes"}
    assert backend.sign_calls == []


def test_reset_returns_detail_and_clears_idempotency_cache() -> None:
    client, backend = make_client()
    payload = {
        "challenge": base64.b64encode(b"challenge").decode("ascii"),
        "requestId": "request-1",
    }
    assert client.post("/mfi/sign", headers=auth_headers(), json=payload).status_code == 200
    response = client.post("/mfi/reset", headers=auth_headers(), json={})
    repeated = client.post("/mfi/sign", headers=auth_headers(), json=payload)
    assert response.status_code == 200
    assert response.json() == {"detail": ""}
    assert repeated.status_code == 200
    assert backend.reset_calls == 1
    assert backend.sign_calls == [
        (b"challenge", "request-1"),
        (b"challenge", "request-1"),
    ]
