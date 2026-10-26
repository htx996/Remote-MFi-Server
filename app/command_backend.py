import asyncio
import base64
import binascii
import json
import logging
import os
import shlex
import signal
from pathlib import Path
from typing import Any

from app.backend import BackendFailure, BackendUnavailable, CertificateMaterial
from app.config import Settings


LOGGER = logging.getLogger("remote_mfi.backend")
MAXIMUM_BACKEND_OUTPUT_BYTES = 256 * 1024
MAXIMUM_BACKEND_STDERR_BYTES = 64 * 1024
PROCESS_STOP_GRACE_SECONDS = 0.05


class _StreamLimitExceeded(RuntimeError):
    def __init__(self, stream_name: str) -> None:
        super().__init__(stream_name)
        self.stream_name = stream_name


class CommandBackend:
    """Delegates real chip operations to an administrator-supplied executable."""

    def __init__(self, settings: Settings) -> None:
        self._settings = settings

    async def get_certificate(self) -> CertificateMaterial:
        static_certificate = await self._read_static_certificate()
        if static_certificate is not None:
            return CertificateMaterial(
                type=self._settings.mfi_certificate_type,
                protocol_major=self._settings.mfi_protocol_major,
                certificate=static_certificate,
            )
        response = await self._run("certificate", {})
        certificate = self._decode_base64(response.get("certificate"), "certificate")
        certificate_type = response.get("type", "mfi")
        protocol_major = response.get("protocolMajor")
        if certificate_type not in ("mfi", "baa"):
            raise BackendFailure("hardware backend returned an unsupported certificate type")
        if not isinstance(protocol_major, int) or isinstance(protocol_major, bool):
            raise BackendFailure("hardware backend response is missing integer protocolMajor")
        return CertificateMaterial(
            type=certificate_type,
            protocol_major=protocol_major,
            certificate=certificate,
        )

    async def sign(self, challenge: bytes, request_id: str) -> bytes:
        response = await self._run(
            "sign",
            {
                "challenge": base64.b64encode(challenge).decode("ascii"),
                "requestId": request_id,
            },
        )
        return self._decode_base64(response.get("signature"), "signature")

    async def reset(self) -> str:
        response = await self._run("reset", {})
        detail = response.get("detail", "")
        if not isinstance(detail, str):
            raise BackendFailure("hardware backend reset detail must be a string")
        return detail

    async def _read_static_certificate(self) -> bytes | None:
        encoded = self._settings.mfi_certificate_base64.strip()
        path = self._settings.mfi_certificate_file
        if encoded and path is not None:
            raise BackendFailure(
                "configure only one of MFI_CERTIFICATE_BASE64 or MFI_CERTIFICATE_FILE",
            )
        if encoded:
            return self._decode_base64(encoded, "MFI_CERTIFICATE_BASE64")
        if path is None:
            return None
        try:
            certificate = await asyncio.to_thread(Path(path).read_bytes)
        except OSError as error:
            raise BackendUnavailable("configured MFI certificate file cannot be read") from error
        if len(certificate) > MAXIMUM_BACKEND_OUTPUT_BYTES:
            raise BackendFailure("configured MFI certificate file is too large")
        return certificate

    async def _run(self, operation: str, payload: dict[str, Any]) -> dict[str, Any]:
        try:
            command = shlex.split(self._settings.mfi_backend_command)
        except ValueError as error:
            raise BackendUnavailable("MFI_BACKEND_COMMAND has invalid quoting") from error
        if not command:
            raise BackendUnavailable(
                "MFI_BACKEND_COMMAND is not configured; real CH341/I2C backend is unavailable",
            )
        environment = {
            name: value
            for name, value in os.environ.items()
            if name.casefold() != "mfi_bearer_token"
        }
        process_options: dict[str, Any] = {}
        if os.name == "posix":
            process_options["start_new_session"] = True
        try:
            process = await asyncio.create_subprocess_exec(
                *command,
                operation,
                stdin=asyncio.subprocess.PIPE,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
                env=environment,
                **process_options,
            )
        except OSError as error:
            raise BackendUnavailable("hardware backend command could not be started") from error

        assert process.stdin is not None
        assert process.stdout is not None
        assert process.stderr is not None
        output_tasks = [
            asyncio.create_task(
                self._read_limited(
                    process.stdout,
                    MAXIMUM_BACKEND_OUTPUT_BYTES,
                    "stdout",
                ),
            ),
            asyncio.create_task(
                self._read_limited(
                    process.stderr,
                    MAXIMUM_BACKEND_STDERR_BYTES,
                    "stderr",
                ),
            ),
            asyncio.create_task(process.wait()),
        ]
        input_bytes = json.dumps(payload, separators=(",", ":")).encode("utf-8")

        async def exchange() -> tuple[bytes, bytes, int]:
            process.stdin.write(input_bytes)
            await process.stdin.drain()
            process.stdin.close()
            try:
                await process.stdin.wait_closed()
            except (BrokenPipeError, ConnectionResetError):
                pass
            stdout, stderr, return_code = await asyncio.gather(*output_tasks)
            return stdout, stderr, return_code

        try:
            stdout, _stderr, return_code = await asyncio.wait_for(
                exchange(),
                timeout=self._settings.mfi_backend_timeout_seconds,
            )
        except TimeoutError as error:
            await self._stop_process_tree(process, output_tasks)
            LOGGER.warning("hardware backend timed out", extra={"operation": operation})
            raise BackendUnavailable("hardware backend command timed out") from error
        except _StreamLimitExceeded as error:
            await self._stop_process_tree(process, output_tasks)
            raise BackendFailure(
                f"hardware backend {error.stream_name} is too large",
            ) from error
        except asyncio.CancelledError:
            await asyncio.shield(self._stop_process_tree(process, output_tasks))
            raise

        if return_code != 0:
            LOGGER.error(
                "hardware backend exited with failure",
                extra={"operation": operation, "exit_code": return_code},
            )
            raise BackendFailure(
                f"hardware backend command failed with exit code {return_code}",
            )
        try:
            decoded = json.loads(stdout.decode("utf-8"))
        except (UnicodeDecodeError, json.JSONDecodeError) as error:
            raise BackendFailure("hardware backend did not return valid JSON") from error
        if not isinstance(decoded, dict):
            raise BackendFailure("hardware backend JSON response must be an object")
        return decoded

    @staticmethod
    async def _read_limited(
        stream: asyncio.StreamReader,
        maximum_bytes: int,
        stream_name: str,
    ) -> bytes:
        output = bytearray()
        while True:
            chunk = await stream.read(65_536)
            if not chunk:
                return bytes(output)
            if len(output) + len(chunk) > maximum_bytes:
                raise _StreamLimitExceeded(stream_name)
            output.extend(chunk)

    @staticmethod
    async def _stop_process_tree(
        process: asyncio.subprocess.Process,
        tasks: list[asyncio.Task],
    ) -> None:
        if process.stdin is not None:
            process.stdin.close()
            try:
                await process.stdin.wait_closed()
            except (BrokenPipeError, ConnectionResetError):
                pass

        def signal_process_tree(selected_signal: signal.Signals) -> None:
            try:
                if os.name == "posix":
                    os.killpg(process.pid, selected_signal)
                elif process.returncode is None:
                    if selected_signal == signal.SIGTERM:
                        process.terminate()
                    else:
                        process.kill()
            except ProcessLookupError:
                pass

        signal_process_tree(signal.SIGTERM)
        try:
            await asyncio.wait_for(
                process.wait(),
                timeout=PROCESS_STOP_GRACE_SECONDS,
            )
        except TimeoutError:
            pass

        # Kill the process group even if the direct child already exited; a descendant may
        # still own the inherited stdout/stderr pipes or the USB device.
        signal_process_tree(signal.SIGKILL)
        if process.returncode is None:
            try:
                await asyncio.wait_for(
                    process.wait(),
                    timeout=PROCESS_STOP_GRACE_SECONDS,
                )
            except TimeoutError:
                LOGGER.error("hardware backend process could not be reaped promptly")

        for task in tasks:
            if not task.done():
                task.cancel()
        await asyncio.gather(*tasks, return_exceptions=True)

        async def drain(stream: asyncio.StreamReader | None) -> None:
            if stream is None:
                return
            try:
                while await stream.read(65_536):
                    pass
            except (BrokenPipeError, ConnectionResetError):
                pass

        try:
            await asyncio.wait_for(
                asyncio.gather(drain(process.stdout), drain(process.stderr)),
                timeout=PROCESS_STOP_GRACE_SECONDS,
            )
        except TimeoutError:
            pass

    @staticmethod
    def _decode_base64(value: Any, field: str) -> bytes:
        if not isinstance(value, str) or not value:
            raise BackendFailure(f"hardware backend response is missing {field}")
        try:
            decoded = base64.b64decode(value, validate=True)
        except (binascii.Error, ValueError) as error:
            raise BackendFailure(f"hardware backend {field} is not valid Base64") from error
        if not decoded:
            raise BackendFailure(f"hardware backend {field} is empty")
        return decoded
