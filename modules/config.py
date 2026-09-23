import ipaddress
import json
import re
from pathlib import Path
from typing import Any, Iterable

from .errors import ConfigError
from .models import (
    Chain,
    DescriptorConfig,
    Profile,
    RelayerAccount,
    ResolvedChain,
    RuntimeConfig,
)


NAME_PATTERN = re.compile(r"^[a-zA-Z0-9][a-zA-Z0-9_.-]*$")
MNEMONIC_PLACEHOLDER = "ADD_COSMOS_RELAYER_MNEMONIC_HERE"


def _read_json(path: Path) -> Any:
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except FileNotFoundError as exc:
        raise ConfigError(f"Arquivo nao encontrado: {path}") from exc
    except json.JSONDecodeError as exc:
        raise ConfigError(
            f"JSON invalido em {path}:{exc.lineno}:{exc.colno}: {exc.msg}"
        ) from exc


def _read_env(path: Path) -> dict[str, str]:
    try:
        lines = path.read_text(encoding="utf-8").splitlines()
    except FileNotFoundError as exc:
        raise ConfigError(f"Arquivo de ambiente nao encontrado: {path}") from exc

    values: dict[str, str] = {}
    for number, original in enumerate(lines, start=1):
        line = original.strip()
        if not line or line.startswith("#"):
            continue
        if line.startswith("export "):
            line = line[7:].lstrip()
        if "=" not in line:
            raise ConfigError(f"Linha invalida em {path}:{number}")
        key, value = line.split("=", 1)
        key = key.strip()
        value = value.strip()
        if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
            value = value[1:-1]
        values[key] = value
    return values


def _string(data: dict[str, Any], key: str, context: str) -> str:
    value = data.get(key)
    if not isinstance(value, str) or not value.strip():
        raise ConfigError(f"{context}: campo obrigatorio invalido: {key}")
    return value.strip()


def _integer(data: dict[str, Any], key: str, context: str) -> int:
    value = data.get(key)
    if isinstance(value, bool):
        raise ConfigError(f"{context}: inteiro invalido: {key}")
    try:
        result = int(value)
    except (TypeError, ValueError) as exc:
        raise ConfigError(f"{context}: inteiro invalido: {key}") from exc
    if result <= 0:
        raise ConfigError(f"{context}: {key} deve ser maior que zero")
    return result


def _number(data: dict[str, Any], key: str, context: str) -> float:
    value = data.get(key)
    if isinstance(value, bool):
        raise ConfigError(f"{context}: numero invalido: {key}")
    try:
        result = float(value)
    except (TypeError, ValueError) as exc:
        raise ConfigError(f"{context}: numero invalido: {key}") from exc
    if result <= 0:
        raise ConfigError(f"{context}: {key} deve ser maior que zero")
    return result


def _object(data: dict[str, Any], key: str, context: str) -> dict[str, Any]:
    value = data.get(key)
    if not isinstance(value, dict):
        raise ConfigError(f"{context}: {key} deve ser um objeto")
    return value


def _fraction(data: dict[str, Any], key: str, context: str) -> dict[str, int]:
    value = _object(data, key, context)
    numerator = _integer(value, "numerator", context)
    denominator = _integer(value, "denominator", context)
    return {"numerator": numerator, "denominator": denominator}


def _documents(paths: Iterable[Path], key: str) -> list[tuple[Path, dict[str, Any]]]:
    entries: list[tuple[Path, dict[str, Any]]] = []
    for path in paths:
        document = _read_json(path)
        raw_entries = document.get(key) if isinstance(document, dict) else None
        if not isinstance(raw_entries, list):
            raise ConfigError(f"{path}: {key} deve ser uma lista")
        for index, raw in enumerate(raw_entries):
            if not isinstance(raw, dict):
                raise ConfigError(f"{path}: {key}[{index}] deve ser um objeto")
            entries.append((path, raw))
    return entries


