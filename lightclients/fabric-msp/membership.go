// VerifyMembership/VerifyNonMembership. Diferente de
// hb-qbft/07-tendermint, não existe um Root único por altura contra o
// qual provar um caminho Merkle genérico: cada valor é verificado por
// uma prova de endosso ad-hoc contra a política de endosso vigente do
// chaincode (LastChaincodeInfo).
//
// Non-membership: como um write-set prova o que foi escrito, não a
// ausência estrutural, a convenção (herdada do fork antigo) é o
// chaincode escrever um valor-sentinela []byte{0} na chave quando quer
// provar ausência, VerifyNonMembership confere que o commitment
// endossado naquela chave é exatamente esse sentinela.
package fabric_msp

import (
	"encoding/hex"
	"fmt"

	storetypes "cosmossdk.io/store/types"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	commitmenttypes "github.com/cosmos/ibc-go/v8/modules/core/23-commitment/types"
	"github.com/cosmos/ibc-go/v8/modules/core/exported"

	gogoproto "github.com/cosmos/gogoproto/proto"
)

// nonMembershipSentinel é o valor que o chaincode deve commitar numa
// chave pra provar ausência
var nonMembershipSentinel = []byte{0}

func (cs *ClientState) VerifyMembership(ctx sdk.Context, clientStore storetypes.KVStore, cdc codec.BinaryCodec, height exported.Height, delayTimePeriod uint64, delayBlockPeriod uint64, proofBz []byte, path exported.Path, value []byte) error {
	ok, err := cs.verifyCommitmentAtPath(ctx, clientStore, height, delayTimePeriod, delayBlockPeriod, proofBz, path, value)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("fabric-msp: membership proof failed: value mismatch")
	}
	return nil
}

func (cs *ClientState) VerifyNonMembership(ctx sdk.Context, clientStore storetypes.KVStore, cdc codec.BinaryCodec, height exported.Height, delayTimePeriod uint64, delayBlockPeriod uint64, proofBz []byte, path exported.Path) error {
	ok, err := cs.verifyCommitmentAtPath(ctx, clientStore, height, delayTimePeriod, delayBlockPeriod, proofBz, path, nonMembershipSentinel)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("fabric-msp: non-membership proof failed: key was found with a different value")
	}
	return nil
}

func (cs *ClientState) verifyCommitmentAtPath(ctx sdk.Context, clientStore storetypes.KVStore, height exported.Height, delayTimePeriod, delayBlockPeriod uint64, proofBz []byte, path exported.Path, expectedValue []byte) (bool, error) {
	if cs.GetLatestHeight().LT(height) {
		return false, fmt.Errorf("fabric-msp: client state height < proof height (%s < %s)", cs.GetLatestHeight(), height)
	}
	if err := verifyDelayPeriodPassed(ctx, clientStore, height, delayTimePeriod, delayBlockPeriod); err != nil {
		return false, err
	}

	var proof CommitmentProof
	if err := gogoproto.Unmarshal(proofBz, &proof); err != nil {
		return false, fmt.Errorf("fabric-msp: failed to unmarshal commitment proof: %w", err)
	}

	icsPath, err := ics24Path(path)
	if err != nil {
		return false, err
	}
	key := fabricLedgerKey(icsPath)

	configs, err := cs.LastMspInfos.GetMSPPBConfigs()
	if err != nil {
		return false, err
	}

	return VerifyEndorsedCommitment(cs.LastChaincodeInfo.GetFabricChaincodeID(), cs.LastChaincodeInfo.EndorsementPolicy, proof, string(key), expectedValue, configs)
}

// ics24Path extrai o path ICS-24 puro
func ics24Path(path exported.Path) ([]byte, error) {
	merklePath, ok := path.(commitmenttypes.MerklePath)
	if !ok {
		return nil, fmt.Errorf("fabric-msp: expected %T, got %T", commitmenttypes.MerklePath{}, path)
	}
	if len(merklePath.KeyPath) == 0 {
		return nil, fmt.Errorf("fabric-msp: empty merkle path")
	}
	return []byte(merklePath.KeyPath[len(merklePath.KeyPath)-1]), nil
}

// fabricStoreKeyIBC/fabricLedgerKey replicam, deste lado do par
// Cosmos<->Fabric, a mesma transformação de chave que
// bcs/fabric/IC_Create_Network/chaincode/cc_ibc/internal/fabricstore
// aplica antes de tocar o WorldState real do Fabric: prefixo
// "s/k:<nome-do-store>/" (dbm.NewPrefixDB) seguido de hex-encoding
// (FabricDB - Fabric exige chaves imprimíveis/UTF-8 pra
// GetStateByRange funcionar em CouchDB; nem toda chave interna do
// ibc-go é isso, ex. alturas em big-endian). sem
// essa transformação aqui, VerifyMembership nunca bateria contra o
// write-set real do cc_ibc, porque a chave "crua" nunca
// é o que de fato fica gravado no ledger do Fabric. Os dois lados são módulos Go diferentes
// qualquer mudança de codificação de um lado precisa ser replicada no
// outro, documentado nos dois arquivos.
const fabricStoreKeyIBC = "ibc"

func fabricLedgerKey(icsPath []byte) []byte {
	prefixed := append([]byte("s/k:"+fabricStoreKeyIBC+"/"), icsPath...)
	encoded := make([]byte, hex.EncodedLen(len(prefixed)))
	hex.Encode(encoded, prefixed)
	return encoded
}
