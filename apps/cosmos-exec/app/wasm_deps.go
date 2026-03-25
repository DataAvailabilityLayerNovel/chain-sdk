package app

import (
	"context"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	distrtypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	upgradetypes "github.com/cosmos/cosmos-sdk/x/upgrade/types"
)

type noopDistributionKeeper struct{}

func (noopDistributionKeeper) DelegatorWithdrawAddress(_ context.Context, _ *distrtypes.QueryDelegatorWithdrawAddressRequest) (*distrtypes.QueryDelegatorWithdrawAddressResponse, error) {
	return &distrtypes.QueryDelegatorWithdrawAddressResponse{}, nil
}

func (noopDistributionKeeper) DelegationRewards(_ context.Context, _ *distrtypes.QueryDelegationRewardsRequest) (*distrtypes.QueryDelegationRewardsResponse, error) {
	return &distrtypes.QueryDelegationRewardsResponse{}, nil
}

func (noopDistributionKeeper) DelegationTotalRewards(_ context.Context, _ *distrtypes.QueryDelegationTotalRewardsRequest) (*distrtypes.QueryDelegationTotalRewardsResponse, error) {
	return &distrtypes.QueryDelegationTotalRewardsResponse{}, nil
}

func (noopDistributionKeeper) DelegatorValidators(_ context.Context, _ *distrtypes.QueryDelegatorValidatorsRequest) (*distrtypes.QueryDelegatorValidatorsResponse, error) {
	return &distrtypes.QueryDelegatorValidatorsResponse{}, nil
}

type noopStakingKeeper struct{}

func (noopStakingKeeper) BondDenom(_ sdk.Context) string {
	return "stake"
}

func (noopStakingKeeper) GetValidator(_ sdk.Context, _ sdk.ValAddress) (stakingtypes.Validator, bool) {
	return stakingtypes.Validator{}, false
}

func (noopStakingKeeper) GetBondedValidatorsByPower(_ sdk.Context) []stakingtypes.Validator {
	return nil
}

func (noopStakingKeeper) GetAllDelegatorDelegations(_ sdk.Context, _ sdk.AccAddress) []stakingtypes.Delegation {
	return nil
}

func (noopStakingKeeper) GetDelegation(_ sdk.Context, _ sdk.AccAddress, _ sdk.ValAddress) (stakingtypes.Delegation, bool) {
	return stakingtypes.Delegation{}, false
}

func (noopStakingKeeper) HasReceivingRedelegation(_ sdk.Context, _ sdk.AccAddress, _ sdk.ValAddress) bool {
	return false
}

type ibcClientStakingKeeper struct {
	enabled bool
}

func (k ibcClientStakingKeeper) GetHistoricalInfo(_ sdk.Context, _ int64) (stakingtypes.HistoricalInfo, bool) {
	if !k.enabled {
		return stakingtypes.HistoricalInfo{}, false
	}
	return stakingtypes.HistoricalInfo{}, true
}

func (k ibcClientStakingKeeper) UnbondingTime(_ sdk.Context) time.Duration {
	if !k.enabled {
		return 0
	}
	return 24 * time.Hour
}

type ibcClientUpgradeKeeper struct {
	enabled bool
}

func (k ibcClientUpgradeKeeper) ClearIBCState(_ sdk.Context, _ int64) {}

func (k ibcClientUpgradeKeeper) GetUpgradePlan(_ sdk.Context) (upgradetypes.Plan, bool) {
	if !k.enabled {
		return upgradetypes.Plan{}, false
	}
	return upgradetypes.Plan{}, false
}

func (k ibcClientUpgradeKeeper) GetUpgradedClient(_ sdk.Context, _ int64) ([]byte, bool) {
	if !k.enabled {
		return nil, false
	}
	return nil, false
}

func (k ibcClientUpgradeKeeper) SetUpgradedClient(_ sdk.Context, _ int64, _ []byte) error {
	if !k.enabled {
		return nil
	}
	return nil
}

func (k ibcClientUpgradeKeeper) GetUpgradedConsensusState(_ sdk.Context, _ int64) ([]byte, bool) {
	if !k.enabled {
		return nil, false
	}
	return nil, false
}

func (k ibcClientUpgradeKeeper) SetUpgradedConsensusState(_ sdk.Context, _ int64, _ []byte) error {
	if !k.enabled {
		return nil
	}
	return nil
}

func (k ibcClientUpgradeKeeper) ScheduleUpgrade(_ sdk.Context, _ upgradetypes.Plan) error {
	if !k.enabled {
		return nil
	}
	return nil
}
