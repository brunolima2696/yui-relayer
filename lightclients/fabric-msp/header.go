package fabric_msp

import (
	"errors"

	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	"github.com/cosmos/ibc-go/v8/modules/core/exported"
	fabricpeer "github.com/hyperledger/fabric-protos-go-apiv2/peer"
)

var _ exported.ClientMessage = (*Header)(nil)

// NewHeader cria um novo Header fabric-msp. Ao menos um dos três campos
// deve ser não-nulo, um Header aqui é um pacote de
// atualização, não um header de bloco tradicional.
func NewHeader(chaincodeHeader *ChaincodeHeader, chaincodeInfo *ChaincodeInfo, mspHeaders *MSPHeaders) *Header {
	return &Header{ChaincodeHeader: chaincodeHeader, ChaincodeInfo: chaincodeInfo, MspHeaders: mspHeaders}
}

func (h *Header) ClientType() string {
	return ClientType
}

// GetHeight só faz sentido quando o Header traz um ChaincodeHeader (avanço
// de sequência), atualizações só de ChaincodeInfo/MSPHeaders não avançam
// a altura do client.
func (h *Header) GetHeight() exported.Height {
	if h.ChaincodeHeader == nil {
		return clienttypes.ZeroHeight()
	}
	return clienttypes.NewHeight(0, h.ChaincodeHeader.Sequence.Value)
}

func (h *Header) ValidateBasic() error {
	if h.ChaincodeHeader == nil && h.ChaincodeInfo == nil && h.MspHeaders == nil {
		return errors.New("either ChaincodeHeader, ChaincodeInfo or MSPHeaders must be non-nil")
	}
	return nil
}

// NewChaincodeHeader cria um ChaincodeHeader com sequência/timestamp dados
// e prova vazia.
func NewChaincodeHeader(seq uint64, timestamp int64) ChaincodeHeader {
	return ChaincodeHeader{Sequence: Sequence{Value: seq, Timestamp: timestamp}}
}

// NewChaincodeID cria um ChaincodeID (path/name/version), no mesmo formato
// do peer.ChaincodeID nativo do Fabric.
func NewChaincodeID(path, name, version string) ChaincodeID {
	return ChaincodeID{Path: path, Name: name, Version: version}
}

// NewChaincodeInfo cria um ChaincodeInfo com a política de endosso/IBC
// vigente
func NewChaincodeInfo(channelID string, ccID ChaincodeID, endorsementPolicy, ibcPolicy []byte) ChaincodeInfo {
	return ChaincodeInfo{
		ChannelId:         channelID,
		ChaincodeId:       ccID,
		EndorsementPolicy: endorsementPolicy,
		IbcPolicy:         ibcPolicy,
	}
}

// GetFabricChaincodeID converte o ChaincodeID pro
// peer.ChaincodeID nativo do Fabric, usado por VerifyEndorsedCommitment
// pra conferir que o commitment veio do chaincode certo.
func (ci ChaincodeInfo) GetFabricChaincodeID() *fabricpeer.ChaincodeID {
	return &fabricpeer.ChaincodeID{
		Path:    ci.ChaincodeId.Path,
		Name:    ci.ChaincodeId.Name,
		Version: ci.ChaincodeId.Version,
	}
}
