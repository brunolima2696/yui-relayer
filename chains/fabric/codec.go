package fabric

import (
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/ibc-go/v8/modules/core/exported"
	"github.com/hyperledger-labs/yui-relayer/core"

	fabricmsp "github.com/hyperledger-labs/yui-relayer/lightclients/fabric-msp"
)

// RegisterInterfaces registra ChainConfig/ProverConfig/MsgID no
// InterfaceRegistry e também os tipos reais do client fabric-msp
// (ClientState/ConsensusState/Header) como implementações de
// exported.ClientState/ConsensusState/ClientMessage - mesmo papel que
// besuqft.Module faz pro hb-qbft: o Prover deste módulo (prover.go)
// produz esses tipos pra montar MsgCreateClient/MsgUpdateClient
// enviados PRO LADO COSMOS (via o módulo tendermint), então o codec
// compartilhado do relayer precisa saber decodificá-los/empacotá-los.
// Sem isso, um Any empacotando um fabricmsp.ClientState não resolveria
func RegisterInterfaces(registry codectypes.InterfaceRegistry) {
	registry.RegisterImplementations(
		(*core.ChainConfig)(nil),
		&ChainConfig{},
	)
	registry.RegisterImplementations(
		(*core.ProverConfig)(nil),
		&ProverConfig{},
	)
	registry.RegisterImplementations(
		(*core.MsgID)(nil),
		&MsgID{},
	)
	registry.RegisterImplementations(
		(*exported.ClientState)(nil),
		&fabricmsp.ClientState{},
	)
	registry.RegisterImplementations(
		(*exported.ConsensusState)(nil),
		&fabricmsp.ConsensusState{},
	)
	registry.RegisterImplementations(
		(*exported.ClientMessage)(nil),
		&fabricmsp.Header{},
	)
}
