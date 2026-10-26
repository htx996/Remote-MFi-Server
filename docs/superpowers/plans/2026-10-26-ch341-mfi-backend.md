# CH341F + MFI337S3959 Hardware Backend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a GPL-3.0 Go/libusb helper inside the existing Remote-MFi-Server image so the FNOS-hosted CH341F + MFI337S3959 board can provide real certificate and signature operations to xcertplay.

**Architecture:** Keep the Python FastAPI service as the authenticated HTTP/API and idempotency layer. Add a separate Go executable at `/usr/local/bin/mfi-backend`; FastAPI invokes it once per operation through the existing argv + JSON stdin/stdout boundary. The helper opens `1a86:5512` with libusb, performs CH341 I2C stream transactions at 100 kHz, and talks only to configured MFi address `0x11` for business operations.

**Tech Stack:** Python 3.12, FastAPI, pytest, Go 1.23, `github.com/google/gousb`, libusb 1.0, Docker Buildx/QEMU, GitHub Actions, Docker Hub.

**Spec:** `docs/superpowers/specs/2026-10-26-ch341-mfi-backend-design.md`

## Global Constraints

- No software private key, fixed signature, challenge echo, or fabricated hardware success.
- Business operations default to `VID:PID 1a86:5512`, 7-bit I2C address `0x11`, and 100 kHz.
- Never pulse physical MFi RST; reset reopens/probes a USB session and says so explicitly.
- Never log Bearer Token, certificate bytes, challenge bytes, signature bytes, or private material.
- Keep the container non-root as `10001:10001`, with `cap_drop: ALL`, `no-new-privileges`, and read-only root filesystem.
- Keep `/healthz` as HTTP-process health; hardware acceptance is a separate staged probe.
- Provide linux/amd64 and linux/arm64 images; production helper builds require CGO and target-architecture libusb.
- Preserve immutable `1.0.0` for rollback; publish the completed hardware version as `1.1.0` only after verification.
- GPL-derived helper source remains GPL-3.0-only and ships with source, license, attribution, upstream commit IDs, and modification notice.

## Review Focus

- A mapped USB node is present but libusb cannot claim it: helper exits nonzero with a safe permission/busy error and no JSON success object; covered in Task 4 integration seams and FNOS Task 8.
- The CH341 response contains variable ACK-prefix length or a 32-byte packet boundary: data remains tail-aligned and no read waits across a full-packet boundary; covered in Task 2 tests.
- MFi responds at `0x10` but configured `0x11` has no valid certificate: business operations fail instead of silently switching addresses; covered in Task 3 and Task 5 tests.
- An interrupted/timed-out sign operation leaves USB resources open: process cancellation closes the libusb session and exits nonzero; covered in Task 4 tests and Task 5 command tests.
- The FNOS device number changes after reconnect: Compose validation fails clearly until `CH341_DEVICE_PATH` is updated and the container is recreated; documented and checked in Task 7/8.

---

### Task 1: Add the GPL helper module, attribution, and validated hardware configuration

**Files:**
- Create: `hardware-backend/go.mod`
- Create: `hardware-backend/LICENSE`
- Create: `hardware-backend/NOTICE.md`
- Create: `hardware-backend/internal/config/config.go`
- Create: `hardware-backend/internal/config/config_test.go`
- Create: `THIRD_PARTY_NOTICES.md`
- Modify: `.gitignore`

**Interfaces:**
- Consumes: environment variables `CH341_USB_VID`, `CH341_USB_PID`, `MFI_I2C_ADDRESS`, `CH341_I2C_SPEED_KHZ`.
- Produces: `config.Config{USBVendor uint16, USBProduct uint16, I2CAddress uint8, I2CSpeedKHz int}` and `config.FromEnvironment(lookup func(string) (string, bool)) (Config, error)`.

- [ ] **Step 1: Write failing configuration tests**

Add tests named:

