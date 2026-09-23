package fabric

import "github.com/hyperledger-labs/yui-relayer/core"

var _ core.MsgID = (*MsgID)(nil)

// Is_MsgID satisfaz core.MsgID (marker method, mesmo padrão do módulo
// tendermint/ethereum).
func (*MsgID) Is_MsgID() {}

func NewMsgID(txID string) *MsgID {
	return &MsgID{TxId: txID}
}
