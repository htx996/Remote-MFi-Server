from collections import OrderedDict
from dataclasses import dataclass
from time import monotonic


class IdempotencyConflict(RuntimeError):
    pass


@dataclass(frozen=True, slots=True)
class CacheEntry:
    challenge_digest: bytes
    signature: bytes
    expires_at: float


class IdempotencyCache:
    def __init__(self, ttl_seconds: int, max_entries: int) -> None:
        self._ttl_seconds = ttl_seconds
        self._max_entries = max_entries
        self._entries: OrderedDict[str, CacheEntry] = OrderedDict()

    def get(self, request_id: str, challenge_digest: bytes) -> bytes | None:
        self._prune_expired()
        entry = self._entries.get(request_id)
        if entry is None:
            return None
        if entry.challenge_digest != challenge_digest:
            raise IdempotencyConflict("requestId was already used with another challenge")
        self._entries.move_to_end(request_id)
        return bytes(entry.signature)

    def put(self, request_id: str, challenge_digest: bytes, signature: bytes) -> None:
        self._prune_expired()
        self._entries[request_id] = CacheEntry(
            challenge_digest=bytes(challenge_digest),
            signature=bytes(signature),
            expires_at=monotonic() + self._ttl_seconds,
        )
        self._entries.move_to_end(request_id)
        while len(self._entries) > self._max_entries:
            self._entries.popitem(last=False)

    def clear(self) -> None:
        self._entries.clear()

    def _prune_expired(self) -> None:
        now = monotonic()
        expired = [key for key, entry in self._entries.items() if entry.expires_at <= now]
        for key in expired:
            self._entries.pop(key, None)
