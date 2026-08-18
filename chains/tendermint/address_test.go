package tendermint

import (
	"bytes"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/bech32"
)

func TestEncodeAddressUsesProvidedPrefix(t *testing.T) {
	address := sdk.AccAddress(bytes.Repeat([]byte{0x42}, 20))

	ethmAddress, err := encodeAddress("ethm", address)
	if err != nil {
		t.Fatal(err)
	}
	cosmosAddress, err := encodeAddress("cosmos", address)
	if err != nil {
		t.Fatal(err)
	}

	ethmPrefix, ethmData, err := bech32.DecodeAndConvert(ethmAddress)
	if err != nil {
		t.Fatal(err)
	}
	cosmosPrefix, cosmosData, err := bech32.DecodeAndConvert(cosmosAddress)
	if err != nil {
		t.Fatal(err)
	}

	if ethmPrefix != "ethm" || cosmosPrefix != "cosmos" {
		t.Fatalf("unexpected prefixes: %s, %s", ethmPrefix, cosmosPrefix)
	}
	if !bytes.Equal(ethmData, address) || !bytes.Equal(cosmosData, address) {
		t.Fatalf("address payload changed with prefix")
	}
}
