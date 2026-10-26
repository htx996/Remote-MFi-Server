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


def test_readme_documents_the_supported_board_and_hardware_acceptance_boundary() -> None:
    readme = read_text("README.md")

    assert "CH341F（USB VID:PID `1a86:5512`）+ MFI337S3959（丝印 3959）" in readme
    assert "mfi-backend probe" in readme
    assert "`/healthz` 只验证 HTTP 服务" in readme
    assert "不会脉冲物理 RST" in readme
    assert "USB 设备编号变化" in readme
    assert "hanfu1997/remote-mfi-server:1.0.0" in readme
    assert "hardware-backend/NOTICE" in readme
    assert "GNU GPL v3" in readme


def test_readme_hardware_examples_do_not_print_certificate_or_signature_bodies() -> None:
    readme = read_text("README.md")

    assert "certificateLength" in readme
    assert "certificateSha256" in readme
    assert "signatureLength" in readme
    assert "signatureSha256" in readme


def test_publish_workflow_requires_python_and_go_quality_gates() -> None:
    workflow = yaml.safe_load(read_text(".github/workflows/docker-publish.yml"))
    jobs = workflow["jobs"]
    test_steps = "\n".join(str(step) for step in jobs["test"]["steps"])

    assert jobs["publish"]["needs"] == "test"
    assert "libusb-1.0-0-dev" in test_steps
    assert "pkg-config" in test_steps
    assert "python -m pytest -q" in test_steps
    assert "go test -race ./..." in test_steps
    assert "go vet ./..." in test_steps


def test_publish_workflow_smoke_runs_each_target_architecture() -> None:
    workflow = yaml.safe_load(read_text(".github/workflows/docker-publish.yml"))
    publish_steps = "\n".join(str(step) for step in workflow["jobs"]["publish"]["steps"])

    assert "--platform linux/amd64" in publish_steps
    assert "--platform linux/arm64" in publish_steps
    assert "/usr/local/bin/mfi-backend --version" in publish_steps
