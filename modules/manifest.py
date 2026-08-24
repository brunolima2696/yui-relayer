import json
from pathlib import Path
from typing import Any

from .errors import ConfigError
from .models import DescriptorConfig, DescriptorSources, RuntimeConfig


MANIFEST_VERSION = 1


def manifest_path(runtime: RuntimeConfig) -> Path:
    return runtime.runtime_dir / "manifest.json"


def _paths(value: Any, field: str, path: Path) -> tuple[Path, ...]:
    if not isinstance(value, list) or not value:
        raise ConfigError(f"{path}: sources.{field} deve ser uma lista nao vazia")
    if not all(isinstance(item, str) and item.strip() for item in value):
        raise ConfigError(f"{path}: sources.{field} contem caminho invalido")
    return tuple(Path(item).expanduser().resolve() for item in value)


def read_manifest_sources(runtime: RuntimeConfig) -> DescriptorSources | None:
    path = manifest_path(runtime)
    if not path.exists():
        return None
    try:
        document = json.loads(path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as exc:
        raise ConfigError(
            f"Manifesto invalido em {path}:{exc.lineno}:{exc.colno}: {exc.msg}"
        ) from exc
    if not isinstance(document, dict) or document.get("version") != MANIFEST_VERSION:
        raise ConfigError(f"{path}: versao de manifesto nao suportada")
    sources = document.get("sources")
    if not isinstance(sources, dict):
        raise ConfigError(f"{path}: sources deve ser um objeto")
    return DescriptorSources(
        profile_files=_paths(sources.get("profiles"), "profiles", path),
        chain_files=_paths(sources.get("chains"), "chains", path),
        account_files=_paths(
            sources.get("relayer_accounts"), "relayer_accounts", path
        ),
    )


def _merge_paths(*groups: tuple[Path, ...]) -> tuple[Path, ...]:
    merged: list[Path] = []
    for group in groups:
        for path in group:
            resolved = path.expanduser().resolve()
            if resolved not in merged:
                merged.append(resolved)
    return tuple(merged)


def resolve_sources(
    runtime: RuntimeConfig,
    profile_files: list[Path] | None,
    chain_files: list[Path] | None,
    account_files: list[Path] | None,
) -> DescriptorSources:
    provided = (bool(profile_files), bool(chain_files), bool(account_files))
    if any(provided) and not all(provided):
        raise ConfigError(
            "Informe juntos --profile, --chains e --relayer-accounts"
        )

    existing = read_manifest_sources(runtime)
    if not any(provided):
        if existing is None:
            raise ConfigError(
                "Manifesto ainda nao existe; informe --profile, --chains e "
                "--relayer-accounts no primeiro init"
            )
        return existing

    supplied = DescriptorSources(
        profile_files=tuple(profile_files or ()),
        chain_files=tuple(chain_files or ()),
        account_files=tuple(account_files or ()),
    )
    if existing is None:
        return DescriptorSources(
            profile_files=_merge_paths(supplied.profile_files),
            chain_files=_merge_paths(supplied.chain_files),
            account_files=_merge_paths(supplied.account_files),
        )
    return DescriptorSources(
        profile_files=_merge_paths(existing.profile_files, supplied.profile_files),
        chain_files=_merge_paths(existing.chain_files, supplied.chain_files),
        account_files=_merge_paths(existing.account_files, supplied.account_files),
    )


def manifest_document(
    sources: DescriptorSources,
    config: DescriptorConfig,
) -> dict:
    return {
        "version": MANIFEST_VERSION,
        "sources": {
            "profiles": [path.as_posix() for path in sources.profile_files],
            "chains": [path.as_posix() for path in sources.chain_files],
            "relayer_accounts": [
                path.as_posix() for path in sources.account_files
            ],
        },
        "profiles": {
            profile.name: {
                "adapter": profile.adapter,
                "source": profile.source_file.as_posix(),
            }
            for profile in config.profiles
        },
        "chains": {
            resolved.chain.name: {
                "profile": resolved.profile.name,
                "chain_id": resolved.chain.chain_id,
                "service": resolved.chain.service,
                "rpc_addr": resolved.chain.rpc_addr,
                "key_name": resolved.account.name,
                "profile_source": resolved.profile.source_file.as_posix(),
                "chains_source": resolved.chain.source_file.as_posix(),
                "accounts_source": resolved.account.source_file.as_posix(),
                "runtime_config": f"chains/{resolved.chain.name}.json",
            }
            for resolved in config.resolved_chains
        },
    }


def write_manifest(
    runtime: RuntimeConfig,
    sources: DescriptorSources,
    config: DescriptorConfig,
) -> Path:
    path = manifest_path(runtime)
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(".json.tmp")
    temporary.write_text(
        json.dumps(manifest_document(sources, config), ensure_ascii=False, indent=2)
        + "\n",
        encoding="utf-8",
    )
    temporary.replace(path)
    return path
