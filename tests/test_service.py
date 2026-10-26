import asyncio

import pytest

from app.backend import CertificateMaterial
from app.cache import IdempotencyConflict
from app.service import MfiService


class SlowBackend:
    def __init__(self) -> None:
        self.sign_calls = 0

    async def get_certificate(self) -> CertificateMaterial:
        return CertificateMaterial(type="mfi", protocol_major=3, certificate=b"certificate")

    async def sign(self, challenge: bytes, request_id: str) -> bytes:
        self.sign_calls += 1
        await asyncio.sleep(0.02)
        return b"signature"

    async def reset(self) -> str:
        return ""


@pytest.mark.asyncio
async def test_concurrent_duplicate_request_invokes_backend_once() -> None:
    backend = SlowBackend()
    service = MfiService(backend, ttl_seconds=300, max_entries=32)
    results = await asyncio.gather(
        service.sign(b"challenge", "same-request"),
        service.sign(b"challenge", "same-request"),
    )
    assert results == [b"signature", b"signature"]
    assert backend.sign_calls == 1


@pytest.mark.asyncio
async def test_request_id_conflict_is_detected() -> None:
    backend = SlowBackend()
    service = MfiService(backend, ttl_seconds=300, max_entries=32)
    await service.sign(b"first", "same-request")
    with pytest.raises(IdempotencyConflict):
        await service.sign(b"second", "same-request")
    assert backend.sign_calls == 1
