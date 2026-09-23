package fabric_msp

import (
	"fmt"
	"time"

	"github.com/cosmos/ibc-go/v8/modules/core/exported"
)

var _ exported.ConsensusState = (*ConsensusState)(nil)

// NewConsensusState cria um novo ConsensusState fabric-msp. Não existe
// Root aqui só o timestamp em que
// o ChaincodeHeader correspondente foi endossado.
func NewConsensusState(timestamp int64) *ConsensusState {
	return &ConsensusState{Timestamp: timestamp}
}

func (cs *ConsensusState) ClientType() string {
	return ClientType
}

func (cs *ConsensusState) GetTimestamp() uint64 {
	return uint64(time.Unix(cs.Timestamp, 0).UnixNano())
}

func (cs *ConsensusState) ValidateBasic() error {
	if cs.Timestamp == 0 {
		return fmt.Errorf("timestamp cannot be zero")
	}
	return nil
}