- `TestFromEnvironmentUsesBoardDefaults` asserting `0x1a86`, `0x5512`, `0x11`, `100`.
- `TestFromEnvironmentAcceptsExplicitHexValues`.
- `TestFromEnvironmentRejectsOutOfRangeAddressBeforeHardwareAccess`.
- `TestFromEnvironmentRejectsUnsupportedSpeed` for any value outside `20/100/400/750`.
- `TestFromEnvironmentRejectsMalformedVIDPID`.

- [ ] **Step 2: Verify RED**

Run: `cd hardware-backend && CGO_ENABLED=0 go test ./internal/config -run TestFromEnvironment -v`

Expected: FAIL because `internal/config` and `FromEnvironment` do not exist.

- [ ] **Step 3: Implement the minimum configuration parser**

Implement the exact `Config` fields and `FromEnvironment` signature above. Parse Go-style integers (`0x` accepted), apply defaults, and reject invalid values before any transport object can be constructed.

- [ ] **Step 4: Add GPL and provenance files**

Copy the GPL-3.0-only license text to `hardware-backend/LICENSE`. Record these exact sources and commits in `hardware-backend/NOTICE.md` and root `THIRD_PARTY_NOTICES.md`:

- `shilapi/xcertplay@1298fb35e29ee30e7bd849cb0e751b52aa22a13f`
- `cuckoohello/remote-mfi-for-xcertplay@b7bd0852539c00b36c7cd850990abb64cf300ead`

State that the Go helper is a modified independent program and the Python service communicates with it only through a subprocess contract.

- [ ] **Step 5: Verify GREEN**

Run: `cd hardware-backend && CGO_ENABLED=0 go test ./internal/config -v`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add hardware-backend/go.mod hardware-backend/LICENSE hardware-backend/NOTICE.md \
  hardware-backend/internal/config THIRD_PARTY_NOTICES.md .gitignore
