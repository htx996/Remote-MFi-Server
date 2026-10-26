import json
from collections.abc import Awaitable, Callable
from typing import Any


AsgiApp = Callable[[dict[str, Any], Callable, Callable], Awaitable[None]]


class RequestBodyLimitMiddleware:
    """Reject request bodies before JSON parsing, including streamed bodies."""

    def __init__(self, app: AsgiApp, max_body_bytes: int) -> None:
        self._app = app
        self._max_body_bytes = max_body_bytes

    async def __call__(self, scope: dict[str, Any], receive: Callable, send: Callable) -> None:
        if scope.get("type") != "http":
            await self._app(scope, receive, send)
            return

        headers = {name.lower(): value for name, value in scope.get("headers", [])}
        raw_length = headers.get(b"content-length")
        if raw_length is not None:
            try:
                content_length = int(raw_length)
            except ValueError:
                content_length = 0
            if content_length > self._max_body_bytes:
                await self._send_too_large(send)
                return

        body = bytearray()
        while True:
            message = await receive()
            if message.get("type") == "http.disconnect":
                return
            if message.get("type") != "http.request":
                continue
            body.extend(message.get("body", b""))
            if len(body) > self._max_body_bytes:
                await self._send_too_large(send)
                return
            if not message.get("more_body", False):
                break

        delivered = False

        async def replay_receive() -> dict[str, Any]:
            nonlocal delivered
            if not delivered:
                delivered = True
                return {"type": "http.request", "body": bytes(body), "more_body": False}
            return {"type": "http.disconnect"}

        await self._app(scope, replay_receive, send)

    async def _send_too_large(self, send: Callable) -> None:
        body = json.dumps(
            {"detail": f"request body exceeds {self._max_body_bytes} bytes"},
            separators=(",", ":"),
        ).encode("utf-8")
        await send(
            {
                "type": "http.response.start",
                "status": 413,
                "headers": [
                    (b"content-type", b"application/json"),
                    (b"content-length", str(len(body)).encode("ascii")),
                ],
            },
        )
        await send({"type": "http.response.body", "body": body})
