from typing import Literal

from pydantic import BaseModel, ConfigDict, Field


class StrictModel(BaseModel):
    model_config = ConfigDict(extra="forbid")


class HealthResponse(BaseModel):
    status: Literal["ok"] = "ok"


class CertificateResponse(BaseModel):
    type: Literal["mfi", "baa"]
    protocolMajor: int
    certificate: str
    certificateSha256: str


class SignRequest(StrictModel):
    challenge: str = Field(min_length=1, max_length=1024)
    requestId: str = Field(min_length=1, max_length=128)


class SignResponse(BaseModel):
    signature: str


class ResetRequest(StrictModel):
    pass


class DetailResponse(BaseModel):
    detail: str
