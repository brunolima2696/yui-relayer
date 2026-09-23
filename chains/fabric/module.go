package fabric

import (
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/hyperledger-labs/yui-relayer/config"
	"github.com/spf13/cobra"
)

// Module é o plugin de verdade do chain Fabric pro yui-relayer
// config.ModuleI, mesmo padrão dos módulos tendermint/ethereum já
// usados em relayer/main.go
type Module struct{}

var _ config.ModuleI = (*Module)(nil)

const ModuleName = "fabric.chain"

func (Module) Name() string {
	return ModuleName
}

func (Module) RegisterInterfaces(registry codectypes.InterfaceRegistry) {
	RegisterInterfaces(registry)
}

// GetCmd: sem subcomandos próprios por enquanto, toda a
// interação de handshake/relay já passa pelos comandos genéricos do
// core (`yrly tx clients`/`tx connection`/`tx channel`), que não
// dependem de nada específico do módulo Fabric.
func (Module) GetCmd(ctx *config.Context) *cobra.Command {
	return &cobra.Command{
		Use:   "fabric",
		Short: "manage fabric configuration",
	}
}
