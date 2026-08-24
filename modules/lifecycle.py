import ipaddress
import json
import shutil
import subprocess
from pathlib import Path
from typing import Sequence

from .errors import DockerError, RelayerError
from .models import RuntimeConfig


def _run(
    command: Sequence[str],
    *,
    cwd: Path,
    capture: bool = False,
    check: bool = True,
) -> subprocess.CompletedProcess[str]:
    try:
        result = subprocess.run(
            list(command),
            cwd=cwd,
            check=False,
            text=True,
            capture_output=capture,
        )
    except FileNotFoundError as exc:
        raise DockerError(f"Comando nao encontrado: {command[0]}") from exc
    if check and result.returncode != 0:
        details = (result.stderr or result.stdout or "").strip()
        suffix = f": {details}" if details else ""
        raise DockerError(
            f"Comando falhou com codigo {result.returncode}: "
            f"{' '.join(command)}{suffix}"
        )
    return result


def _compose(runtime: RuntimeConfig, *arguments: str) -> tuple[str, ...]:
    return (
        "docker",
        "compose",
        "-f",
        str(runtime.compose_file),
        *arguments,
    )


def verify_docker(runtime: RuntimeConfig) -> None:
    if shutil.which("docker") is None:
        raise DockerError("docker nao foi encontrado no PATH")
    _run(("docker", "compose", "version"), cwd=runtime.root_dir, capture=True)
    _run(("docker", "info", "--format", "{{.ServerVersion}}"), cwd=runtime.root_dir, capture=True)


def verify_network(runtime: RuntimeConfig) -> None:
    result = _run(
        ("docker", "network", "inspect", runtime.network_name),
        cwd=runtime.root_dir,
        capture=True,
        check=False,
    )
    if result.returncode != 0:
        raise DockerError(
            f"Rede externa nao encontrada: {runtime.network_name}; "
            "inicialize primeiro um modulo blockchain"
        )
    try:
        network = json.loads(result.stdout)[0]
    except (json.JSONDecodeError, IndexError, TypeError) as exc:
        raise DockerError(
            f"Resposta invalida ao inspecionar a rede {runtime.network_name}"
        ) from exc

    address = ipaddress.ip_address(runtime.ip)
    subnets = []
    for item in network.get("IPAM", {}).get("Config", []) or []:
        value = item.get("Subnet")
        if value:
            try:
                subnets.append(ipaddress.ip_network(value, strict=False))
            except ValueError:
                pass
    if not any(address in subnet for subnet in subnets):
        raise DockerError(
            f"IP {runtime.ip} nao pertence ao IPAM da rede "
            f"{runtime.network_name}: {subnets}"
        )

    for container_id, item in (network.get("Containers") or {}).items():
        current = str(item.get("IPv4Address", "")).split("/", 1)[0]
        name = item.get("Name")
        if current == runtime.ip and name != runtime.container_name:
            raise DockerError(
                f"IP {runtime.ip} ja esta em uso por {name or container_id}"
            )


def _verify_existing_mount(runtime: RuntimeConfig) -> None:
    result = _run(
        ("docker", "inspect", runtime.container_name),
        cwd=runtime.root_dir,
        capture=True,
        check=False,
    )
    if result.returncode != 0:
        return
    try:
        container = json.loads(result.stdout)[0]
    except (json.JSONDecodeError, IndexError, TypeError) as exc:
        raise DockerError(
            f"Resposta invalida ao inspecionar {runtime.container_name}"
        ) from exc
    mount = next(
        (
            item
            for item in container.get("Mounts", [])
            if item.get("Destination") == "/root/.yui-relayer"
        ),
        None,
    )
    actual = Path(mount["Source"]).resolve() if mount and mount.get("Source") else None
    if actual != runtime.runtime_dir:
        raise DockerError(
            f"O container {runtime.container_name} ja existe com outro runtime: "
            f"{actual}. Remova o container legado antes de executar init."
        )


def build_and_start(runtime: RuntimeConfig, *, build: bool) -> None:
    _verify_existing_mount(runtime)
    if build:
        _run(_compose(runtime, "build", "yui-relayer"), cwd=runtime.root_dir)
    _run(
        _compose(runtime, "up", "-d", "--no-build", "yui-relayer"),
        cwd=runtime.root_dir,
    )
    result = _run(
        _compose(runtime, "ps", "--status", "running", "--quiet", "yui-relayer"),
        cwd=runtime.root_dir,
        capture=True,
    )
    if not result.stdout.strip():
        raise DockerError("O container yui-relayer nao ficou em execucao")


def run_yui(
    runtime: RuntimeConfig,
    *arguments: str,
    capture: bool = False,
    check: bool = True,
) -> subprocess.CompletedProcess[str]:
    result = _run(
        _compose(runtime, "exec", "-T", "yui-relayer", "yrly", *arguments),
        cwd=runtime.root_dir,
        capture=capture,
        check=False,
    )
    if check and result.returncode != 0:
        details = (result.stderr or result.stdout or "").strip()
        suffix = f": {details}" if details else ""
        raise RelayerError(
            f"yrly {' '.join(arguments)} falhou com codigo "
            f"{result.returncode}{suffix}"
        )
    return result


def status(runtime: RuntimeConfig) -> None:
    _run(_compose(runtime, "ps"), cwd=runtime.root_dir)
    result = run_yui(runtime, "config", "show", capture=True, check=False)
    if result.returncode == 0 and result.stdout.strip():
        print(result.stdout.strip())


def start_service(runtime: RuntimeConfig, path: str) -> None:
    run_yui(runtime, "service", "start", path)
