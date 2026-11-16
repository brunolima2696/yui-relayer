package main

import (
	"log"
	"os"

	besuqbft "github.com/datachainlab/besu-ibc-relay-prover/module"
	ethereum "github.com/datachainlab/ethereum-ibc-relay-chain/pkg/relay/ethereum"
	hd "github.com/datachainlab/ibc-hd-signer/pkg/hd"
	debug_chain "github.com/hyperledger-labs/yui-relayer/chains/debug/module"
	fabric "github.com/hyperledger-labs/yui-relayer/chains/fabric"
	tendermint "github.com/hyperledger-labs/yui-relayer/chains/tendermint/module"
	"github.com/hyperledger-labs/yui-relayer/cmd"
	relaylog "github.com/hyperledger-labs/yui-relayer/log"
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
		if logger := relaylog.GetLogger(); logger != nil {
			logger.Error("YUI encerrado com erro", err)
		} else {
			log.Print(err)
		}
		os.Exit(1)
	}
}
