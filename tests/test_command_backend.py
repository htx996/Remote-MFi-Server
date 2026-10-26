import base64
import asyncio
import os
import stat
import time
from pathlib import Path

import pytest

from app.backend import BackendFailure, BackendUnavailable
from app.command_backend import CommandBackend
from app.config import Settings


def write_executable(path: Path, source: str) -> Path:
    path.write_text(source, encoding="utf-8")
    path.chmod(path.stat().st_mode | stat.S_IXUSR)
    return path


def make_settings(command: Path, timeout: float = 1.0) -> Settings:
    return Settings(
        mfi_bearer_token="test-token-0123456789abcdef0123456789abcdef",
        mfi_backend_command=str(command),
        mfi_backend_timeout_seconds=timeout,
    )


def test_settings_default_to_the_built_in_hardware_backend() -> None:
    settings = Settings(mfi_bearer_token="test-token-0123456789abcdef0123456789abcdef")
    assert settings.mfi_backend_command == "/usr/local/bin/mfi-backend"


@pytest.mark.asyncio
async def test_command_backend_sends_operation_and_json_stdin(tmp_path: Path) -> None:
    helper = write_executable(
        tmp_path / "helper.py",
        """#!/usr/bin/env python3
import base64, json, sys
payload = json.load(sys.stdin)
assert sys.argv[1] == "sign"
assert base64.b64decode(payload["challenge"], validate=True) == b"challenge"
assert payload["requestId"] == "request-1"
print(json.dumps({"signature": base64.b64encode(b"real-helper-output").decode()}))
""",
    )
    backend = CommandBackend(make_settings(helper))
    result = await backend.sign(b"challenge", "request-1")
    assert result == b"real-helper-output"


@pytest.mark.asyncio
async def test_command_backend_rejects_malformed_json(tmp_path: Path) -> None:
    helper = write_executable(
        tmp_path / "helper.py",
        """#!/usr/bin/env python3
print("not-json")
""",
    )
    backend = CommandBackend(make_settings(helper))
    with pytest.raises(BackendFailure, match="valid JSON"):
        await backend.sign(b"challenge", "request-1")


@pytest.mark.asyncio
async def test_command_backend_surfaces_nonzero_exit(tmp_path: Path) -> None:
    helper = write_executable(
        tmp_path / "helper.py",
        """#!/usr/bin/env python3
import sys
print("hardware unavailable", file=sys.stderr)
raise SystemExit(7)
""",
    )
    backend = CommandBackend(make_settings(helper))
    with pytest.raises(BackendFailure, match="exit code 7"):
        await backend.sign(b"challenge", "request-1")


@pytest.mark.asyncio
async def test_command_backend_times_out(tmp_path: Path) -> None:
    helper = write_executable(
        tmp_path / "helper.py",
        """#!/usr/bin/env python3
import time
time.sleep(2)
""",
    )
    backend = CommandBackend(make_settings(helper, timeout=0.05))
    with pytest.raises(BackendUnavailable, match="timed out"):
        await backend.sign(b"challenge", "request-1")


@pytest.mark.asyncio
async def test_timeout_kills_descendants_that_inherit_output_pipes(tmp_path: Path) -> None:
    helper = write_executable(
        tmp_path / "helper.py",
        """#!/usr/bin/env python3
import subprocess, sys, time
subprocess.Popen([sys.executable, "-c", "import time; time.sleep(2)"])
time.sleep(2)
""",
    )
    backend = CommandBackend(make_settings(helper, timeout=0.05))
    started = time.monotonic()

    with pytest.raises(BackendUnavailable, match="timed out"):
        await backend.sign(b"challenge", "request-1")

    assert time.monotonic() - started < 1.5


@pytest.mark.asyncio
async def test_excessive_stdout_is_stopped_at_the_configured_limit(tmp_path: Path) -> None:
    helper = write_executable(
        tmp_path / "helper.py",
        """#!/usr/bin/env python3
import os
chunk = b"x" * 65536
while True:
    os.write(1, chunk)
""",
    )
    backend = CommandBackend(make_settings(helper, timeout=2.0))
    started = time.monotonic()

    with pytest.raises(BackendFailure, match="stdout is too large"):
        await backend.sign(b"challenge", "request-1")

    # Fresh executable scripts can incur roughly 0.8 s of macOS security/cold-start
    # latency. This still proves output limiting stops the process before its 2 s timeout.
    assert time.monotonic() - started < 1.5


@pytest.mark.asyncio
async def test_cancellation_terminates_the_backend_process(tmp_path: Path) -> None:
    pid_file = tmp_path / "helper.pid"
    helper = write_executable(
        tmp_path / "helper.py",
        f"""#!/usr/bin/env python3
import os, time
open({str(pid_file)!r}, "w", encoding="utf-8").write(str(os.getpid()))
time.sleep(30)
""",
    )
    backend = CommandBackend(make_settings(helper, timeout=20.0))
    task = asyncio.create_task(backend.sign(b"challenge", "request-1"))
    for _ in range(50):
        if pid_file.exists():
            break
        await asyncio.sleep(0.01)
    assert pid_file.exists()
    pid = int(pid_file.read_text(encoding="utf-8"))

    task.cancel()
    with pytest.raises(asyncio.CancelledError):
        await task
    await asyncio.sleep(0.05)

    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        pass
    else:
        os.kill(pid, 9)
        pytest.fail("cancelled backend process is still running")


@pytest.mark.asyncio
async def test_case_insensitive_bearer_token_is_not_inherited_by_helper(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    helper = write_executable(
        tmp_path / "helper.py",
        """#!/usr/bin/env python3
import base64, json, os
if any(name.casefold() == "mfi_bearer_token" for name in os.environ):
    raise SystemExit(9)
print(json.dumps({"signature": base64.b64encode(b"clean").decode()}))
""",
    )
    monkeypatch.setenv("mfi_bearer_token", "must-not-reach-helper")
    backend = CommandBackend(make_settings(helper))

    assert await backend.sign(b"challenge", "request-1") == b"clean"


@pytest.mark.asyncio
async def test_static_certificate_does_not_require_hardware_command() -> None:
    settings = Settings(
        mfi_bearer_token="test-token-0123456789abcdef0123456789abcdef",
        mfi_certificate_base64=base64.b64encode(b"certificate").decode("ascii"),
        mfi_protocol_major=3,
        mfi_certificate_type="mfi",
    )
    backend = CommandBackend(settings)
    material = await backend.get_certificate()
    assert material.type == "mfi"
    assert material.protocol_major == 3
    assert material.certificate == b"certificate"


@pytest.mark.asyncio
async def test_sign_without_hardware_command_is_unavailable() -> None:
    backend = CommandBackend(
        Settings(
            mfi_bearer_token="test-token-0123456789abcdef0123456789abcdef",
            mfi_backend_command="",
        ),
    )
    with pytest.raises(BackendUnavailable, match="MFI_BACKEND_COMMAND"):
        await backend.sign(b"challenge", "request-1")
