package fabric_msp

import (
	"errors"

	msppb "github.com/hyperledger/fabric-protos-go-apiv2/msp"
	"google.golang.org/protobuf/proto"
)

// MSPPBConfig é o MSPConfig nativo do Fabric que MSPInfo.Config guarda serializado. Precisa ser o mesmo
// pacote de protos que o `msp` vendorizado usa internamente
type MSPPBConfig = msppb.MSPConfig

// GetMSPPBConfigs decodifica os MSPConfig nativos de cada MSPInfo não
// congelada. É decodificação de dado, não verificação — a verificação de
// assinatura contra esses configs
func (mi MSPInfos) GetMSPPBConfigs() ([]*MSPPBConfig, error) {
	var configs []*MSPPBConfig
	for _, info := range mi.Infos {
		if info.Freezed {
			continue
		}
		if info.Config == nil {
			return nil, errors.New("a MSPInfo has no config")
		}
		cfg := &MSPPBConfig{}
		if err := proto.Unmarshal(info.Config, cfg); err != nil {
			return nil, err
		}
		configs = append(configs, cfg)
	}
	return configs, nil
}

// HasMSPID retorna true se já existe uma MSPInfo com esse MSPID.
func (mi MSPInfos) HasMSPID(mspID string) bool {
	return mi.IndexOf(mspID) != -1
}

// IndexOf retorna o índice da MSPInfo com esse MSPID, ou -1.
func (mi MSPInfos) IndexOf(mspID string) int {
	for i, info := range mi.Infos {
		if info.MspId == mspID {
			return i
		}
	}
	return -1
}

// FindMSPInfo retorna a MSPInfo com esse MSPID, ou erro se não existir.
func (mi MSPInfos) FindMSPInfo(mspID string) (*MSPInfo, error) {
	idx := mi.IndexOf(mspID)
	if idx < 0 {
		return nil, errors.New("MSPInfo not found")
	}
	return &mi.Infos[idx], nil
}

// NewMSPInfo cria uma MSPInfo (raiz de confiança de uma organização).
func NewMSPInfo(mspID string, config, policy []byte) MSPInfo {
	return MSPInfo{MspId: mspID, Config: config, Policy: policy}
}

// NewMSPInfos agrupa uma lista de MSPInfo.
func NewMSPInfos(infos []MSPInfo) MSPInfos {
	return MSPInfos{Infos: infos}
}
