from dataclasses import dataclass
from pathlib import Path
from typing import Any


@dataclass(frozen=True)
class Profile:
    name: str
    adapter: str
    account_prefix: str | None
    relayer: dict[str, Any]
    prover: dict[str, Any] | None
    source_file: Path


@dataclass(frozen=True)
class Chain:
    name: str
    profile: str
    chain_id: str
    service: str
    rpc_addr: str
    eth_chain_id: int | None
    ibc_address: str | None
    abi_paths: tuple[str, ...]
    adapter_config: dict[str, Any]
    source_file: Path


@dataclass(frozen=True)
class RelayerAccount:
    name: str
    chains: tuple[str, ...]
    mnemonic: str | None
    derivation_path: str | None
    source_file: Path


@dataclass(frozen=True)
class DescriptorSources:
    profile_files: tuple[Path, ...]
    chain_files: tuple[Path, ...]
    account_files: tuple[Path, ...]


@dataclass(frozen=True)
class ResolvedChain:
    chain: Chain
    profile: Profile
    account: RelayerAccount


@dataclass(frozen=True)
class DescriptorConfig:
    profiles: tuple[Profile, ...]
    chains: tuple[Chain, ...]
    accounts: tuple[RelayerAccount, ...]
    resolved_chains: tuple[ResolvedChain, ...]


@dataclass(frozen=True)
class RuntimeConfig:
    root_dir: Path
    runtime_dir: Path
    compose_file: Path
    container_name: str
    image: str
    network_name: str
    ip: str
