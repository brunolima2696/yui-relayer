package fabric_msp

// Casca que o ibc-go v8.2.1 exige pra um novo tipo de light
// client aparecer no module manager do simd - mesmo formato mínimo de
// bcs/cosmos/app/lightclients/besu-qbft/module.go (só RegisterInterfaces
// importa; o resto é no-op).

import (
	"encoding/json"

	"github.com/grpc-ecosystem/grpc-gateway/runtime"
	"github.com/spf13/cobra"

	"cosmossdk.io/core/appmodule"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdkmodule "github.com/cosmos/cosmos-sdk/types/module"
)

// ModuleName é o mesmo client type que o light client usa, "fabric-msp"
// (mesma convenção de 07-tendermint/hb-qbft: nome do módulo = client type).
const ModuleName = ClientType

var (
	_ sdkmodule.AppModuleBasic = (*AppModuleBasic)(nil)
	_ appmodule.AppModule      = (*AppModule)(nil)
)

// AppModuleBasic é o módulo básico do light client fabric-msp. Só
// RegisterInterfaces faz algo de verdade
type AppModuleBasic struct{}

func (AppModuleBasic) Name() string {
	return ModuleName
}

func (AppModule) IsOnePerModuleType() {}
func (AppModule) IsAppModule()        {}

func (AppModuleBasic) RegisterLegacyAminoCodec(*codec.LegacyAmino) {}

// RegisterInterfaces registra ClientState/ConsensusState/Header
// (implementando exported.ClientState/ConsensusState/ClientMessage) no
// InterfaceRegistry do simd, pra permitir empacotamento em Any.
func (AppModuleBasic) RegisterInterfaces(registry codectypes.InterfaceRegistry) {
	RegisterInterfaces(registry)
}

func (AppModuleBasic) DefaultGenesis(cdc codec.JSONCodec) json.RawMessage {
	return nil
}

func (AppModuleBasic) ValidateGenesis(cdc codec.JSONCodec, config client.TxEncodingConfig, bz json.RawMessage) error {
	return nil
}

func (AppModuleBasic) RegisterGRPCGatewayRoutes(clientCtx client.Context, mux *runtime.ServeMux) {}

func (AppModuleBasic) GetTxCmd() *cobra.Command {
	return nil
}

func (AppModuleBasic) GetQueryCmd() *cobra.Command {
	return nil
}

// AppModule é o módulo do light client fabric-msp.
type AppModule struct {
	AppModuleBasic
}

// NewAppModule cria um novo AppModule do light client fabric-msp, nos
// moldes de ibctm.NewAppModule()/qbftlightclient.NewAppModule().
func NewAppModule() AppModule {
	return AppModule{}
}
