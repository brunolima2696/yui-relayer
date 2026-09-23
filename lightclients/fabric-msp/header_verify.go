// Verificação por tipo de update de Header, portado de
// VerifyChaincodeInfo/VerifyMSPHeader/verifyMSPCreate/
// verifyMSPUpdatePolicy/verifyMSPUpdateConfig/verifyMSPFreeze do fork
// antigo (types/fabric.go). ChaincodeHeader (avanço de sequência) fica
// fora daqui — depende de VerifyEndorsedCommitment, não de
// VerifyEndorsedMessage.
package fabric_msp

import (
	"fmt"

	gogoproto "github.com/cosmos/gogoproto/proto"
)

// SequenceCommitmentKey é a chave fixa e bem-conhecida onde o chaincode
// deve commitar o Sequence a cada avanço
const SequenceCommitmentKey = "fabric-msp/sequence"

// verifyChaincodeHeader confere que o novo Sequence foi legitimamente
// commitado porta VerifyChaincodeHeader
// do fork antigo, usando VerifyEndorsedCommitment
func (cs *ClientState) verifyChaincodeHeader(ch ChaincodeHeader) error {
	if cs.GetLatestHeight().GetRevisionHeight() >= ch.Sequence.Value {
		return fmt.Errorf("fabric-msp: new sequence %d must be greater than current %d", ch.Sequence.Value, cs.GetLatestHeight().GetRevisionHeight())
	}
	configs, err := cs.LastMspInfos.GetMSPPBConfigs()
	if err != nil {
		return err
	}
	seqBytes, err := gogoproto.Marshal(&ch.Sequence)
	if err != nil {
		return err
	}
	ok, err := VerifyEndorsedCommitment(cs.LastChaincodeInfo.GetFabricChaincodeID(), cs.LastChaincodeInfo.EndorsementPolicy, ch.Proof, SequenceCommitmentKey, seqBytes, configs)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("fabric-msp: sequence commitment proof failed: value mismatch")
	}
	return nil
}

func (cs *ClientState) verifyChaincodeInfo(info ChaincodeInfo) error {
	if info.Proof == nil {
		return fmt.Errorf("fabric-msp: ChaincodeInfo proof is empty")
	}
	configs, err := cs.LastMspInfos.GetMSPPBConfigs()
	if err != nil {
		return err
	}
	return VerifyEndorsedMessage(cs.LastChaincodeInfo.IbcPolicy, *info.Proof, chaincodeInfoSignBytes(info), configs)
}

func (cs *ClientState) verifyMSPHeader(mh MSPHeader) error {
	switch mh.Type {
	case MSPHeaderType_MSP_HEADER_TYPE_CREATE:
		return cs.verifyMSPCreate(mh)
	case MSPHeaderType_MSP_HEADER_TYPE_UPDATE_POLICY:
		return cs.verifyMSPUpdatePolicy(mh)
	case MSPHeaderType_MSP_HEADER_TYPE_UPDATE_CONFIG:
		return cs.verifyMSPUpdateConfig(mh)
	case MSPHeaderType_MSP_HEADER_TYPE_FREEZE:
		return cs.verifyMSPFreeze(mh)
	default:
		return fmt.Errorf("fabric-msp: unknown MSPHeaderType %v", mh.Type)
	}
}

func (cs *ClientState) verifyMSPCreate(mh MSPHeader) error {
	if mh.Proof == nil {
		return fmt.Errorf("fabric-msp: MSPHeader proof is empty")
	}
	if cs.LastMspInfos.HasMSPID(mh.MspId) {
		return fmt.Errorf("fabric-msp: MSPInfo %q already created", mh.MspId)
	}
	configs, err := cs.LastMspInfos.GetMSPPBConfigs()
	if err != nil {
		return err
	}
	return VerifyEndorsedMessage(cs.LastChaincodeInfo.IbcPolicy, *mh.Proof, mspHeaderSignBytes(mh), configs)
}

