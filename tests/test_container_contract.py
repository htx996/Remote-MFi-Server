from __future__ import annotations

import re
from pathlib import Path

import yaml


ROOT = Path(__file__).resolve().parents[1]


def read_text(name: str) -> str:
    return (ROOT / name).read_text(encoding="utf-8")


def env_example() -> dict[str, str]:
    values: dict[str, str] = {}
    for raw_line in read_text(".env.example").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        name, value = line.split("=", 1)
        values[name] = value
    return values


def test_built_in_backend_is_the_default() -> None:
    assert env_example()["MFI_BACKEND_COMMAND"] == "/usr/local/bin/mfi-backend"


def test_dockerfile_builds_cgo_helper_and_installs_libusb_runtime() -> None:
    dockerfile = read_text("Dockerfile")
    assert re.search(r"^FROM .*golang:1\.23.* AS hardware-builder$", dockerfile, re.MULTILINE)
    assert "libusb-1.0-0-dev" in dockerfile
    assert "pkg-config" in dockerfile
    assert "CGO_ENABLED=1" in dockerfile
    assert "go build" in dockerfile
    assert re.search(
        r"COPY --from=hardware-builder .* /usr/local/bin/mfi-backend",
        dockerfile,
    )
    assert "libusb-1.0-0" in dockerfile


def test_final_image_remains_non_root_after_all_package_installation() -> None:
    dockerfile = read_text("Dockerfile")
    user_offset = dockerfile.rindex("USER 10001:10001")
    assert user_offset > dockerfile.rindex("apt-get install")
    assert "USER root" not in dockerfile[user_offset:]


def test_compose_uses_exact_usb_node_and_numeric_group_without_privileged_mode() -> None:
    base = yaml.safe_load(read_text("docker-compose.yml"))["services"]["remote-mfi-server"]
    usb = yaml.safe_load(read_text("docker-compose.usb.yml"))["services"]["remote-mfi-server"]
    merged_text = read_text("docker-compose.yml") + read_text("docker-compose.usb.yml")

    assert base.get("privileged") is not True
    assert usb.get("privileged") is not True
    assert usb["devices"] == [
        "${CH341_DEVICE_PATH:?请在 .env 设置实际 CH341_DEVICE_PATH}:"
        "${CH341_DEVICE_PATH:?请在 .env 设置实际 CH341_DEVICE_PATH}:rwm"
    ]
    assert usb["group_add"] == ["${CH341_DEVICE_GID:?请在 .env 设置设备节点的数字 GID}"]
    assert "/dev/bus/usb:/dev/bus/usb" not in merged_text


def test_custom_external_backend_remains_available_by_explicit_override() -> None:
    compose = yaml.safe_load(read_text("docker-compose.yml"))["services"]["remote-mfi-server"]
    assert "./backend:/opt/remote-mfi/backend:ro" in compose["volumes"]
    assert "/opt/remote-mfi/backend/mfi-backend" in read_text(".env.example")
