import asyncio
import hashlib

from app.backend import BackendFailure, CertificateMaterial, MfiBackend
from app.cache import IdempotencyCache


MAXIMUM_RESPONSE_BYTES = 65_525


class MfiService:
    def __init__(self, backend: MfiBackend, ttl_seconds: int, max_entries: int) -> None:
        self._backend = backend
        self._cache = IdempotencyCache(ttl_seconds=ttl_seconds, max_entries=max_entries)
        self._operation_lock = asyncio.Lock()

    async def certificate(self) -> CertificateMaterial:
        async with self._operation_lock:
            material = await self._backend.get_certificate()
        if material.type not in ("mfi", "baa"):
            raise BackendFailure("hardware backend returned an unsupported certificate type")
        if material.protocol_major not in range(256):
            raise BackendFailure("hardware backend returned an invalid protocolMajor")
        if not 1 <= len(material.certificate) <= MAXIMUM_RESPONSE_BYTES:
            raise BackendFailure("hardware backend returned an invalid certificate length")
        return material

    async def sign(self, challenge: bytes, request_id: str) -> bytes:
        challenge_digest = hashlib.sha256(challenge).digest()
        async with self._operation_lock:
            cached = self._cache.get(request_id, challenge_digest)
            if cached is not None:
                return cached
            signature = await self._backend.sign(bytes(challenge), request_id)
            if not 1 <= len(signature) <= MAXIMUM_RESPONSE_BYTES:
                raise BackendFailure("hardware backend returned an invalid signature length")
            self._cache.put(request_id, challenge_digest, signature)
            return bytes(signature)

    async def reset(self) -> str:
        async with self._operation_lock:
            detail = await self._backend.reset()
            if not isinstance(detail, str):
                raise BackendFailure("hardware backend returned an invalid reset detail")
            self._cache.clear()
            return detail