func (cs *ClientState) verifyMSPUpdatePolicy(mh MSPHeader) error {
	if mh.Proof == nil {
		return fmt.Errorf("fabric-msp: MSPHeader proof is empty")
	}
	mi, err := cs.LastMspInfos.FindMSPInfo(mh.MspId)
	if err != nil {
		return err
	}
	if mi.Freezed {
		return fmt.Errorf("fabric-msp: MSPInfo %q is freezed", mh.MspId)
	}
	configs, err := cs.LastMspInfos.GetMSPPBConfigs()
	if err != nil {
		return err
	}
	return VerifyEndorsedMessage(cs.LastChaincodeInfo.IbcPolicy, *mh.Proof, mspHeaderSignBytes(mh), configs)
}

func (cs *ClientState) verifyMSPUpdateConfig(mh MSPHeader) error {
	if mh.Proof == nil {
		return fmt.Errorf("fabric-msp: MSPHeader proof is empty")
	}
	mi, err := cs.LastMspInfos.FindMSPInfo(mh.MspId)
	if err != nil {
		return err
	}
	if mi.Freezed {
		return fmt.Errorf("fabric-msp: MSPInfo %q is freezed", mh.MspId)
	}
	configs, err := cs.LastMspInfos.GetMSPPBConfigs()
	if err != nil {
		return err
	}

	return VerifyEndorsedMessage(mi.Policy, *mh.Proof, mspHeaderSignBytes(mh), configs)
}

func (cs *ClientState) verifyMSPFreeze(mh MSPHeader) error {
	if mh.Proof == nil {
		return fmt.Errorf("fabric-msp: MSPHeader proof is empty")
	}
	mi, err := cs.LastMspInfos.FindMSPInfo(mh.MspId)
	if err != nil {
		return err
	}
	if mi.Freezed {
		return fmt.Errorf("fabric-msp: MSPInfo %q is freezed", mh.MspId)
	}
	configs, err := cs.LastMspInfos.GetMSPPBConfigs()
	if err != nil {
		return err
	}
	return VerifyEndorsedMessage(cs.LastChaincodeInfo.IbcPolicy, *mh.Proof, mspHeaderSignBytes(mh), configs)
}

// chaincodeInfoSignBytes/mspHeaderSignBytes reproduzem GetSignBytes() do
// fork antigo: serializa a mensagem com o campo Proof zerado
func chaincodeInfoSignBytes(info ChaincodeInfo) []byte {
	info.Proof = nil
	bz, err := gogoproto.Marshal(&info)
	if err != nil {
		panic(err)
	}
	return bz
}

func mspHeaderSignBytes(mh MSPHeader) []byte {
	mh.Proof = nil
	bz, err := gogoproto.Marshal(&mh)
	if err != nil {
		panic(err)
	}
	return bz
}

// applyMSPHeaders aplica os updates de MSP (Create/UpdatePolicy/
// UpdateConfig/Freeze) já verificados sobre uma cópia de MSPInfos.
func applyMSPHeaders(infos MSPInfos, headers []MSPHeader) MSPInfos {
	for _, mh := range headers {
		switch mh.Type {
		case MSPHeaderType_MSP_HEADER_TYPE_CREATE:
			infos.Infos = append(infos.Infos, MSPInfo{MspId: mh.MspId, Config: mh.Config, Policy: mh.Policy})
		case MSPHeaderType_MSP_HEADER_TYPE_UPDATE_POLICY:
			if idx := infos.IndexOf(mh.MspId); idx >= 0 {
				infos.Infos[idx].Policy = mh.Policy
			}
		case MSPHeaderType_MSP_HEADER_TYPE_UPDATE_CONFIG:
			if idx := infos.IndexOf(mh.MspId); idx >= 0 {
				infos.Infos[idx].Config = mh.Config
			}
		case MSPHeaderType_MSP_HEADER_TYPE_FREEZE:
			if idx := infos.IndexOf(mh.MspId); idx >= 0 {
				infos.Infos[idx].Freezed = true
			}
		}
	}
	return infos
}