def _load_profiles(paths: tuple[Path, ...]) -> tuple[Profile, ...]:
    profiles: list[Profile] = []
    for path in paths:
        raw = _read_json(path)
        if not isinstance(raw, dict):
            raise ConfigError(f"{path}: deve conter um objeto")
        context = str(path)
        name = _string(raw, "name", context)
        adapter = _string(raw, "adapter", context)
        relayer = _object(raw, "relayer", context)
        account_prefix: str | None = None
        prover: dict[str, Any] | None = None

        if adapter == "tendermint":
            account_prefix = _string(raw, "account_prefix", context)
            relayer = {
                "gas_adjustment": _number(relayer, "gas_adjustment", context),
                "gas_prices": _string(relayer, "gas_prices", context),
                "average_block_time_msec": _integer(
                    relayer, "average_block_time_msec", context
                ),
                "max_retry_for_commit": _integer(
                    relayer, "max_retry_for_commit", context
                ),
                "trusting_period": _string(relayer, "trusting_period", context),
            }
        elif adapter == "ethereum":
            signer = _object(relayer, "signer", context)
            if _string(signer, "type", context) != "hd":
                raise ConfigError(f"{context}: apenas signer hd e suportado")
            relayer = {
                "average_block_time_msec": _integer(
                    relayer, "average_block_time_msec", context
                ),
                "max_retry_for_inclusion": _integer(
                    relayer, "max_retry_for_inclusion", context
                ),
                "gas_estimate_rate": _fraction(
                    relayer, "gas_estimate_rate", context
                ),
                "max_gas_limit": _integer(relayer, "max_gas_limit", context),
                "tx_type": _string(relayer, "tx_type", context),
                "signer": {
                    "type": "hd",
                    "derivation_path": _string(
                        signer, "derivation_path", context
                    ),
                },
            }
            prover_raw = _object(raw, "prover", context)
            if _string(prover_raw, "type", context) != "qbft":
                raise ConfigError(f"{context}: apenas prover qbft e suportado")
            prover = {
                "type": "qbft",
                "consensus_type": _string(
                    prover_raw, "consensus_type", context
                ),
                "trusting_period": _string(
                    prover_raw, "trusting_period", context
                ),
                "max_clock_drift": _string(
                    prover_raw, "max_clock_drift", context
                ),
                "refresh_threshold_rate": _fraction(
                    prover_raw, "refresh_threshold_rate", context
                ),
            }
        elif adapter == "fabric":
            relayer = {
                "average_block_time_msec": _integer(
                    relayer, "average_block_time_msec", context
                ),
            }
            prover_raw = _object(raw, "prover", context)
            prover = {
                "trusting_period_sec": _integer(
                    prover_raw, "trusting_period_sec", context
                ),
                "max_clock_drift_sec": _integer(
                    prover_raw, "max_clock_drift_sec", context
                ),
            }
        else:
            raise ConfigError(f"{context}: adapter ainda nao suportado: {adapter}")

        profile = Profile(
            name=name,
            adapter=adapter,
            account_prefix=account_prefix,
            relayer=relayer,
            prover=prover,
            source_file=path.resolve(),
        )
        profiles.append(profile)
    _unique(profiles, "name", "profiles")
    return tuple(profiles)