git commit -m "chore: establish GPL hardware backend module"
```

### Task 2: Implement and test the CH341 I2C stream codec

**Files:**
- Create: `hardware-backend/internal/ch341/encoder.go`
- Create: `hardware-backend/internal/ch341/encoder_test.go`
- Create: `hardware-backend/internal/ch341/response.go`
- Create: `hardware-backend/internal/ch341/response_test.go`

**Interfaces:**
- Consumes: validated 7-bit address, write bytes, requested read length, and I2C speed.
- Produces: `EncodeConfiguration(speedKHz int) ([]byte, error)`, `EncodeTransaction(address uint8, writeData []byte, readLength int) ([]byte, error)`, `InstructionLengths(segment []byte) (writtenStatusMax int, dataBytes int, err error)`, and `ExtractReadData(received []byte, dataBytes int) ([]byte, error)`.

- [ ] **Step 1: Write failing encoder tests**

Test exact bytes for:

- 100 kHz configuration;
- write-only register selection with STOP;
- pure one-byte read;
- combined write/read request rejection because MFi reads must be separate transactions;
- maximum 128-byte MFi payload split on CH341's 32-byte USB stream boundary;
- invalid address, negative read length, and oversized write/read input.

- [ ] **Step 2: Verify encoder RED**

Run: `cd hardware-backend && CGO_ENABLED=0 go test ./internal/ch341 -run 'TestEncode' -v`

Expected: FAIL because encoder functions do not exist.

- [ ] **Step 3: Implement the CH341 stream encoder**

Use stream marker `0xaa`, START `0x74`, STOP `0x75`, WRITE base `0x80`, READ base `0xc0`, END `0x00`, maximum stream packet 32 bytes, and 7-bit address expansion `(address << 1) | readBit`.

- [ ] **Step 4: Verify encoder GREEN**

Run the same command; expected PASS.

- [ ] **Step 5: Write failing response tests**

Add tests that assert:

- bare `0x80` writes contribute status bytes;
- batched `0x80|n` writes do not force fixed status count;
- read data is aligned from the response tail;
- any leading status byte with bit `0x80` becomes an I2C NACK;
- responses shorter than requested data fail;
- a full 32-byte answer is consumed without asking libusb for byte 33.

- [ ] **Step 6: Verify response RED**

Run: `cd hardware-backend && CGO_ENABLED=0 go test ./internal/ch341 -run 'TestInstruction|TestExtract' -v`

Expected: FAIL because response helpers do not exist.

- [ ] **Step 7: Implement response parsing and verify GREEN**

Implement the signatures above, preserving tail alignment. Run `CGO_ENABLED=0 go test ./internal/ch341 -v`; expected PASS.

- [ ] **Step 8: Commit**

```bash
git add hardware-backend/internal/ch341
git commit -m "feat: encode CH341 I2C stream transactions"
```

### Task 3: Implement the MFI337S3959 register driver with a fake transport

**Files:**
- Create: `hardware-backend/internal/mfi/transport.go`
- Create: `hardware-backend/internal/mfi/driver.go`
- Create: `hardware-backend/internal/mfi/driver_test.go`
- Create: `hardware-backend/internal/mfi/probe.go`
- Create: `hardware-backend/internal/mfi/probe_test.go`

**Interfaces:**
- Consumes: `type I2CTransport interface { Transaction(context.Context, uint8, []byte, int) ([]byte, error); Close() error }`.
- Produces: `NewDriver(transport I2CTransport, address uint8, options ...Option) (*Driver, error)`, `Probe(ctx) (ProbeResult, error)`, `ReadCertificate(ctx) ([]byte, error)`, and `SignChallenge(ctx, challenge []byte) ([]byte, error)`.
- `ProbeResult` fields: `Address uint8`, `DeviceVersion uint8`, `ProtocolMajor uint8`, `CertificateLength uint16`.

- [ ] **Step 1: Write failing read/probe tests with a recording fake transport**

Assert the exact operation sequence:

- select register `0x00`, STOP, then pure one-byte read;
- select `0x02` for protocol major;
- select `0x30` and read two-byte big-endian certificate length;
- configured `0x11` is used for every business transaction;
- an answering `0x10` never changes a configured `0x11` driver;
- invalid device versions `0x00/0xff` and certificate lengths outside `1..1280` fail probe.

- [ ] **Step 2: Verify probe RED**

Run: `cd hardware-backend && CGO_ENABLED=0 go test ./internal/mfi -run 'TestProbe|TestConfiguredAddress' -v`

Expected: FAIL because the MFi driver does not exist.

- [ ] **Step 3: Implement probe and register reads**

Use write-only register selection followed by a separate pure-read transaction. Retry the entire select/read pair for at most 2 seconds at 20 ms intervals; the transport itself enforces the 5 ms select-to-read gap.

- [ ] **Step 4: Verify probe GREEN**

Run the same command; expected PASS.

- [ ] **Step 5: Write failing certificate tests**

Assert a 607-byte certificate is read from windows `0x31` through `0x35` with chunk sizes `128,128,128,128,95`, and that no certificate bytes are logged or included in errors.

- [ ] **Step 6: Implement certificate windows and verify GREEN**

Run: `cd hardware-backend && CGO_ENABLED=0 go test ./internal/mfi -run TestReadCertificate -v`; expected PASS.

- [ ] **Step 7: Write failing signature tests**

Assert:

- 0- and 129-byte challenges are rejected before transport access;
- big-endian length goes to `0x20`, bytes to `0x21`, and `0x01` to `0x10`;
- initial 10 ms delay and 10 ms polling are injectable in tests;
- only status `0x10` succeeds;
- timeout is 3 seconds and best-effort error register is `0x05`;
- signature length is read from `0x11`, data from `0x12`;
- cancellation stops polling.

- [ ] **Step 8: Implement signing and verify the full package**

Run: `cd hardware-backend && CGO_ENABLED=0 go test ./internal/mfi -v`; expected PASS.

- [ ] **Step 9: Commit**

```bash
git add hardware-backend/internal/mfi
git commit -m "feat: implement MFI337 certificate and signing protocol"
```

### Task 4: Add the libusb CH341 transport with deterministic non-CGO tests

**Files:**
- Create: `hardware-backend/internal/ch341/transport.go`
- Create: `hardware-backend/internal/ch341/transport_test.go`
- Create: `hardware-backend/internal/ch341/usb_cgo.go`
- Create: `hardware-backend/internal/ch341/usb_nocgo.go`

**Interfaces:**
- Consumes: `config.Config` and the Task 2 stream helpers.
- Produces: `Open(ctx context.Context, cfg config.Config) (mfi.I2CTransport, error)` in both CGO and non-CGO builds, and a testable internal `bulkSession` abstraction.

- [ ] **Step 1: Write failing transport tests against a fake bulk session**

Assert:

- configure is sent before each I2C transaction;
- pure read waits at least 5 ms after the previous register-select process boundary supplied by the caller;
- transfers are split into 32-byte segments;
- write-only packets may have an empty response;
- read packets require all requested data;
- timeout/cancellation closes the session;
- claim permission/busy errors are safe and contain no payload bytes.

- [ ] **Step 2: Verify transport RED**

Run: `cd hardware-backend && CGO_ENABLED=0 go test ./internal/ch341 -run TestTransport -v`

Expected: FAIL because transport/session types do not exist.

- [ ] **Step 3: Implement the transport core and no-CGO stub**

The no-CGO `Open` returns a clear build-capability error and exists only so pure tests run on hosts without libusb development files. It must never be copied into the production Docker image.

- [ ] **Step 4: Verify core GREEN**

Run: `cd hardware-backend && CGO_ENABLED=0 go test ./... -v`; expected PASS for pure tests.

- [ ] **Step 5: Implement the CGO/gousb adapter**

Enumerate only configured VID/PID, find one interface with bulk IN and OUT endpoints, claim it, and return a session that releases interface/config/device/context on every exit path. Do not detach unrelated kernel drivers or inspect other device strings in normal operation.

- [ ] **Step 6: Compile the CGO adapter in a Linux builder**

Run through the Docker builder added later in Task 6, or in CI with `libusb-1.0-0-dev` and `pkg-config` installed. Expected: `go test -race ./...` exits 0.

- [ ] **Step 7: Commit**

```bash
git add hardware-backend/internal/ch341
git commit -m "feat: access CH341 through libusb"
```

### Task 5: Implement the executable JSON command contract

**Files:**
- Create: `hardware-backend/internal/command/run.go`
- Create: `hardware-backend/internal/command/run_test.go`
- Create: `hardware-backend/cmd/mfi-backend/main.go`

**Interfaces:**
- Consumes: argv operation, UTF-8 JSON stdin, safe stderr logger, `config.FromEnvironment`, and a transport factory.
- Produces: `command.Run(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer, open OpenFunc) int` and production `mfi-backend`.

- [ ] **Step 1: Write failing CLI tests**

Add tests for:

- `--version` prints version metadata without opening USB;
- unknown/missing operation exits nonzero;
- malformed, trailing, or oversized JSON exits nonzero;
- `probe` emits only `usbId`, selected address, device version, protocol major, certificate length, and optional alternate-address response status;
- `certificate` emits `type=mfi`, integer `protocolMajor`, and Base64 real bytes from the fake driver;
- `sign` requires Base64 challenge plus requestId and emits Base64 signature;
- `reset` probes configured `0x11` and returns the exact no-physical-RST detail;
- hardware errors write safe diagnostics to stderr and never emit a JSON success object;
- environment containing `MFI_BEARER_TOKEN` is neither read nor printed.

- [ ] **Step 2: Verify CLI RED**

Run: `cd hardware-backend && CGO_ENABLED=0 go test ./internal/command -v`

Expected: FAIL because `command.Run` does not exist.

- [ ] **Step 3: Implement command dispatch and JSON envelopes**

Decode exactly one JSON object, cap stdin at 4 KiB, use `json.Encoder` for stdout, and send only operation/error metadata to stderr. `reset` must perform a real probe and fail if the configured chip is unavailable.

- [ ] **Step 4: Verify CLI GREEN and full non-CGO suite**

Run:

```bash
cd hardware-backend
CGO_ENABLED=0 go test ./... -v
CGO_ENABLED=0 go vet ./...
```

Expected: both exit 0.

- [ ] **Step 5: Commit**

```bash
git add hardware-backend/internal/command hardware-backend/cmd/mfi-backend
git commit -m "feat: expose hardware backend command contract"
```

### Task 6: Integrate the helper into the Docker image and Python defaults

**Files:**
- Modify: `Dockerfile`
- Modify: `.dockerignore`
- Modify: `.env.example`
- Modify: `docker-compose.yml`
- Modify: `docker-compose.usb.yml`
- Modify: `tests/test_command_backend.py`
- Create: `tests/test_container_contract.py`

**Interfaces:**
- Consumes: helper source and binary from Tasks 1-5.
- Produces: image containing `/usr/local/bin/mfi-backend`, libusb runtime, Python service user `10001:10001`, and default `MFI_BACKEND_COMMAND=/usr/local/bin/mfi-backend`.

- [ ] **Step 1: Write failing Python/static container-contract tests**

Assert:

- `.env.example` points to `/usr/local/bin/mfi-backend`;
- Dockerfile has a Go/libusb builder and copies the helper into the final image;
- final image still declares `USER 10001:10001` after all root package installation;
- Compose retains exact `devices` and numeric `group_add`, not `privileged: true` or the whole USB bus;
- a custom `/opt/remote-mfi/backend/mfi-backend` remains possible by environment override.

- [ ] **Step 2: Verify RED**

Run: `.venv312/bin/pytest tests/test_container_contract.py -q`

Expected: FAIL because the helper is not yet built into Dockerfile and the default path is old.

- [ ] **Step 3: Implement the multi-stage Docker build**

Use a target-platform Go 1.23 Debian stage with `libusb-1.0-0-dev` and `pkg-config`; build with CGO enabled. Install only `libusb-1.0-0` in the Python runtime stage, copy the helper, then switch to `USER 10001:10001`.

- [ ] **Step 4: Update configuration and Compose comments**

Set the built-in path as default; retain the optional external backend mount and document that it is used only when the administrator overrides `MFI_BACKEND_COMMAND`.

- [ ] **Step 5: Verify Python and static contracts GREEN**

Run: `.venv312/bin/pytest -q`

Expected: all tests pass with no warnings treated as failures.

- [ ] **Step 6: Build and inspect a local image where Docker is available**

Run:

```bash
docker build -t remote-mfi-server:hardware-local .
docker run --rm remote-mfi-server:hardware-local /usr/local/bin/mfi-backend --version
docker image inspect remote-mfi-server:hardware-local --format '{{json .Config.User}}'
```

Expected: helper prints version; image user is `10001:10001`. If the current workstation lacks Docker, this gate moves to GitHub Actions and must not be reported as locally verified.

- [ ] **Step 7: Commit**

```bash
git add Dockerfile .dockerignore .env.example docker-compose.yml docker-compose.usb.yml \
  tests/test_command_backend.py tests/test_container_contract.py
