package fabric_msp

import (
	"fmt"

	storetypes "cosmossdk.io/store/types"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	"github.com/cosmos/ibc-go/v8/modules/core/exported"
)

func (cs *ClientState) VerifyClientMessage(ctx sdk.Context, cdc codec.BinaryCodec, clientStore storetypes.KVStore, clientMsg exported.ClientMessage) error {
	header, ok := clientMsg.(*Header)
	if !ok {
		return fmt.Errorf("fabric-msp: unexpected client message type %T", clientMsg)
	}
	if err := header.ValidateBasic(); err != nil {
		return err
	}
	if header.ChaincodeHeader != nil {
		if err := cs.verifyChaincodeHeader(*header.ChaincodeHeader); err != nil {
			return err
		}
	}
	if header.ChaincodeInfo != nil {
		if err := cs.verifyChaincodeInfo(*header.ChaincodeInfo); err != nil {
			return err
		}
	}
	if header.MspHeaders != nil {
		for _, mh := range header.MspHeaders.Headers {
			if err := cs.verifyMSPHeader(mh); err != nil {
				return err
			}
		}
	}
	return nil
}

// CheckForMisbehaviour: sempre false (ex.: dois Headers
// conflitantes endossados pela mesma política, ou duas sequências
// concorrentes) fica fora do escopo, mesmo recorte já usado em hb-qbft.
func (cs *ClientState) CheckForMisbehaviour(ctx sdk.Context, cdc codec.BinaryCodec, clientStore storetypes.KVStore, clientMsg exported.ClientMessage) bool {
	return false
}

// UpdateStateOnMisbehaviour nunca é chamado (CheckForMisbehaviour sempre
// false) - panic explícito só pra deixar isso claro se algum dia deixar de
// ser verdade.
func (cs *ClientState) UpdateStateOnMisbehaviour(ctx sdk.Context, cdc codec.BinaryCodec, clientStore storetypes.KVStore, clientMsg exported.ClientMessage) {
	panic("fabric-msp: misbehaviour handling not implemented (out of scope, see nextsteps.md)")
}

// UpdateState só é alcançado depois de VerifyClientMessage aceitar (ibc-go
// core só chama UpdateState após Verify sem erro) - aplica a atualização
// já verificada: novo LastChaincodeHeader (se a sequência avançou,
// persistindo um novo ConsensusState na nova altura + metadata de delay
// period) e/ou LastChaincodeInfo/LastMspInfos.
func (cs *ClientState) UpdateState(ctx sdk.Context, cdc codec.BinaryCodec, clientStore storetypes.KVStore, clientMsg exported.ClientMessage) []exported.Height {
	header, ok := clientMsg.(*Header)
	if !ok {
		panic(fmt.Sprintf("fabric-msp: unexpected client message type %T", clientMsg))
	}

	updated := *cs
	if header.ChaincodeInfo != nil {
		updated.LastChaincodeInfo = *header.ChaincodeInfo
	}
	if header.MspHeaders != nil {
		updated.LastMspInfos = applyMSPHeaders(updated.LastMspInfos, header.MspHeaders.Headers)
	}

	if header.ChaincodeHeader == nil {
		setClientState(clientStore, cdc, &updated)
		*cs = updated
		return []exported.Height{cs.GetLatestHeight()}
	}

	updated.LastChaincodeHeader = *header.ChaincodeHeader
	newHeight := clienttypes.NewHeight(0, updated.LastChaincodeHeader.Sequence.Value)

	setClientState(clientStore, cdc, &updated)
	setConsensusState(clientStore, cdc, NewConsensusState(updated.LastChaincodeHeader.Sequence.Timestamp), newHeight)
	setConsensusMetadata(ctx, clientStore, newHeight)

	*cs = updated
	return []exported.Height{newHeight}
}
