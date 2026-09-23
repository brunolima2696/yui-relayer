import json
import shutil
from pathlib import Path

from .models import ResolvedChain, RuntimeConfig


def write_json(path: Path, content: object) -> Path:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(
        json.dumps(content, ensure_ascii=False, indent=2) + "\n",
        encoding="utf-8",
    )
    return path


def chain_document(resolved: ResolvedChain) -> dict:
    if resolved.profile.adapter == "ethereum":
        return ethereum_chain_document(resolved)
    if resolved.profile.adapter == "fabric":
        return fabric_chain_document(resolved)
    return tendermint_chain_document(resolved)


def tendermint_chain_document(resolved: ResolvedChain) -> dict:
    chain = resolved.chain
    settings = resolved.profile.relayer
    return {
        "chain": {
            "@type": "/relayer.chains.tendermint.config.ChainConfig",
            "key": resolved.account.name,
            "chain_id": chain.chain_id,
            "rpc_addr": chain.rpc_addr,
            "account_prefix": resolved.profile.account_prefix,
            "gas_adjustment": settings["gas_adjustment"],
            "gas_prices": settings["gas_prices"],
            "average_block_time_msec": settings["average_block_time_msec"],
            "max_retry_for_commit": settings["max_retry_for_commit"],
        },
        "prover": {
            "@type": "/relayer.chains.tendermint.config.ProverConfig",
            "trusting_period": settings["trusting_period"],
            "refresh_threshold_rate": {
                "numerator": 2,
                "denominator": 3,
            },
        },
    }


def ethereum_chain_document(resolved: ResolvedChain) -> dict:
    chain = resolved.chain
    settings = resolved.profile.relayer
    prover = resolved.profile.prover
    if prover is None:
        raise ValueError(f"Profile Ethereum sem prover: {resolved.profile.name}")
    derivation_path = (
        resolved.account.derivation_path
        or settings["signer"]["derivation_path"]
    )
    return {
        "chain": {
            "@type": "/relayer.chains.ethereum.config.ChainConfig",
            "chain_id": chain.chain_id,
            "eth_chain_id": chain.eth_chain_id,
            "rpc_addr": chain.rpc_addr,
            "signer": {
                "@type": "/relayer.signers.hd.SignerConfig",
                "mnemonic": resolved.account.mnemonic,
                "path": derivation_path,
            },
            "ibc_address": chain.ibc_address,
            "average_block_time_msec": settings["average_block_time_msec"],
            "max_retry_for_inclusion": settings["max_retry_for_inclusion"],
            "gas_estimate_rate": settings["gas_estimate_rate"],
            "max_gas_limit": settings["max_gas_limit"],
            "tx_type": settings["tx_type"],
            "abi_paths": list(chain.abi_paths),
        },
        "prover": {
            "@type": "/relayer.provers.qbft.config.ProverConfig",
            "consensus_type": prover["consensus_type"],
            "trusting_period": prover["trusting_period"],
            "max_clock_drift": prover["max_clock_drift"],
            "refresh_threshold_rate": prover["refresh_threshold_rate"],
        },
    }


def fabric_chain_document(resolved: ResolvedChain) -> dict:
    chain = resolved.chain
    settings = resolved.profile.relayer
    prover = resolved.profile.prover
    if prover is None:
        raise ValueError(f"Profile Fabric sem prover: {resolved.profile.name}")
    config = chain.adapter_config
    artifact_root = f"/root/.yui-relayer/fabric/{chain.name}"

    msp_info = {
        "msp_id": config["msp_id"],
        "config_path": f"{artifact_root}/{config['msp_config_file']}",
    }
    if config["msp_policy_file"]:
        msp_info["policy_path"] = (
            f"{artifact_root}/{config['msp_policy_file']}"
        )

    return {
        "chain": {
            "@type": "/relayer.chains.fabric.config.ChainConfig",
            "chain_id": chain.chain_id,
            "channel_id": config["channel_id"],
            "chaincode_name": config["chaincode_name"],
            "gateway_endpoint": config["gateway_endpoint"],
            "tls_ca_cert_path": (
                f"{artifact_root}/{config['tls_ca_cert_file']}"
            ),
            "gateway_host_override": config["gateway_host_override"],
            "msp_id": config["msp_id"],
            "cert_path": f"{artifact_root}/{config['cert_file']}",
            "key_path": f"{artifact_root}/{config['key_file']}",
            "msp_infos": [msp_info],
            "chaincode_info": {
                "path": config["chaincode_path"],
                "name": config["chaincode_name"],
                "version": config["chaincode_version"],
                "endorsement_policy_path": (
                    f"{artifact_root}/{config['endorsement_policy_file']}"
                ),
                "ibc_policy_path": (
                    f"{artifact_root}/{config['ibc_policy_file']}"
                ),
            },
            "average_block_time_msec": settings["average_block_time_msec"],
        },
        "prover": {
            "@type": "/relayer.chains.fabric.config.ProverConfig",
            "trusting_period_sec": prover["trusting_period_sec"],
            "max_clock_drift_sec": prover["max_clock_drift_sec"],
        },
    }


