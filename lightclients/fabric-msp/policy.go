// é o pacote de política do light client fabric-msp.
package fabric_msp

import (
	"fmt"

	"github.com/hyperledger/fabric-lib-go/bccsp/factory"
	fabricpeer "github.com/hyperledger/fabric-protos-go-apiv2/peer"
	cauthdsl "github.com/hyperledger/fabric/common/cauthdsl"
	"github.com/hyperledger/fabric/common/policies"
	fabricmsp "github.com/hyperledger/fabric/msp"
	"github.com/hyperledger/fabric/protoutil"
	"google.golang.org/protobuf/proto"
)

// getPolicyEvaluator desserializa um peer.ApplicationPolicy (variante
// SignaturePolicy) e devolve um avaliador de política real, com
// LastMspInfos como raiz de confiança.
func getPolicyEvaluator(policyBytes []byte, configs []*MSPPBConfig) (policies.Policy, error) {
	var ap fabricpeer.ApplicationPolicy
	if err := proto.Unmarshal(policyBytes, &ap); err != nil {
		return nil, err
	}
	sigPolicy := ap.GetSignaturePolicy()
	if sigPolicy == nil {
		return nil, fmt.Errorf("fabric-msp: policy não é uma SignaturePolicy")
	}

	mgr, err := setupVerifyingMSPManager(configs)
	if err != nil {
		return nil, err
	}
	pp := cauthdsl.EnvelopeBasedPolicyProvider{Deserializer: mgr}
	return pp.NewPolicy(sigPolicy)
}

// setupVerifyingMSPManager monta um MSPManager real a partir dos
// MSPConfig rastreados em LastMspInfos
func setupVerifyingMSPManager(mspConfigs []*MSPPBConfig) (fabricmsp.MSPManager, error) {
	var msps []fabricmsp.MSP
	for _, conf := range mspConfigs {
		m, err := setupVerifyingMSP(conf)
		if err != nil {
			return nil, err
		}
		msps = append(msps, m)
	}
	mgr := fabricmsp.NewMSPManager()
	if err := mgr.Setup(msps); err != nil {
		return nil, err
	}
	return mgr, nil
}

func setupVerifyingMSP(mspConf *MSPPBConfig) (fabricmsp.MSP, error) {
	bccspConfig := factory.GetDefaultOpts()
	cryptoProvider, err := (&factory.SWFactory{}).Get(bccspConfig)
	if err != nil {
		return nil, err
	}
	opts := fabricmsp.Options[fabricmsp.ProviderTypeToString(fabricmsp.FABRIC)]
	m, err := fabricmsp.New(opts, cryptoProvider)
	if err != nil {
		return nil, err
	}
	if err := m.Setup(mspConf); err != nil {
		return nil, err
	}
	return m, nil
}

// VerifyEndorsedMessage verifica um valor assinado (MessageProof) contra
// uma política de endosso — usado para autenticar ChaincodeInfo/MSPHeader
func VerifyEndorsedMessage(policyBytes []byte, proof MessageProof, value []byte, configs []*MSPPBConfig) error {
	policy, err := getPolicyEvaluator(policyBytes, configs)
	if err != nil {
		return err
	}
	sigs := makeSignedDataListWithMessageProof(proof, value)
	return policy.EvaluateSignedData(sigs)
}

func makeSignedDataListWithMessageProof(proof MessageProof, value []byte) []*protoutil.SignedData {
	var sigSet []*protoutil.SignedData
	for i := 0; i < len(proof.Signatures); i++ {
		sigSet = append(sigSet, &protoutil.SignedData{
			Data:      value,
			Identity:  proof.Identities[i],
			Signature: proof.Signatures[i],
		})
	}
	return sigSet
}
