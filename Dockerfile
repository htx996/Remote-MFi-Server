FROM --platform=$TARGETPLATFORM golang:1.23-bookworm AS hardware-builder

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

RUN apt-get update \
    && apt-get install --yes --no-install-recommends libusb-1.0-0-dev pkg-config \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src/hardware-backend

COPY hardware-backend/go.mod hardware-backend/go.sum ./
RUN go mod download

COPY hardware-backend/ ./

RUN CGO_ENABLED=1 go build \
      -trimpath \
      -ldflags "-s -w \
        -X github.com/htx996/remote-mfi-server/hardware-backend/internal/command.Version=${VERSION} \
        -X github.com/htx996/remote-mfi-server/hardware-backend/internal/command.Commit=${COMMIT} \
        -X github.com/htx996/remote-mfi-server/hardware-backend/internal/command.BuildDate=${BUILD_DATE}" \
      -o /out/mfi-backend \
      ./cmd/mfi-backend \
    && /out/mfi-backend --version

FROM python:3.12-slim

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1

WORKDIR /srv/remote-mfi

RUN apt-get update \
    && apt-get install --yes --no-install-recommends libusb-1.0-0 \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 remote-mfi \
    && useradd --uid 10001 --gid 10001 --no-create-home --shell /usr/sbin/nologin remote-mfi

COPY requirements.txt ./
RUN python -m pip install --no-cache-dir --requirement requirements.txt

COPY --chown=10001:10001 app ./app
COPY --from=hardware-builder /out/mfi-backend /usr/local/bin/mfi-backend
COPY --from=hardware-builder /src/hardware-backend /usr/share/doc/remote-mfi/hardware-backend
COPY THIRD_PARTY_NOTICES.md /usr/share/doc/remote-mfi/THIRD_PARTY_NOTICES.md

USER 10001:10001

EXPOSE 8000

CMD ["python", "-m", "uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8000", "--workers", "1", "--no-access-log"]
