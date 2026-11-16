import argparse
import sys
from pathlib import Path

from .commands import COMMANDS
from .errors import YuiError


def _path(value: str) -> Path:
    return Path(value).expanduser().resolve()


def _add_runtime_options(parser: argparse.ArgumentParser, root_dir: Path) -> None:
    parser.add_argument(
        "--env-file",
        type=_path,
        default=root_dir / ".env",
        help=f"ambiente do relayer (padrao: {root_dir / '.env'})",
    )
    parser.add_argument(
        "--runtime-dir",
        type=_path,
        default=root_dir / "runtime",
        help=f"runtime persistente (padrao: {root_dir / 'runtime'})",
    )


def _add_descriptor_options(parser: argparse.ArgumentParser) -> None:
    parser.add_argument(
        "--profile",
        action="append",
        dest="profile_files",
        type=_path,
        metavar="FILE",
        help=(
            "profile.json de um modulo; pode ser repetido; obrigatorio "
            "enquanto nao existir manifest.json"
        ),
    )
    parser.add_argument(
        "--chains",
        action="append",
        dest="chain_files",
        type=_path,
        metavar="FILE",
        help=(
            "chains.json de um modulo; pode ser repetido; obrigatorio "
            "enquanto nao existir manifest.json"
        ),
    )
    parser.add_argument(
        "--relayer-accounts",
        action="append",
        dest="account_files",
        type=_path,
        metavar="FILE",
        help=(
            "relayer-accounts.json; pode ser repetido; obrigatorio "
            "enquanto nao existir manifest.json"
        ),
    )


def _add_chain_selector(parser: argparse.ArgumentParser) -> None:
    parser.add_argument(
        "--chain",
        action="append",
        dest="selected_chains",
        metavar="CHAIN",
        help="chain a configurar; pode ser repetido; usa todas por padrao",
    )


def build_parser(root_dir: Path) -> argparse.ArgumentParser:
    runtime = argparse.ArgumentParser(add_help=False)
    _add_runtime_options(runtime, root_dir)

    configured = argparse.ArgumentParser(add_help=False, parents=[runtime])
    _add_descriptor_options(configured)

    parser = argparse.ArgumentParser(
        description="Configura e executa o YUI Relayer de forma modular."
    )
    subparsers = parser.add_subparsers(dest="command", required=True)

    validate = subparsers.add_parser(
        "validate",
        parents=[configured],
        help="valida descritores sem alterar o runtime",
    )
    _add_chain_selector(validate)

    render = subparsers.add_parser(
        "render",
        parents=[configured],
        help="gera o Compose e as configuracoes de chain",
    )
    _add_chain_selector(render)

    init = subparsers.add_parser(
        "init",
        parents=[configured],
        help="sobe o relayer e registra as chains",
    )
    _add_chain_selector(init)
    init.add_argument("--no-build", action="store_true", help="nao faz build da imagem")

    path = subparsers.add_parser(
        "path",
        parents=[configured],
        help="cria path, IBC clients, connection e channel",
    )
    path.add_argument("source", help="chain de origem")
    path.add_argument("destination", help="chain de destino")
    path.add_argument("--name", help="nome explicito do path")
    path.add_argument("--port", default="transfer", help="porta IBC nos dois lados (padrao: transfer)")
    path.add_argument("--version", help="versao da aplicacao; inferida para transfer e fabric-events")

    start = subparsers.add_parser(
        "start",
        parents=[runtime],
        help="inicia o servico de relay para um path",
    )
    start.add_argument("path", help="nome do path configurado")
    start.add_argument("--debug", action="store_true", help="exibe diagnosticos completos do yrly")
    start.add_argument("--log-format", choices=("compact", "json", "text"), default="compact", help="formato dos logs do relay (padrao: compact)")
    start.add_argument("--health-interval", type=float, default=30.0, metavar="SECONDS", help="intervalo dos healthchecks em segundos (padrao: 30; 0 desabilita)")

    subparsers.add_parser(
        "status",
        parents=[runtime],
        help="exibe o container e a configuracao nativa do YUI",
    )
    return parser


def run(root_dir: Path, argv: list[str] | None = None) -> int:
    root_dir = root_dir.resolve()
    argv = list(sys.argv[1:] if argv is None else argv)
    parser = build_parser(root_dir)
    if not argv:
        parser.print_help()
        return 0
    args = parser.parse_args(argv)
    try:
        COMMANDS[args.command](args, root_dir)
    except (YuiError, OSError) as exc:
        print(f"Falha: {exc}", file=sys.stderr)
        return 1
    return 0
