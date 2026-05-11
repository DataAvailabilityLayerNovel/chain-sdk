package app

import (
	"context"
	"time"

	"cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	distrtypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	upgradetypes "cosmossdk.io/x/upgrade/types"
)

// noopDistributionKeeper satisfies the wasmtypes.DistributionKeeper interface.
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

// noopStakingKeeper satisfies the wasmtypes.StakingKeeper and
// wasmkeeper.ValidatorSetSource interfaces.
type noopStakingKeeper struct{}

func (noopStakingKeeper) BondDenom(_ context.Context) (string, error) {
	return "stake", nil
}

func (noopStakingKeeper) GetValidator(_ context.Context, _ sdk.ValAddress) (stakingtypes.Validator, error) {
	return stakingtypes.Validator{}, stakingtypes.ErrNoValidatorFound
}

func (noopStakingKeeper) GetBondedValidatorsByPower(_ context.Context) ([]stakingtypes.Validator, error) {
	return nil, nil
}

func (noopStakingKeeper) GetAllDelegatorDelegations(_ context.Context, _ sdk.AccAddress) ([]stakingtypes.Delegation, error) {
	return nil, nil
}

func (noopStakingKeeper) GetDelegation(_ context.Context, _ sdk.AccAddress, _ sdk.ValAddress) (stakingtypes.Delegation, error) {
	return stakingtypes.Delegation{}, stakingtypes.ErrNoDelegation
}

func (noopStakingKeeper) HasReceivingRedelegation(_ context.Context, _ sdk.AccAddress, _ sdk.ValAddress) (bool, error) {
	return false, nil
}

func (noopStakingKeeper) ApplyAndReturnValidatorSetUpdates(_ context.Context) ([]abci.ValidatorUpdate, error) {
	return nil, nil
}

func (noopStakingKeeper) TotalBondedTokens(_ context.Context) (math.Int, error) {
	return math.ZeroInt(), nil
}

// ibcClientStakingKeeper satisfies the IBC staking keeper interface.
type ibcClientStakingKeeper struct {
	enabled bool
}

func (k ibcClientStakingKeeper) GetHistoricalInfo(_ context.Context, _ int64) (stakingtypes.HistoricalInfo, error) {
	if !k.enabled {
		return stakingtypes.HistoricalInfo{}, stakingtypes.ErrNoHistoricalInfo
	}
	return stakingtypes.HistoricalInfo{}, nil
}

func (k ibcClientStakingKeeper) UnbondingTime(_ context.Context) (time.Duration, error) {
	if !k.enabled {
		return 0, nil
	}
	return 24 * time.Hour, nil
}

// ibcClientUpgradeKeeper satisfies the IBC upgrade keeper interface.
type ibcClientUpgradeKeeper struct {
	enabled bool
}

func (k ibcClientUpgradeKeeper) ClearIBCState(_ context.Context, _ int64) error {
	return nil
}

func (k ibcClientUpgradeKeeper) GetUpgradePlan(_ context.Context) (upgradetypes.Plan, error) {
	return upgradetypes.Plan{}, nil
}

func (k ibcClientUpgradeKeeper) GetUpgradedClient(_ context.Context, _ int64) ([]byte, error) {
	return nil, nil
}

func (k ibcClientUpgradeKeeper) SetUpgradedClient(_ context.Context, _ int64, _ []byte) error {
	return nil
}

func (k ibcClientUpgradeKeeper) GetUpgradedConsensusState(_ context.Context, _ int64) ([]byte, error) {
	return nil, nil
}

func (k ibcClientUpgradeKeeper) SetUpgradedConsensusState(_ context.Context, _ int64, _ []byte) error {
	return nil
}

func (k ibcClientUpgradeKeeper) ScheduleUpgrade(_ context.Context, _ upgradetypes.Plan) error {
	return nil
}
