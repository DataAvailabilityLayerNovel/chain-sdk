package app

import (
	"os"
	"strings"

	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"
	txsigning "cosmossdk.io/x/tx/signing"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	authante "github.com/cosmos/cosmos-sdk/x/auth/ante"
	authkeeper "github.com/cosmos/cosmos-sdk/x/auth/keeper"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"
	bankkeeper "github.com/cosmos/cosmos-sdk/x/bank/keeper"
)

// enforceSignaturesEnv returns true when COSMOS_EXEC_ENFORCE_SIGNATURES is set
// to a truthy value. Production / public-submission setups should set this.
func enforceSignaturesEnv() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("COSMOS_EXEC_ENFORCE_SIGNATURES")))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

// feePolicy is the minimum-gas-price rule applied by the TxFeeChecker. It is
// loaded from the same env vars cost.go uses so /tx/estimate and on-chain
// enforcement agree. When minGasPrice is zero (env unset) enforcement is OFF
// and 0-fee txs are accepted — preserving the permissionless dev default.
type feePolicy struct {
	minGasPrice math.LegacyDec
	denom       string
}

func feePolicyFromEnv() feePolicy {
	denom := strings.TrimSpace(os.Getenv("COSMOS_EXEC_GAS_DENOM"))
	if denom == "" {
		denom = "ustake"
	}
	p := feePolicy{minGasPrice: math.LegacyZeroDec(), denom: denom}
	if v := strings.TrimSpace(os.Getenv("COSMOS_EXEC_MIN_GAS_PRICE")); v != "" {
		if d, err := math.LegacyNewDecFromStr(v); err == nil && d.IsPositive() {
			p.minGasPrice = d
		}
	}
	return p
}

// enforced reports whether a positive min gas price is configured.
func (p feePolicy) enforced() bool { return p.minGasPrice.IsPositive() }

// requiredFee is ceil(gas * minGasPrice) in the policy denom.
func (p feePolicy) requiredFee(gas uint64) math.Int {
	return p.minGasPrice.MulInt(math.NewIntFromUint64(gas)).Ceil().RoundInt()
}

// txFeeChecker returns the TxFeeChecker wired into NewDeductFeeDecorator.
// Behavior:
//   - policy off (no COSMOS_EXEC_MIN_GAS_PRICE): accept any fee incl. 0
//     (unchanged dev behavior; existing tests keep passing).
//   - policy on: require the tx fee in the gas denom to cover gas*minGasPrice,
//     else ErrInsufficientFee. Enforced in BOTH CheckTx and block execution so
//     a tx that underpays is rejected even though cosmos-exec has no CheckTx
//     mempool-admission step (InjectTx just queues bytes; the ante chain only
//     runs in FinalizeBlock). NOTE: this means every node (sequencer + full
//     nodes) MUST run with the same COSMOS_EXEC_MIN_GAS_PRICE / GAS_DENOM —
//     config drift would let a full node reject a tx the sequencer included
//     and fork. They already share one .env, so this holds in this stack.
//   - simulate (ExecModeSimulate): NEVER reject. /tx/simulate runs a 0-fee tx
//     just to measure gas; the client only learns the gas (and thus the fee)
//     from that result, so enforcing here would be a chicken-and-egg deadlock.
func txFeeChecker(p feePolicy) authante.TxFeeChecker {
	return func(ctx sdk.Context, tx sdk.Tx) (sdk.Coins, int64, error) {
		feeTx, ok := tx.(sdk.FeeTx)
		if !ok {
			return nil, 0, errorsmod.Wrap(sdkerrors.ErrTxDecode, "tx must be a FeeTx")
		}
		fee := feeTx.GetFee()
		gas := feeTx.GetGas()

		if p.enforced() && ctx.ExecMode() != sdk.ExecModeSimulate {
			required := p.requiredFee(gas)
			paid := fee.AmountOf(p.denom)
			if paid.LT(required) {
				return nil, 0, errorsmod.Wrapf(
					sdkerrors.ErrInsufficientFee,
					"insufficient fee: got %s, need at least %s%s (gas %d * min price %s)",
					fee, required, p.denom, gas, p.minGasPrice,
				)
			}
		}

		// Priority = fee paid in the gas denom (clamped to int64).
		priority := int64(0)
		if paid := fee.AmountOf(p.denom); paid.IsInt64() {
			priority = paid.Int64()
		} else if paid.IsPositive() {
			priority = int64(^uint64(0) >> 1) // clamp to max int64
		}
		return fee, priority, nil
	}
}

