import base64
import binascii
import hashlib
import logging
import time
import uuid
from typing import Annotated

from fastapi import Depends, FastAPI, HTTPException, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse

from app.auth import bearer_dependency
from app.backend import BackendFailure, BackendUnavailable, MfiBackend
from app.body_limit import RequestBodyLimitMiddleware
from app.cache import IdempotencyConflict
from app.command_backend import CommandBackend
from app.config import Settings
from app.logging_config import configure_logging
from app.models import (
    CertificateResponse,
    DetailResponse,
    HealthResponse,
    ResetRequest,
    SignRequest,
    SignResponse,
)
from app.service import MfiService


LOGGER = logging.getLogger("remote_mfi.api")


def _decode_challenge(encoded: str) -> bytes:
    try:
        challenge = base64.b64decode(encoded, validate=True)
    except (binascii.Error, ValueError) as error:
        raise HTTPException(
            status_code=422,
            detail="challenge must be valid standard Base64",
        ) from error
    if not 1 <= len(challenge) <= 128:
        raise HTTPException(status_code=422, detail="challenge must decode to 1..128 bytes")
    return challenge


def create_app(settings: Settings | None = None, backend: MfiBackend | None = None) -> FastAPI:
    selected_settings = settings or Settings()
    configure_logging(selected_settings.log_level)
    selected_backend = backend or CommandBackend(selected_settings)
    service = MfiService(
        selected_backend,
        ttl_seconds=selected_settings.idempotency_ttl_seconds,
        max_entries=selected_settings.idempotency_max_entries,
    )
    require_bearer = bearer_dependency(selected_settings)

    application = FastAPI(
        title="Remote MFi Server",
        version="1.1.0",
        docs_url=None,
        redoc_url=None,
        openapi_url=None,
    )

    @application.middleware("http")
    async def request_logging(request: Request, call_next):
        request_id = request.headers.get("X-Request-ID", "")[:128] or str(uuid.uuid4())
        started = time.perf_counter()
        status_code = 500
        try:
            response = await call_next(request)
            status_code = response.status_code
            response.headers["X-Request-ID"] = request_id
            return response
        finally:
            LOGGER.info(
                "request completed",
                extra={
                    "request_id": request_id,
                    "method": request.method,
                    "path": request.url.path,
                    "status_code": status_code,
                    "duration_ms": round((time.perf_counter() - started) * 1000, 2),
                },
            )

    application.add_middleware(
        RequestBodyLimitMiddleware,
        max_body_bytes=selected_settings.max_request_body_bytes,
    )

    @application.exception_handler(RequestValidationError)
    async def validation_error_handler(_request: Request, error: RequestValidationError):
        first = error.errors()[0] if error.errors() else {}
        location = ".".join(str(part) for part in first.get("loc", [])[1:]) or "request"
        message = first.get("msg", "invalid request")
        return JSONResponse(status_code=422, content={"detail": f"{location}: {message}"})

    @application.exception_handler(IdempotencyConflict)
    async def idempotency_error_handler(_request: Request, error: IdempotencyConflict):
        return JSONResponse(status_code=409, content={"detail": str(error)})

    @application.exception_handler(BackendUnavailable)
    async def unavailable_handler(_request: Request, error: BackendUnavailable):
        return JSONResponse(status_code=503, content={"detail": str(error)})

    @application.exception_handler(BackendFailure)
    async def backend_failure_handler(_request: Request, error: BackendFailure):
        return JSONResponse(status_code=502, content={"detail": str(error)})

    @application.get("/healthz", response_model=HealthResponse)
    async def health() -> HealthResponse:
        return HealthResponse()

    Protected = Annotated[None, Depends(require_bearer)]

    @application.get("/mfi/certificate", response_model=CertificateResponse)
    async def certificate(_authorized: Protected) -> CertificateResponse:
        material = await service.certificate()
        return CertificateResponse(
            type=material.type,
            protocolMajor=material.protocol_major,
            certificate=base64.b64encode(material.certificate).decode("ascii"),
            certificateSha256=hashlib.sha256(material.certificate).hexdigest(),
        )

    @application.post("/mfi/sign", response_model=SignResponse)
    async def sign(request: SignRequest, _authorized: Protected) -> SignResponse:
        challenge = _decode_challenge(request.challenge)
        signature = await service.sign(challenge, request.requestId)
        return SignResponse(signature=base64.b64encode(signature).decode("ascii"))

    @application.post("/mfi/reset", response_model=DetailResponse)
    async def reset(_request: ResetRequest, _authorized: Protected) -> DetailResponse:
        return DetailResponse(detail=await service.reset())

    return application


app = create_app()
