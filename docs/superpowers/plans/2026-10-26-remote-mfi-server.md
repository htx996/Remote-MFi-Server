# Remote MFi Server Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Docker-deployable, xcertplay-compatible Remote MFi API framework without fabricating the hardware signing protocol.

**Architecture:** FastAPI exposes authenticated endpoints and delegates certificate, sign, and reset operations to an async backend contract. A command backend exchanges JSON with an administrator-supplied hardware helper; an in-memory service layer serializes chip access and caches signatures by requestId.

**Tech Stack:** Python 3.12, FastAPI, Pydantic Settings, Uvicorn, pytest, Docker Compose

**Spec:** `docs/design.md`

## Global Constraints

- Match xcertplay commit `1298fb35e29ee30e7bd849cb0e751b52aa22a13f` Remote MFi JSON fields.
- Never implement or simulate a real MFi signature.
- Decoded challenges are 1–128 bytes; certificates and signatures are non-empty and at most 65,525 bytes.
- Failure responses use a string `detail` field.
- Do not log secrets or cryptographic payloads.

## Review Focus

- A repeated requestId with changed challenge must fail instead of returning a cached signature.
- Concurrent retries must invoke the single hardware backend only once.
- Invalid Base64 and oversized payloads must fail before hardware invocation.
- An unset backend must report a clear 503, never a fake success.
- Reset must clear the completed-signature idempotency cache only after backend reset succeeds.

---

### Task 1: HTTP contract, authentication, and certificate response

**Files:**
- Create: `app/config.py`, `app/auth.py`, `app/models.py`, `app/main.py`
- Test: `tests/test_api.py`

**Interfaces:**
- Consumes: `Settings` and `MfiBackend` supplied to `create_app`.
- Produces: `create_app(settings, backend) -> FastAPI` and protected MFi routes.

- [ ] Write endpoint tests first for health, missing/wrong/correct Bearer token, certificate fields, and string-detail validation failures.
- [ ] Run tests and verify import/behavior failures.
- [ ] Implement the minimum HTTP layer and response models.
- [ ] Run the focused tests and verify they pass.

### Task 2: Serialized hardware service and requestId idempotency

**Files:**
- Create: `app/service.py`, `app/cache.py`, `app/backend.py`
- Test: `tests/test_api.py`, `tests/test_service.py`

**Interfaces:**
- Consumes: async `MfiBackend.get_certificate/sign/reset` methods.
- Produces: validated, serialized `MfiService` operations and TTL/LRU request cache.

- [ ] Write tests first for cache hits, conflict, concurrent duplicate calls, validation, and reset cache clearing.
- [ ] Run tests and verify missing behavior failures.
- [ ] Implement the minimum service and backend protocol.
- [ ] Run focused and full tests.

### Task 3: External command adapter, container, and operator documentation

**Files:**
- Create: `app/command_backend.py`, `tests/test_command_backend.py`
- Create: `Dockerfile`, `docker-compose.yml`, `docker-compose.usb.yml`, `.env.example`, `README.md`, `docs/hardware-backend-contract.md`

**Interfaces:**
- Consumes: exact JSON operation contract described in the hardware backend document.
- Produces: no-shell subprocess adapter and deployable image/Compose project.

- [ ] Write command-adapter tests first for success, malformed output, non-zero exit, and timeout.
- [ ] Run tests and verify failures.
- [ ] Implement the adapter and wire the default application factory.
- [ ] Add hardened container files and Chinese FNOS deployment instructions.
- [ ] Run full tests, compile checks, Compose config validation when Docker is available, image build when Docker is available, and ZIP integrity checks.