def _load_chains(
    paths: tuple[Path, ...], profiles: dict[str, Profile]
) -> tuple[Chain, ...]:
    chains: list[Chain] = []
    for path, raw in _documents(paths, "chains"):
        context = f"{path}: chain {raw.get('name', '<sem nome>')}"
        name = _string(raw, "name", context)
        service = _string(raw, "service", context)
        if not NAME_PATTERN.fullmatch(name) or not NAME_PATTERN.fullmatch(service):
            raise ConfigError(f"{context}: name ou service invalido")
        profile_name = _string(raw, "profile", context)
        profile = profiles.get(profile_name)
        if profile is None:
            raise ConfigError(f"{name}: profile nao carregado: {profile_name}")

        rpc_addr = raw.get("rpc_addr")
        if rpc_addr is None:
            port = 8545 if profile.adapter == "ethereum" else 26657
            rpc_addr = f"http://{service}:{port}"
        if not isinstance(rpc_addr, str) or not rpc_addr.strip():
            raise ConfigError(f"{context}: rpc_addr invalido")

        eth_chain_id: int | None = None
        ibc_address: str | None = None
        abi_paths: tuple[str, ...] = ()
        adapter_config: dict[str, Any] = {}
        if profile.adapter == "ethereum":
            eth_chain_id = _integer(raw, "eth_chain_id", context)
            ibc_address = _string(raw, "ibc_address", context)
            raw_abi_paths = raw.get("abi_paths", [])
            if not isinstance(raw_abi_paths, list) or not all(
                isinstance(value, str) and value.strip() for value in raw_abi_paths
            ):
                raise ConfigError(f"{context}: abi_paths deve ser uma lista")
            abi_paths = tuple(value.strip() for value in raw_abi_paths)
        elif profile.adapter == "fabric":
            artifacts_dir = Path(_string(raw, "artifacts_dir", context)).expanduser()
            if not artifacts_dir.is_absolute():
                artifacts_dir = path.parent / artifacts_dir
            adapter_config = {
                "channel_id": _string(raw, "channel_id", context),
                "chaincode_name": _string(raw, "chaincode_name", context),
                "chaincode_path": str(raw.get("chaincode_path", "")).strip(),
                "chaincode_version": _string(raw, "chaincode_version", context),
                "gateway_endpoint": _string(raw, "gateway_endpoint", context),
                "gateway_host_override": _string(
                    raw, "gateway_host_override", context
                ),
                "msp_id": _string(raw, "msp_id", context),
                "artifacts_dir": artifacts_dir.resolve().as_posix(),
                "tls_ca_cert_file": str(
                    raw.get("tls_ca_cert_file", "peer_tls_ca.pem")
                ).strip(),
                "cert_file": str(raw.get("cert_file", "admin_cert.pem")).strip(),
                "key_file": str(raw.get("key_file", "admin_key.pem")).strip(),
                "msp_config_file": str(
                    raw.get("msp_config_file", "org1_msp_config.pb")
                ).strip(),
                "msp_policy_file": str(raw.get("msp_policy_file", "")).strip(),
                "endorsement_policy_file": str(
                    raw.get("endorsement_policy_file", "endorsement_policy.pb")
                ).strip(),
                "ibc_policy_file": str(
                    raw.get("ibc_policy_file", "ibc_policy.pb")
                ).strip(),
            }
            empty = [
                key for key, value in adapter_config.items()
                if key not in {"chaincode_path", "msp_policy_file"} and not value
            ]
            if empty:
                raise ConfigError(
                    f"{context}: campos Fabric invalidos: {', '.join(empty)}"
                )

        chains.append(
            Chain(
                name=name,
                profile=profile_name,
                chain_id=_string(raw, "chain_id", context),
                service=service,
                rpc_addr=rpc_addr.strip(),
                eth_chain_id=eth_chain_id,
                ibc_address=ibc_address,
                abi_paths=abi_paths,
                adapter_config=adapter_config,
                source_file=path.resolve(),
            )
        )
    _unique(chains, "name", "chains")
    _unique(chains, "chain_id", "chains")
    return tuple(chains)


def _load_accounts(paths: tuple[Path, ...]) -> tuple[RelayerAccount, ...]:
    accounts: list[RelayerAccount] = []
    for path, raw in _documents(paths, "accounts"):
        context = f"{path}: account {raw.get('name', '<sem nome>')}"
        memberships = raw.get("chains")
        if not isinstance(memberships, list) or not memberships:
            raise ConfigError(f"{context}: chains deve ser uma lista nao vazia")
        if not all(isinstance(value, str) and value.strip() for value in memberships):
            raise ConfigError(f"{context}: chains contem valor invalido")
        mnemonic_raw = raw.get("mnemonic")
        mnemonic = (
            mnemonic_raw.strip()
            if isinstance(mnemonic_raw, str) and mnemonic_raw.strip()
            else None
        )
        if mnemonic == MNEMONIC_PLACEHOLDER:
            raise ConfigError(f"{context}: mnemonic ainda usa o placeholder")
        accounts.append(
            RelayerAccount(
                name=_string(raw, "name", context),
                chains=tuple(dict.fromkeys(value.strip() for value in memberships)),
                mnemonic=mnemonic,
                derivation_path=(
                    raw["derivation_path"].strip()
                    if isinstance(raw.get("derivation_path"), str)
                    and raw["derivation_path"].strip()
                    else None
                ),
                source_file=path.resolve(),
            )
        )
    _unique(accounts, "name", "relayer accounts")
    return tuple(accounts)


