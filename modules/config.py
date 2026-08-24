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
    RelayerSettings,
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
        relayer = raw.get("relayer")
        if not isinstance(relayer, dict):
            raise ConfigError(f"{context}: relayer deve ser um objeto")
        profile = Profile(
            name=_string(raw, "name", context),
            adapter=_string(raw, "adapter", context),
            account_prefix=_string(raw, "account_prefix", context),
            relayer=RelayerSettings(
                gas_adjustment=_number(relayer, "gas_adjustment", context),
                gas_prices=_string(relayer, "gas_prices", context),
                average_block_time_msec=_integer(
                    relayer, "average_block_time_msec", context
                ),
                max_retry_for_commit=_integer(
                    relayer, "max_retry_for_commit", context
                ),
                trusting_period=_string(relayer, "trusting_period", context),
            ),
            source_file=path.resolve(),
        )
        if profile.adapter != "tendermint":
            raise ConfigError(
                f"{context}: adapter ainda nao suportado: {profile.adapter}"
            )
        profiles.append(profile)
    _unique(profiles, "name", "profiles")
    return tuple(profiles)


def _load_chains(paths: tuple[Path, ...]) -> tuple[Chain, ...]:
    chains: list[Chain] = []
    for path, raw in _documents(paths, "chains"):
        context = f"{path}: chain {raw.get('name', '<sem nome>')}"
        name = _string(raw, "name", context)
        service = _string(raw, "service", context)
        if not NAME_PATTERN.fullmatch(name) or not NAME_PATTERN.fullmatch(service):
            raise ConfigError(f"{context}: name ou service invalido")
        rpc_addr = raw.get("rpc_addr")
        if rpc_addr is None:
            rpc_addr = f"http://{service}:26657"
        if not isinstance(rpc_addr, str) or not rpc_addr.strip():
            raise ConfigError(f"{context}: rpc_addr invalido")
        chains.append(
            Chain(
                name=name,
                profile=_string(raw, "profile", context),
                chain_id=_string(raw, "chain_id", context),
                service=service,
                rpc_addr=rpc_addr.strip(),
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
        mnemonic = _string(raw, "mnemonic", context)
        if mnemonic == MNEMONIC_PLACEHOLDER:
            raise ConfigError(f"{context}: mnemonic ainda usa o placeholder")
        accounts.append(
            RelayerAccount(
                name=_string(raw, "name", context),
                chains=tuple(dict.fromkeys(value.strip() for value in memberships)),
                mnemonic=mnemonic,
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
    chains = _load_chains(chain_files)
    accounts = _load_accounts(account_files)
    profiles_by_name = {profile.name: profile for profile in profiles}

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
        resolved.append(ResolvedChain(chain, profile, matches[0]))

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
