package main

import (
	"log"

	besuqbft "github.com/datachainlab/besu-ibc-relay-prover/module"
	ethereum "github.com/datachainlab/ethereum-ibc-relay-chain/pkg/relay/ethereum"
	hd "github.com/datachainlab/ibc-hd-signer/pkg/hd"
	debug_chain "github.com/hyperledger-labs/yui-relayer/chains/debug/module"
	fabric "github.com/hyperledger-labs/yui-relayer/chains/fabric"
	tendermint "github.com/hyperledger-labs/yui-relayer/chains/tendermint/module"
	"github.com/hyperledger-labs/yui-relayer/cmd"
	debug_prover "github.com/hyperledger-labs/yui-relayer/provers/debug/module"
	mock "github.com/hyperledger-labs/yui-relayer/provers/mock/module"
)

func main() {
	if err := cmd.Execute(
		tendermint.Module{},
		fabric.Module{},
		ethereum.Module{},
		hd.Module{},
		besuqbft.Module{},
		mock.Module{},
		debug_chain.Module{},
		debug_prover.Module{},
	); err != nil {
		log.Fatal(err)
	}
}
