package fabric_msp

import (
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/ibc-go/v8/modules/core/exported"
)

// RegisterInterfaces registra ClientState/ConsensusState/Header no
// InterfaceRegistry do simd, permitindo empacotamento em Any.
//
// Header precisa ser registrado como exported.ClientMessage (não só
// ClientState/ConsensusState), sem isso, MsgUpdateClient falha em runtime com "unable to resolve type URL" assim
// que o relayer tenta atualizar o client de verdade. Registrando os três desde já evita repetir
func RegisterInterfaces(registry codectypes.InterfaceRegistry) {
	registry.RegisterImplementations(
		(*exported.ClientState)(nil),
		&ClientState{},
	)
	registry.RegisterImplementations(
		(*exported.ConsensusState)(nil),
		&ConsensusState{},
	)
	registry.RegisterImplementations(
		(*exported.ClientMessage)(nil),
		&Header{},
	)
}
