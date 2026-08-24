import json
import sys
import tempfile
import unittest
from pathlib import Path


YUI_ROOT = Path(__file__).resolve().parents[1]
if str(YUI_ROOT) not in sys.path:
    sys.path.insert(0, str(YUI_ROOT))

from modules.cli import build_parser
from modules.config import load_descriptors, load_runtime, select_chains
from modules.errors import ConfigError
from modules.manifest import (
    read_manifest_sources,
    resolve_sources,
    write_manifest,
)
from modules.models import DescriptorSources
from modules.render import chain_document, path_name, render_chain_files, render_compose


def write_json(path: Path, content: object) -> Path:
    path.write_text(json.dumps(content), encoding="utf-8")
    return path


class OrchestratorTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name)
        self.profile = write_json(
            self.root / "profile.json",
            {
                "name": "test-profile",
                "adapter": "tendermint",
                "account_prefix": "cosmos",
                "relayer": {
                    "gas_adjustment": 1.5,
                    "gas_prices": "0.025stake",
                    "average_block_time_msec": 1000,
                    "max_retry_for_commit": 5,
                    "trusting_period": "336h",
                },
            },
        )
        self.chains = write_json(
            self.root / "chains.json",
            {
                "chains": [
                    {
                        "name": "chain-a",
                        "profile": "test-profile",
                        "chain_id": "chain-a-1",
                        "service": "chain-a",
                    },
                    {
                        "name": "chain-b",
                        "profile": "test-profile",
                        "chain_id": "chain-b-1",
                        "service": "chain-b",
                    },
                ]
            },
        )
        self.accounts = write_json(
            self.root / "relayer-accounts.json",
            {
                "accounts": [
                    {
                        "name": "relayer-a",
                        "chains": ["chain-a"],
                        "mnemonic": "one two three four five six seven eight nine ten eleven twelve",
                    },
                    {
                        "name": "relayer-b",
                        "chains": ["chain-b"],
                        "mnemonic": "twelve eleven ten nine eight seven six five four three two one",
                    },
                ]
            },
        )
        self.env = self.root / ".env"
        self.env.write_text(
            "YUI_RELAYER_IP=172.30.0.20\n"
            "DOCKER_NETWORK_NAME=interoperability_network\n",
            encoding="utf-8",
        )

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def load(self):
        return load_descriptors(
            (self.profile,),
            (self.chains,),
            (self.accounts,),
        )

    def test_descriptors_are_joined_by_profile_and_chain(self) -> None:
        config = self.load()
        self.assertEqual(len(config.resolved_chains), 2)
        self.assertEqual(config.resolved_chains[0].account.name, "relayer-a")
        self.assertEqual(config.resolved_chains[1].profile.account_prefix, "cosmos")

    def test_chain_can_be_selected_by_name(self) -> None:
        selected = select_chains(self.load(), ["chain-b"])
        self.assertEqual([item.chain.name for item in selected], ["chain-b"])

    def test_chain_document_uses_profile_and_relayer_account(self) -> None:
        document = chain_document(self.load().resolved_chains[0])
        self.assertEqual(document["chain"]["key"], "relayer-a")
        self.assertEqual(document["chain"]["rpc_addr"], "http://chain-a:26657")
        self.assertEqual(document["prover"]["trusting_period"], "336h")

    def test_compose_stays_at_repository_root_and_state_goes_to_runtime(self) -> None:
        runtime_dir = self.root / "runtime"
        runtime = load_runtime(self.root, runtime_dir, self.env)
        compose = render_compose(runtime)
        chain_files = render_chain_files(runtime, self.load().resolved_chains)
        self.assertEqual(compose, self.root / "docker-compose.yaml")
        self.assertTrue(all(runtime_dir in path.parents for path in chain_files))
        self.assertIn('ipv4_address: "172.30.0.20"', compose.read_text())

    def test_path_names_preserve_the_xrpl_short_form(self) -> None:
        self.assertEqual(path_name("xrplevm-a", "xrplevm-b"), "xrplevm-a-b")
        self.assertEqual(
            path_name("xrplevm-a", "cosmos-chain-1"),
            "xrplevm-a-cosmos-chain-1",
        )

    def test_cli_accepts_explicit_descriptor_paths(self) -> None:
        args = build_parser(self.root).parse_args(
            [
                "init",
                "--profile",
                str(self.profile),
                "--chains",
                str(self.chains),
                "--relayer-accounts",
                str(self.accounts),
                "--chain",
                "chain-a",
            ]
        )
        self.assertEqual(args.selected_chains, ["chain-a"])

    def test_manifest_reuses_sources_without_storing_mnemonics(self) -> None:
        runtime = load_runtime(self.root, self.root / "runtime", self.env)
        sources = DescriptorSources(
            profile_files=(self.profile.resolve(),),
            chain_files=(self.chains.resolve(),),
            account_files=(self.accounts.resolve(),),
        )
        manifest = write_manifest(runtime, sources, self.load())
        content = manifest.read_text(encoding="utf-8")
        self.assertNotIn("one two three", content)
        self.assertEqual(read_manifest_sources(runtime), sources)
        self.assertEqual(resolve_sources(runtime, None, None, None), sources)

    def test_first_execution_requires_all_descriptor_groups(self) -> None:
        runtime = load_runtime(self.root, self.root / "runtime", self.env)
        with self.assertRaises(ConfigError):
            resolve_sources(runtime, [self.profile], None, None)
        with self.assertRaises(ConfigError):
            resolve_sources(runtime, None, None, None)

    def test_path_command_accepts_manifest_only_execution(self) -> None:
        args = build_parser(self.root).parse_args(
            ["path", "chain-a", "chain-b"]
        )
        self.assertIsNone(args.profile_files)
        self.assertIsNone(args.chain_files)
        self.assertIsNone(args.account_files)


if __name__ == "__main__":
    unittest.main()
