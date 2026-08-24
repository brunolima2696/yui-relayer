import argparse
from pathlib import Path

from .config import load_descriptors, load_runtime, select_chains
from .errors import ConfigError
from .lifecycle import (
    build_and_start,
    start_service,
    status,
    verify_docker,
    verify_network,
)
from .manifest import resolve_sources, write_manifest
from .output import stage
from .relayer import (
    configure_path,
    ensure_global_config,
    import_keys,
    initialize_light_clients,
    register_chains,
)
from .render import path_name, render_chain_files, render_compose


def _load(args: argparse.Namespace, root_dir: Path):
    runtime = load_runtime(root_dir, args.runtime_dir, args.env_file)
    sources = resolve_sources(
        runtime,
        getattr(args, "profile_files", None),
        getattr(args, "chain_files", None),
        getattr(args, "account_files", None),
    )
    descriptors = load_descriptors(
        sources.profile_files,
        sources.chain_files,
        sources.account_files,
    )
    chains = select_chains(descriptors, getattr(args, "selected_chains", None))
    return descriptors, chains, runtime, sources


def _print_chains(chains) -> None:
    print("Chains selecionadas: " + ", ".join(item.chain.name for item in chains))


def command_validate(args: argparse.Namespace, root_dir: Path) -> None:
    with stage("validar descritores e ambiente YUI"):
        _, chains, runtime, _ = _load(args, root_dir)
        _print_chains(chains)
        print(
            f"Relayer: IP={runtime.ip} rede={runtime.network_name} "
            f"runtime={runtime.runtime_dir}"
        )


def command_render(args: argparse.Namespace, root_dir: Path) -> None:
    with stage("validar descritores e ambiente YUI"):
        _, chains, runtime, _ = _load(args, root_dir)
        _print_chains(chains)
    with stage("gerar Compose e configuracoes das chains"):
        compose = render_compose(runtime)
        files = render_chain_files(runtime, chains)
        print(f"Compose: {compose}")
        for path in files:
            print(f"Chain config: {path}")


def command_init(args: argparse.Namespace, root_dir: Path) -> None:
    with stage("validar descritores e ambiente YUI"):
        descriptors, chains, runtime, sources = _load(args, root_dir)
        _print_chains(chains)

    with stage("gerar Compose e configuracoes das chains"):
        print(f"Compose: {render_compose(runtime)}")
        for path in render_chain_files(runtime, chains):
            print(f"Chain config: {path}")
        print(f"Manifesto: {write_manifest(runtime, sources, descriptors)}")

    with stage("verificar Docker e rede externa"):
        verify_docker(runtime)
        verify_network(runtime)

    with stage("construir e iniciar o container YUI"):
        build_and_start(runtime, build=not args.no_build)

    with stage("inicializar a configuracao global do YUI"):
        ensure_global_config(runtime)

    with stage("registrar as chains no YUI"):
        register_chains(runtime, chains)

    with stage("importar as chaves dos relayers"):
        import_keys(runtime, chains)

    with stage("inicializar os light clients locais"):
        initialize_light_clients(runtime, chains)

    print("YUI inicializado. Nenhum path ou servico de relay foi iniciado.")


def command_path(args: argparse.Namespace, root_dir: Path) -> None:
    if args.source == args.destination:
        raise ConfigError("As chains de origem e destino devem ser diferentes")
    with stage("validar descritores do path"):
        descriptors, _, runtime, _ = _load(args, root_dir)
        selected = select_chains(descriptors, [args.source, args.destination])
        if len(selected) != 2:
            raise ConfigError("O path exige duas chains diferentes")
        source, destination = selected
        name = args.name or path_name(source.chain.name, destination.chain.name)
        print(
            f"Path {name}: {source.chain.name} <-> {destination.chain.name}"
        )

    with stage("verificar o container YUI"):
        verify_docker(runtime)

    with stage(f"configurar clients, connection e channel de {name}"):
        configure_path(runtime, name, source, destination)

    print(
        f"Path {name} pronto. Inicie o relay com: "
        f"python main.py start {name}"
    )


def command_start(args: argparse.Namespace, root_dir: Path) -> None:
    runtime = load_runtime(root_dir, args.runtime_dir, args.env_file)
    with stage(f"iniciar o servico de relay {args.path}"):
        verify_docker(runtime)
        start_service(runtime, args.path)


def command_status(args: argparse.Namespace, root_dir: Path) -> None:
    runtime = load_runtime(root_dir, args.runtime_dir, args.env_file)
    with stage("consultar container e configuracao YUI"):
        verify_docker(runtime)
        status(runtime)


COMMANDS = {
    "validate": command_validate,
    "render": command_render,
    "init": command_init,
    "path": command_path,
    "start": command_start,
    "status": command_status,
}
