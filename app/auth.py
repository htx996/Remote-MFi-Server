import secrets
from collections.abc import Callable
from typing import Annotated

from fastapi import Depends, HTTPException, status
from fastapi.security import HTTPAuthorizationCredentials, HTTPBearer

from app.config import Settings


MINIMUM_TOKEN_BYTES = 32
UNSAFE_TOKEN_VALUES = {"replace-with-a-long-random-token"}


def bearer_dependency(settings: Settings) -> Callable:
    bearer = HTTPBearer(auto_error=False)

    async def require_bearer(
        credentials: Annotated[
            HTTPAuthorizationCredentials | None,
            Depends(bearer),
        ],
    ) -> None:
        if not settings.mfi_auth_required:
            return
        token_bytes = settings.mfi_bearer_token.encode("utf-8")
        if (
            len(token_bytes) < MINIMUM_TOKEN_BYTES
            or settings.mfi_bearer_token in UNSAFE_TOKEN_VALUES
        ):
            raise HTTPException(
                status_code=status.HTTP_503_SERVICE_UNAVAILABLE,
                detail="server bearer token is not configured securely",
            )
        if credentials is None or credentials.scheme.lower() != "bearer":
            raise HTTPException(
                status_code=status.HTTP_401_UNAUTHORIZED,
                detail="missing bearer token",
                headers={"WWW-Authenticate": "Bearer"},
            )
        if not secrets.compare_digest(credentials.credentials, settings.mfi_bearer_token):
            raise HTTPException(
                status_code=status.HTTP_401_UNAUTHORIZED,
                detail="invalid bearer token",
                headers={"WWW-Authenticate": "Bearer"},
            )

    return require_bearer
