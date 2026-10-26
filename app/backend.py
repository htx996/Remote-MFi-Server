from dataclasses import dataclass
from typing import Literal, Protocol


CertificateType = Literal["mfi", "baa"]


@dataclass(frozen=True, slots=True)
class CertificateMaterial:
    type: CertificateType
    protocol_major: int
    certificate: bytes


class BackendError(RuntimeError):
    """Base class for hardware backend errors safe to map to an HTTP detail."""


class BackendUnavailable(BackendError):
    """The hardware backend is absent, busy, or timed out."""


class BackendFailure(BackendError):
    """The configured backend ran but returned an invalid or failed result."""


class MfiBackend(Protocol):
    async def get_certificate(self) -> CertificateMaterial: ...

    async def sign(self, challenge: bytes, request_id: str) -> bytes: ...

    async def reset(self) -> str: ...
