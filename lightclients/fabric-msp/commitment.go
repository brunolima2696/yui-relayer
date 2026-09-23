// Verificação de commitment endossado, portada de
// types/fabric.go/types/commitment.go do fork antigo, o coração de
// VerifyMembership/VerifyNonMembership e do avanço de sequência
// (ChaincodeHeader). Diferente de VerifyEndorsedMessage,, aqui a prova é sobre uma entrada específica do
// read-write set de uma transação endossada: prova que a chave `key`
// foi escrita com o valor `value` numa transação cujo
// ProposalResponsePayload foi assinado por identidades que satisfazem
// a política de endosso.
package fabric_msp

import (
	"bytes"
	"fmt"

	fabricpeer "github.com/hyperledger/fabric-protos-go-apiv2/peer"
	"github.com/hyperledger/fabric/core/ledger/kvledger/txmgmt/rwsetutil"
	"github.com/hyperledger/fabric/protoutil"
	"google.golang.org/protobuf/proto"
)

// ToSignedData constrói os dados assinados de um CommitmentProof: ao
// contrário de MessageProof (assina só o valor), aqui a assinatura é
// sobre Proposal+Identity concatenados (é assim que um endosso real do
// Fabric assina um ProposalResponsePayload).
func (proof CommitmentProof) ToSignedData() []*protoutil.SignedData {
	var sigSet []*protoutil.SignedData
	for i := 0; i < len(proof.Signatures); i++ {
		msg := make([]byte, len(proof.Proposal)+len(proof.Identities[i]))
		copy(msg[:len(proof.Proposal)], proof.Proposal)
		copy(msg[len(proof.Proposal):], proof.Identities[i])
		sigSet = append(sigSet, &protoutil.SignedData{
			Data:      msg,
			Identity:  proof.Identities[i],
			Signature: proof.Signatures[i],
		})
	}
	return sigSet
}

// VerifyEndorsedCommitment verifica que (key, value) está de verdade no
// read-write set endossado carregado em proof.Proposal, assinado por
// identidades que satisfazem policyBytes.
func VerifyEndorsedCommitment(ccID *fabricpeer.ChaincodeID, policyBytes []byte, proof CommitmentProof, key string, value []byte, configs []*MSPPBConfig) (bool, error) {
	policy, err := getPolicyEvaluator(policyBytes, configs)
	if err != nil {
		return false, err
	}
	if err := policy.EvaluateSignedData(proof.ToSignedData()); err != nil {
		return false, err
	}

	id, rwset, err := getTxReadWriteSetFromProposalResponsePayload(proof.Proposal)
	if err != nil {
		return false, err
	}
	if !equalChaincodeID(ccID, id) {
		return false, fmt.Errorf("fabric-msp: unexpected chaincodeID: expected=%v actual=%v", ccID, id)
	}
	return ensureWriteSetIncludesCommitment(rwset.NsRwSets, proof.NsIndex, proof.WriteSetIndex, key, value)
}

func getTxReadWriteSetFromProposalResponsePayload(proposal []byte) (*fabricpeer.ChaincodeID, *rwsetutil.TxRwSet, error) {
	var payload fabricpeer.ProposalResponsePayload
	if err := proto.Unmarshal(proposal, &payload); err != nil {
		return nil, nil, err
	}
	var cact fabricpeer.ChaincodeAction
	if err := proto.Unmarshal(payload.Extension, &cact); err != nil {
		return nil, nil, err
	}
	txRWSet := &rwsetutil.TxRwSet{}
	if err := txRWSet.FromProtoBytes(cact.Results); err != nil {
		return nil, nil, err
	}
	return cact.GetChaincodeId(), txRWSet, nil
}

func ensureWriteSetIncludesCommitment(nsRwSets []*rwsetutil.NsRwSet, nsIdx, rwsIdx uint32, targetKey string, expectValue []byte) (bool, error) {
	if int(nsIdx) >= len(nsRwSets) {
		return false, fmt.Errorf("fabric-msp: ns index %d out of range (len=%d)", nsIdx, len(nsRwSets))
	}
	rws := nsRwSets[nsIdx].KvRwSet
	if rws == nil || len(rws.Writes) <= int(rwsIdx) {
		return false, fmt.Errorf("fabric-msp: write index %d not found (len=%d)", rwsIdx, len(rws.GetWrites()))
	}

	w := rws.Writes[rwsIdx]
	if targetKey != w.Key {
		return false, fmt.Errorf("fabric-msp: unexpected key %q (expected %q)", w.Key, targetKey)
	}
	return bytes.Equal(w.Value, expectValue), nil
}

func equalChaincodeID(a, b *fabricpeer.ChaincodeID) bool {
	return a.GetName() == b.GetName() && a.GetPath() == b.GetPath() && a.GetVersion() == b.GetVersion()
}
