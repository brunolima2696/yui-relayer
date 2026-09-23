package fabric

import (
	"fmt"
	"strings"

	"github.com/hyperledger-labs/yui-relayer/core"
)

var (
	_ core.ChainConfig  = (*ChainConfig)(nil)
	_ core.ProverConfig = (*ProverConfig)(nil)
)

func isEmpty(s string) bool { return strings.TrimSpace(s) == "" }

func (c ChainConfig) Build() (core.Chain, error) {
	return &Chain{config: c}, nil
}

func (c ChainConfig) Validate() error {
	var errs []error
	req := func(name, v string) {
		if isEmpty(v) {
			errs = append(errs, fmt.Errorf("config attribute %q is empty", name))
		}
	}
	req("chain_id", c.ChainId)
	req("channel_id", c.ChannelId)
	req("chaincode_name", c.ChaincodeName)
	req("gateway_endpoint", c.GatewayEndpoint)
	req("msp_id", c.MspId)
	req("cert_path", c.CertPath)
	req("key_path", c.KeyPath)
	if len(c.MspInfos) == 0 {
		errs = append(errs, fmt.Errorf("config attribute \"msp_infos\" is empty"))
	}
	if c.ChaincodeInfo == nil {
		errs = append(errs, fmt.Errorf("config attribute \"chaincode_info\" is required"))
	}
	if c.AverageBlockTimeMsec == 0 {
		errs = append(errs, fmt.Errorf("config attribute \"average_block_time_msec\" is zero"))
	}
	return joinErrors(errs)
}

func (c ProverConfig) Build(chain core.Chain) (core.Prover, error) {
	fabChain, ok := chain.(*Chain)
	if !ok {
		return nil, fmt.Errorf("fabric: ProverConfig.Build expects a *fabric.Chain, got %T", chain)
	}
	return NewProver(fabChain, c), nil
}

func (c ProverConfig) Validate() error {
	if c.TrustingPeriodSec <= 0 {
		return fmt.Errorf("config attribute \"trusting_period_sec\" must be positive")
	}
	if c.MaxClockDriftSec <= 0 {
		return fmt.Errorf("config attribute \"max_clock_drift_sec\" must be positive")
	}
	return nil
}

func joinErrors(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	return fmt.Errorf("%s", strings.Join(msgs, "; "))
}
