from dataclasses import dataclass
from pathlib import Path


@dataclass(frozen=True)
class RelayerSettings:
    gas_adjustment: float
    gas_prices: str
    average_block_time_msec: int
    max_retry_for_commit: int
    trusting_period: str


@dataclass(frozen=True)
class Profile:
    name: str
    adapter: str
    account_prefix: str
    relayer: RelayerSettings
    source_file: Path


@dataclass(frozen=True)
class Chain:
    name: str
    profile: str
    chain_id: str
    service: str
    rpc_addr: str
    source_file: Path


@dataclass(frozen=True)
class RelayerAccount:
    name: str
    chains: tuple[str, ...]
    mnemonic: str
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