def _unique(entries: list[Any], attribute: str, context: str) -> None:
    values = [getattr(entry, attribute) for entry in entries]
    duplicates = sorted({value for value in values if values.count(value) > 1})
    if duplicates:
        raise ConfigError(f"Valores duplicados em {context}: {', '.join(duplicates)}")


def load_descriptors(
    profile_files: tuple[Path, ...],
    chain_files: tuple[Path, ...],
    account_files: tuple[Path, ...],
) -> DescriptorConfig:
    profiles = _load_profiles(profile_files)
    profiles_by_name = {profile.name: profile for profile in profiles}
    chains = _load_chains(chain_files, profiles_by_name)
    accounts = _load_accounts(account_files)

    resolved: list[ResolvedChain] = []
    known_chain_names = {chain.name for chain in chains}
    for account in accounts:
        unknown = sorted(set(account.chains) - known_chain_names)
        if unknown:
            raise ConfigError(
                f"Conta {account.name} referencia chains nao carregadas: "
                + ", ".join(unknown)
            )

    for chain in chains:
        profile = profiles_by_name.get(chain.profile)
        if profile is None:
            raise ConfigError(
                f"{chain.name}: profile nao carregado: {chain.profile}"
            )
        matches = [account for account in accounts if chain.name in account.chains]
        if len(matches) != 1:
            raise ConfigError(
                f"{chain.name}: esperado exatamente um relayer account; "
                f"encontrados {len(matches)}"
            )
        account = matches[0]
        if profile.adapter != "fabric" and not account.mnemonic:
            raise ConfigError(
                f"{account.source_file}: account {account.name}: mnemonic obrigatorio"
            )
        resolved.append(ResolvedChain(chain, profile, account))

    return DescriptorConfig(profiles, chains, accounts, tuple(resolved))


def select_chains(
    config: DescriptorConfig,
    requested: list[str] | None,
) -> tuple[ResolvedChain, ...]:
    if not requested:
        return config.resolved_chains
    aliases: dict[str, ResolvedChain] = {}
    for resolved in config.resolved_chains:
        chain = resolved.chain
        for alias in (chain.name, chain.chain_id, chain.service):
            aliases[alias] = resolved
    selected: list[ResolvedChain] = []
    unknown: list[str] = []
    for value in requested:
        chain = aliases.get(value)
        if chain is None:
            unknown.append(value)
        elif chain not in selected:
            selected.append(chain)
    if unknown:
        raise ConfigError(f"Chains nao encontradas: {', '.join(unknown)}")
    return tuple(selected)


def load_runtime(
    root_dir: Path,
    runtime_dir: Path,
    env_file: Path,
) -> RuntimeConfig:
    values = _read_env(env_file)
    ip = values.get("YUI_RELAYER_IP", "").strip()
    network_name = values.get("DOCKER_NETWORK_NAME", "").strip()
    if not ip or not network_name:
        raise ConfigError(
            f"{env_file}: YUI_RELAYER_IP e DOCKER_NETWORK_NAME sao obrigatorios"
        )
    try:
        ipaddress.ip_address(ip)
    except ValueError as exc:
        raise ConfigError(f"{env_file}: YUI_RELAYER_IP invalido: {ip}") from exc
    runtime_dir = runtime_dir.resolve()
    return RuntimeConfig(
        root_dir=root_dir.resolve(),
        runtime_dir=runtime_dir,
        compose_file=root_dir.resolve() / "docker-compose.yaml",
        container_name=values.get("YUI_RELAYER_CONTAINER", "yui-relayer").strip()
        or "yui-relayer",
        image=values.get("YUI_RELAYER_IMAGE", "yui-relayer-local:dev").strip()
        or "yui-relayer-local:dev",
        network_name=network_name,
        ip=ip,
    )
