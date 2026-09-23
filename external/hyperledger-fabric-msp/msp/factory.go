/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package msp

import (
	"github.com/hyperledger/fabric-lib-go/bccsp"
	"github.com/pkg/errors"
)

type MSPVersion int

const (
	MSPv1_0 = iota
	MSPv1_1
	MSPv1_3
	MSPv1_4_3
	MSPv3_0
)

// NewOpts represent
type NewOpts interface {
	// GetVersion returns the MSP's version to be instantiated
	GetVersion() MSPVersion
}

// NewBaseOpts is the default base type for all MSP instantiation Opts
type NewBaseOpts struct {
	Version MSPVersion
}

func (o *NewBaseOpts) GetVersion() MSPVersion {
	return o.Version
}

// BCCSPNewOpts contains the options to instantiate a new BCCSP-based (X509) MSP
type BCCSPNewOpts struct {
	NewBaseOpts
}

// New create a new MSP instance depending on the passed Opts
//
// NOTA (patch local, ver YUI_Relayer/.claude/nextsteps.md T28): suporte a
// Idemix (IdemixNewOpts, msp/idemix.go) removido deste vendor. O light
// client fabric-msp só usa MSPs tipo FABRIC/X.509 (o único usado por
// EndorsementPolicy/MSPInfo neste projeto) - manter o caminho Idemix
// puxaria github.com/IBM/idemix -> github.com/IBM/mathlib ->
// consensys/gnark-crypto numa versão incompatível com
// github.com/crate-crypto/go-kzg-4844 (já usado pelo lado Besu/Ethereum
// deste mesmo app), quebrando o build sem nenhum ganho real (Idemix nunca
// é exercitado aqui).
func New(opts NewOpts, cryptoProvider bccsp.BCCSP) (MSP, error) {
	switch opts.(type) {
	case *BCCSPNewOpts:
		switch opts.GetVersion() {
		case MSPv1_0, MSPv1_1, MSPv1_3, MSPv1_4_3, MSPv3_0:
			return newBccspMsp(opts.GetVersion(), cryptoProvider)
		default:
			return nil, errors.Errorf("Invalid *BCCSPNewOpts. Version not recognized [%v]", opts.GetVersion())
		}
	default:
		return nil, errors.Errorf("Invalid msp.NewOpts instance. It must be *BCCSPNewOpts (Idemix support removed from this vendor). It was [%v]", opts)
	}
}
