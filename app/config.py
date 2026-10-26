from pathlib import Path
from typing import Literal

from pydantic import Field
from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(
        env_file=".env",
        env_file_encoding="utf-8",
        case_sensitive=False,
        extra="ignore",
    )

    mfi_auth_required: bool = True
    mfi_bearer_token: str = ""

    mfi_certificate_file: Path | None = None
    mfi_certificate_base64: str = ""
    mfi_certificate_type: Literal["mfi", "baa"] = "mfi"
    mfi_protocol_major: int = Field(default=3, ge=0, le=255)

    mfi_backend_command: str = "/usr/local/bin/mfi-backend"
    mfi_backend_timeout_seconds: float = Field(default=8.0, gt=0, le=120)

    idempotency_ttl_seconds: int = Field(default=300, ge=1, le=86_400)
    idempotency_max_entries: int = Field(default=1024, ge=1, le=100_000)
    max_request_body_bytes: int = Field(default=4096, ge=512, le=65_536)
    log_level: Literal["DEBUG", "INFO", "WARNING", "ERROR", "CRITICAL"] = "INFO"
