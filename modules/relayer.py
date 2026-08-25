import json
import shutil
import tempfile
from pathlib import Path

from .errors import RelayerError
from .lifecycle import run_yui
from .models import ResolvedChain, RuntimeConfig
from .render import chain_document, path_document, render_path_file, write_json


CONTAINER_HOME = "/root/.yui-relayer"


def ensure_global_config(runtime: RuntimeConfig) -> None:
    config_file = runtime.runtime_dir / "config" / "config.json"
    if not config_file.exists():
        run_yui(runtime, "config", "init")
    run_yui(runtime, "config", "show", capture=True)


def yui_config(runtime: RuntimeConfig) -> dict:
    result = run_yui(runtime, "config", "show", capture=True)
    try:
        value = json.loads(result.stdout)
    except json.JSONDecodeError as exc:
        raise RelayerError("O YUI retornou uma configuracao JSON invalida") from exc
    if not isinstance(value, dict):
        raise RelayerError("O YUI retornou uma configuracao invalida")
    return value


def _chain_id(entry: dict) -> str | None:
    chain = entry.get("chain", {})
    return chain.get("chain_id") or chain.get("chain-id")


def register_chains(
    runtime: RuntimeConfig,
    chains: tuple[ResolvedChain, ...],
) -> None:
    configured = {
        _chain_id(entry): entry for entry in yui_config(runtime).get("chains", [])
    }
    missing = []
    for resolved in chains:
        chain_id = resolved.chain.chain_id
        desired = chain_document(resolved)
        current = configured.get(chain_id)
        if current is None:
            missing.append(resolved)
        elif current != desired:
            raise RelayerError(
                f"A chain {chain_id} ja existe no runtime com configuracao diferente"
            )
        else:
            print(f"{resolved.chain.name}: chain ja registrada")
    if not missing:
        return

    runtime.runtime_dir.mkdir(parents=True, exist_ok=True)
    staging = Path(
        tempfile.mkdtemp(prefix=".setup-chains-", dir=runtime.runtime_dir)
    )
    try:
        for resolved in missing:
            write_json(staging / f"{resolved.chain.name}.json", chain_document(resolved))
        container_dir = f"{CONTAINER_HOME}/{staging.name}"
        run_yui(runtime, "chains", "add-dir", container_dir)
    finally:
        shutil.rmtree(staging, ignore_errors=True)


def import_keys(
    runtime: RuntimeConfig,
    chains: tuple[ResolvedChain, ...],
) -> None:
    for resolved in chains:
        if resolved.profile.adapter == "ethereum":
            print(
                f"{resolved.chain.name}: signer HD configurado no descritor da chain"
            )
            continue
        chain_id = resolved.chain.chain_id
        key_name = resolved.account.name
        existing = run_yui(
            runtime,
            "tendermint",
            "keys",
            "show",
            chain_id,
            key_name,
            capture=True,
            check=False,
        )
        if existing.returncode == 0:
            print(
                f"{resolved.chain.name}: chave {key_name} ja existe -> "
                f"{existing.stdout.strip()}"
            )
            continue
        restored = run_yui(
            runtime,
            "tendermint",
            "keys",
            "restore",
            chain_id,
            key_name,
            resolved.account.mnemonic,
            capture=True,
        )
        print(
            f"{resolved.chain.name}: chave {key_name} importada -> "
            f"{restored.stdout.strip()}"
        )


def initialize_light_clients(
    runtime: RuntimeConfig,
    chains: tuple[ResolvedChain, ...],
) -> None:
    for resolved in chains:
        if resolved.profile.adapter == "ethereum":
            print(
                f"{resolved.chain.name}: prover QBFT nao exige light cache local"
            )
            continue
        chain_id = resolved.chain.chain_id
        probe = run_yui(
            runtime,
            "tendermint",
            "light",
            "header",
            chain_id,
            "0",
            capture=True,
            check=False,
        )
        if probe.returncode == 0:
            print(f"{resolved.chain.name}: light client local ja inicializado")
            continue
        run_yui(runtime, "tendermint", "light", "init", chain_id, "-f")
        print(f"{resolved.chain.name}: light client local inicializado")


def _path_state(runtime: RuntimeConfig, name: str) -> dict | None:
    return yui_config(runtime).get("paths", {}).get(name)


def _required_path_state(runtime: RuntimeConfig, name: str) -> dict:
    current = _path_state(runtime, name)
    if not isinstance(current, dict):
        raise RelayerError(f"Path {name} nao foi persistido na configuracao do YUI")
    return current


def _complete(path: dict, field: str) -> bool:
    return bool(path.get("src", {}).get(field)) and bool(
        path.get("dst", {}).get(field)
    )


def configure_path(
    runtime: RuntimeConfig,
    name: str,
    source: ResolvedChain,
    destination: ResolvedChain,
) -> None:
    if source.chain.name == destination.chain.name:
        raise RelayerError("As chains do path devem ser diferentes")
    path_file = render_path_file(runtime, name, source, destination)
    desired = path_document(source, destination)
    current = _path_state(runtime, name)
    if current is None:
        container_file = f"{CONTAINER_HOME}/paths/{path_file.name}"
        run_yui(
            runtime,
            "paths",
            "add",
            source.chain.chain_id,
            destination.chain.chain_id,
            name,
            f"--file={container_file}",
        )
        current = _required_path_state(runtime, name)
    else:
        if not isinstance(current, dict):
            raise RelayerError(f"Path {name} possui configuracao invalida")
        actual_ids = (
            current.get("src", {}).get("chain-id"),
            current.get("dst", {}).get("chain-id"),
        )
        desired_ids = (
            desired["src"]["chain-id"],
            desired["dst"]["chain-id"],
        )
        if actual_ids != desired_ids:
            raise RelayerError(
                f"O path {name} ja existe para chains diferentes: {actual_ids}"
            )

    if not _complete(current, "client-id"):
        run_yui(runtime, "tx", "clients", name)
        current = _required_path_state(runtime, name)
    else:
        print(f"{name}: IBC clients ja configurados")

    if not _complete(current, "connection-id"):
        run_yui(runtime, "tx", "connection", name)
        current = _required_path_state(runtime, name)
    else:
        print(f"{name}: IBC connection ja configurada")

    if not _complete(current, "channel-id"):
        run_yui(runtime, "tx", "channel", name)
        current = _required_path_state(runtime, name)
    else:
        print(f"{name}: IBC channel ja configurado")

    for endpoint in ("src", "dst"):
        values = current.get(endpoint, {})
        required = ("client-id", "connection-id", "channel-id")
        if not all(values.get(field) for field in required):
            raise RelayerError(f"Path {name} incompleto em {endpoint}: {values}")
    print(f"Path {name} configurado e validado")