git commit -m "feat: bundle CH341 MFI backend in Docker image"
```

### Task 7: Update documentation and CI/release gates

**Files:**
- Modify: `README.md`
- Modify: `docs/hardware-backend-contract.md`
- Modify: `docs/design.md`
- Modify: `.github/workflows/docker-publish.yml`

**Interfaces:**
- Consumes: finished helper and container contract.
- Produces: reproducible test/build/publish workflow and exact FNOS deployment/rollback instructions.

- [ ] **Step 1: Add documentation assertions to `tests/test_container_contract.py` and verify RED**

Assert README contains:

- board identity and exact iBOM-supported model;
- `probe` command;
- `/healthz` versus hardware-ready distinction;
- no physical RST pulse;
- device-number-change recovery;
- `1.0.0` rollback command;
- GPL helper source/provenance link.

Run the focused test and expect failure before editing docs.

- [ ] **Step 2: Update Chinese deployment and hardware acceptance documentation**

Document the five acceptance layers and never print certificate/signature bodies in examples. Use lengths and SHA-256 only.

- [ ] **Step 3: Add CI test gates before publish**

Add a `test` job that installs Python requirements plus Linux `libusb-1.0-0-dev/pkg-config`, runs Python pytest, Go `go test -race ./...`, and Go vet. Make the publish job depend on it. Keep Buildx QEMU and the existing amd64/arm64 platform list, provenance, and SBOM.

- [ ] **Step 4: Add post-build architecture smoke checks**

For non-push verification or a temporary local output, run each target image's `mfi-backend --version`. Do not claim arm64 runtime verification from manifest metadata alone.

- [ ] **Step 5: Run full repository verification**

Run:

```bash
.venv312/bin/pytest -q
cd hardware-backend && CGO_ENABLED=0 go test ./... -v && CGO_ENABLED=0 go vet ./...
git diff --check
```

Expected: all commands exit 0.

- [ ] **Step 6: Commit design, plan, docs, and workflow**

```bash
git add README.md docs .github/workflows/docker-publish.yml
git commit -m "docs: add CH341 hardware deployment and release gates"
```

### Task 8: Publish version 1.1.0 and perform staged FNOS hardware validation

**Files:**
- No new source files expected.
- External state: GitHub repository, Docker Hub tags, FNOS Compose project.

**Interfaces:**
- Consumes: verified main branch commit and the existing authorized GitHub/Docker Hub connections.
- Produces: immutable `1.1.0` multiarch image and evidence for each hardware acceptance layer.

- [ ] **Step 1: Review repository state before external writes**

Run `git status --short`, `git diff --stat`, `git log -5 --oneline`, and re-run all Task 7 verification commands. Stop on unrelated or uncommitted changes.

- [ ] **Step 2: Push source and wait for CI**

Push the verified commits to `origin/main`. Observe the workflow to terminal success; inspect failing logs before any tag or NAS deployment.

- [ ] **Step 3: Tag and publish `v1.1.0`**

Create and push the annotated tag only after main CI succeeds. Confirm Docker Hub manifests contain linux/amd64 and linux/arm64 and record the manifest digest. Attach the resulting GitHub release/PR artifacts to the task when applicable.

- [ ] **Step 4: Preserve and update FNOS deployment**

Back up the current Compose and `.env` without printing the token. Change only:

- image tag to `hanfu1997/remote-mfi-server:1.1.0`;
- `MFI_BACKEND_COMMAND=/usr/local/bin/mfi-backend`;
- hardware defaults if not already present;
- current exact device path and group `0`.

Recreate only `remote-mfi-server`; do not restart the Docker daemon or unrelated containers.

- [ ] **Step 5: Verify USB layer**

Use filtered inspect output to confirm user, health, exact device mapping, group `0`, and non-privileged status. Inside the container, confirm the node exists and is readable/writable. Do not print environment variables.

- [ ] **Step 6: Run read-only hardware probe**

Run `mfi-backend probe` with `{}` stdin. Accept only configured address `0x11`, nonzero/non-`0xff` device version, plausible protocol major, and certificate length `1..1280`. On failure, collect safe stderr and stop.

- [ ] **Step 7: Read and verify the real certificate**

Call authenticated `/mfi/certificate` without displaying its `certificate` field. Report only HTTP status, type, protocol major, decoded byte length, and server-provided SHA-256. Repeat once and require the same SHA-256.

- [ ] **Step 8: Run one controlled real signature operation**

Warn before this write-to-chip step. Submit one 32-byte challenge with a UUID requestId. Report only HTTP status, decoded signature length, and SHA-256. Retry the same requestId/challenge and verify the idempotent response is identical without assuming that proves cryptographic validity.

- [ ] **Step 9: Perform xcertplay/iPhone end-to-end acceptance**

Configure xcertplay base URL and Bearer Token. Final success requires xcertplay/iPhone evidence of `AA05 AuthenticationSucceeded`; until the user supplies that evidence, report the result as hardware-signature verified but end-to-end pending.

- [ ] **Step 10: Roll back on any failed gate**

Set the image back to `hanfu1997/remote-mfi-server:1.0.0`, restore the previous `MFI_BACKEND_COMMAND`, recreate only this service, and verify `/healthz`. Keep logs and source for diagnosis; do not delete the failed image or user data.