// AutoCreateAccountDecorator creates a BaseAccount on the fly for any signer
// whose address doesn't yet exist in state. This lets clients submit a signed
// tx without first interacting with a faucet — the first signed tx itself
// is what registers the account.
//
// Must run BEFORE SetPubKeyDecorator and SigVerificationDecorator so they have
// an account record to populate.
type AutoCreateAccountDecorator struct {
	ak authkeeper.AccountKeeper
}

func NewAutoCreateAccountDecorator(ak authkeeper.AccountKeeper) AutoCreateAccountDecorator {
	return AutoCreateAccountDecorator{ak: ak}
}

func (d AutoCreateAccountDecorator) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (sdk.Context, error) {
	sigTx, ok := tx.(authsigning.SigVerifiableTx)
	if !ok {
		return ctx, errorsmod.Wrap(sdkerrors.ErrTxDecode, "tx must be a SigVerifiableTx")
	}

	sigs, err := sigTx.GetSignaturesV2()
	if err != nil {
		return ctx, err
	}

	for _, sig := range sigs {
		if sig.PubKey == nil {
			continue
		}
		addr := sdk.AccAddress(sig.PubKey.Address())
		if !d.ak.HasAccount(ctx, addr) {
			acc := d.ak.NewAccountWithAddress(ctx, addr)
			d.ak.SetAccount(ctx, acc)
		}
	}

	return next(ctx, tx, simulate)
}

// NewPermissionlessAnteHandler builds an AnteHandler that:
//   - verifies tx signatures (anti-impersonation),
//   - enforces sequence numbers (anti-replay),
//   - charges gas based on tx size (anti-DOS),
//   - auto-creates accounts on first signed tx (no faucet needed).
//
// Fee enforcement is controlled by COSMOS_EXEC_MIN_GAS_PRICE (see feePolicy):
// unset → 0-fee txs accepted (dev default); set → CheckTx requires
// gas*minGasPrice in COSMOS_EXEC_GAS_DENOM, else ErrInsufficientFee.
func NewPermissionlessAnteHandler(
	ak authkeeper.AccountKeeper,
	bk bankkeeper.Keeper,
	signModeHandler *txsigning.HandlerMap,
) sdk.AnteHandler {
	return sdk.ChainAnteDecorators(
		authante.NewSetUpContextDecorator(),
		authante.NewExtensionOptionsDecorator(nil),
		authante.NewValidateBasicDecorator(),
		authante.NewTxTimeoutHeightDecorator(),
		authante.NewValidateMemoDecorator(ak),
		authante.NewConsumeGasForTxSizeDecorator(ak),
		// Auto-create accounts BEFORE DeductFee so the fee payer (= first signer
		// by default) exists in state when DeductFee looks it up. Otherwise the
		// first tx from a fresh Keplr account fails with "fee payer address
		// <bytes> does not exist: unknown address".
		NewAutoCreateAccountDecorator(ak),
		// Fee rule from env: 0-fee when unset, gas*minGasPrice when configured.
		authante.NewDeductFeeDecorator(ak, bk, nil, txFeeChecker(feePolicyFromEnv())),
		authante.NewSetPubKeyDecorator(ak),
		authante.NewValidateSigCountDecorator(ak),
		authante.NewSigGasConsumeDecorator(ak, authante.DefaultSigVerificationGasConsumer),
		authante.NewSigVerificationDecorator(ak, signModeHandler),
		authante.NewIncrementSequenceDecorator(ak),
	)
}
