// Diferença central em relação a hb-qbft/07-tendermint: aqui não há
// validator set nem uma raiz de estado única por altura, a raiz
// de confiança são as MSPInfos (certificados MSP de cada organização) e a
// política de endosso vigente do chaincode (LastChaincodeInfo). Cada
// valor é verificado por uma prova de endosso ad-hoc contra essa
// política, não por uma prova Merkle genérica contra um Root.
package fabric_msp

import (
	"fmt"

	storetypes "cosmossdk.io/store/types"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	"github.com/cosmos/ibc-go/v8/modules/core/exported"
)

// ClientType é o mesmo client type usado em todo o projeto pra registrar
// esse light client
const ClientType = "fabric-msp"

var _ exported.ClientState = (*ClientState)(nil)

// NewClientState cria um novo ClientState fabric-msp.
func NewClientState(id string, chaincodeHeader ChaincodeHeader, chaincodeInfo ChaincodeInfo, mspInfos MSPInfos) *ClientState {
	return &ClientState{
		Id:                  id,
		LastChaincodeHeader: chaincodeHeader,
		LastChaincodeInfo:   chaincodeInfo,
		LastMspInfos:        mspInfos,
	}
}

func (cs *ClientState) ClientType() string {
	return ClientType
}

// GetLatestHeight usa o contador de sequência do commitment log do
// chaincode como altura, não existe block height nesse modelo mesma ideia da v0.2 do yui
func (cs *ClientState) GetLatestHeight() exported.Height {
	return clienttypes.NewHeight(0, cs.LastChaincodeHeader.Sequence.Value)
}

func (cs *ClientState) Validate() error {
	if cs.Id == "" {
		return fmt.Errorf("client id cannot be blank")
	}
	if cs.LastChaincodeInfo.ChannelId == "" {
		return fmt.Errorf("channel id cannot be blank")
	}
	if cs.GetLatestHeight().IsZero() {
		return fmt.Errorf("latest height must be > 0")
	}
	return nil
}

// Status: sem noção de Frozen/Expired ainda, mesmo
// ponto de partida que hb-qbft tinha antes de existir trusting period.
func (cs *ClientState) Status(ctx sdk.Context, clientStore storetypes.KVStore, cdc codec.BinaryCodec) exported.Status {
	return exported.Active
}

// ExportMetadata: sem metadata adicional pra exportar
func (cs *ClientState) ExportMetadata(clientStore storetypes.KVStore) []exported.GenesisMetadata {
	return nil
}

func (cs *ClientState) ZeroCustomFields() exported.ClientState {
	return NewClientState(cs.Id, cs.LastChaincodeHeader, cs.LastChaincodeInfo, cs.LastMspInfos)
}

// GetTimestampAtHeight lê o ConsensusState persistido naquela altura, mesmo padrão de hb-qbft
func (cs *ClientState) GetTimestampAtHeight(ctx sdk.Context, clientStore storetypes.KVStore, cdc codec.BinaryCodec, height exported.Height) (uint64, error) {
	consState, found := getConsensusState(clientStore, cdc, height)
	if !found {
		return 0, fmt.Errorf("consensus state not found at height %s", height)
	}
	return consState.GetTimestamp(), nil
}

func (cs *ClientState) Initialize(ctx sdk.Context, cdc codec.BinaryCodec, clientStore storetypes.KVStore, consensusState exported.ConsensusState) error {
	consState, ok := consensusState.(*ConsensusState)
	if !ok {
		return fmt.Errorf("invalid initial consensus state. expected type: %T, got: %T", &ConsensusState{}, consensusState)
	}
	setClientState(clientStore, cdc, cs)
	setConsensusState(clientStore, cdc, consState, cs.GetLatestHeight())
	return nil
}

// CheckSubstituteAndUpdateState: client recovery fica fora desta fase, mesmo recorte já usado em hb-qbft.
func (cs *ClientState) CheckSubstituteAndUpdateState(ctx sdk.Context, cdc codec.BinaryCodec, subjectClientStore, substituteClientStore storetypes.KVStore, substituteClient exported.ClientState) error {
	return fmt.Errorf("fabric-msp: client recovery not implemented (out of scope, see nextsteps.md T27)")
}

// VerifyUpgradeAndUpdateState: chain upgrades fora do escopo desta fase.
func (cs *ClientState) VerifyUpgradeAndUpdateState(ctx sdk.Context, cdc codec.BinaryCodec, store storetypes.KVStore, newClient exported.ClientState, newConsState exported.ConsensusState, proofUpgradeClient, proofUpgradeConsState []byte) error {
	return fmt.Errorf("fabric-msp: chain upgrades not implemented (out of scope, see nextsteps.md T27)")
}