def stage_fabric_artifacts(
    runtime: RuntimeConfig,
    resolved: ResolvedChain,
) -> None:
    if resolved.profile.adapter != "fabric":
        return
    config = resolved.chain.adapter_config
    source_dir = Path(config["artifacts_dir"])
    destination_dir = runtime.runtime_dir / "fabric" / resolved.chain.name
    destination_dir.mkdir(parents=True, exist_ok=True)
    names = {
        config["tls_ca_cert_file"],
        config["cert_file"],
        config["key_file"],
        config["msp_config_file"],
        config["endorsement_policy_file"],
        config["ibc_policy_file"],
    }
    if config["msp_policy_file"]:
        names.add(config["msp_policy_file"])
    for name in sorted(names):
        source = source_dir / name
        if not source.is_file():
            raise FileNotFoundError(f"Artefato Fabric nao encontrado: {source}")
        shutil.copy2(source, destination_dir / name)


def render_chain_files(
    runtime: RuntimeConfig,
    chains: tuple[ResolvedChain, ...],
) -> tuple[Path, ...]:
    destinations = []
    for resolved in chains:
        stage_fabric_artifacts(runtime, resolved)
        destination = runtime.runtime_dir / "chains" / f"{resolved.chain.name}.json"
        destinations.append(write_json(destination, chain_document(resolved)))
    return tuple(destinations)


def path_name(source: str, destination: str) -> str:
    prefix = "xrplevm-"
    if source.startswith(prefix) and destination.startswith(prefix):
        return (
            f"{prefix}{source.removeprefix(prefix)}-"
            f"{destination.removeprefix(prefix)}"
        )
    return f"{source}-{destination}"


def path_document(source: ResolvedChain, destination: ResolvedChain) -> dict:
    endpoint = {
        "client-id": "",
        "connection-id": "",
        "channel-id": "",
        "port-id": "transfer",
        "order": "unordered",
        "version": "ics20-1",
    }
    return {
        "src": {"chain-id": source.chain.chain_id, **endpoint},
        "dst": {"chain-id": destination.chain.chain_id, **endpoint},
        "strategy": {"type": "naive"},
    }


def render_path_file(
    runtime: RuntimeConfig,
    name: str,
    source: ResolvedChain,
    destination: ResolvedChain,
) -> Path:
    return write_json(
        runtime.runtime_dir / "paths" / f"{name}.json",
        path_document(source, destination),
    )


def compose_text(runtime: RuntimeConfig) -> str:
    default_runtime = (runtime.root_dir / "runtime").resolve()
    root = json.dumps(".")
    runtime_source = (
        "./runtime"
        if runtime.runtime_dir == default_runtime
        else runtime.runtime_dir.as_posix()
    )
    runtime_dir = json.dumps(runtime_source)
    image = json.dumps(runtime.image)
    container = json.dumps(runtime.container_name)
    network = json.dumps(runtime.network_name)
    ip = json.dumps(runtime.ip)
    return f"""# Generated by main.py. Do not edit manually.
name: yui-relayer-module

services:
  yui-relayer:
    image: {image}
    build:
      context: {root}
      dockerfile: Dockerfile
    container_name: {container}
    entrypoint: ["sleep"]
    command: ["infinity"]
    restart: unless-stopped
    extra_hosts:
      - "host.docker.internal:host-gateway"
    volumes:
      - type: bind
        source: {runtime_dir}
        target: /root/.yui-relayer
    networks:
      interoperability:
        ipv4_address: {ip}
    healthcheck:
      test: ["CMD-SHELL", "test -x /usr/local/bin/yrly"]
      interval: 5s
      timeout: 3s
      retries: 10

networks:
  interoperability:
    name: {network}
    external: true
"""


def render_compose(runtime: RuntimeConfig) -> Path:
    runtime.runtime_dir.mkdir(parents=True, exist_ok=True)
    runtime.compose_file.write_text(compose_text(runtime), encoding="utf-8")
    return runtime.compose_file
