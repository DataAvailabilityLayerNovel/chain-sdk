package app

import (
	"os"
	"strings"

	errorsmod "cosmossdk.io/errors"
	txsigning "cosmossdk.io/x/tx/signing"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	authante "github.com/cosmos/cosmos-sdk/x/auth/ante"
	authkeeper "github.com/cosmos/cosmos-sdk/x/auth/keeper"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	bankkeeper "github.com/cosmos/cosmos-sdk/x/bank/keeper"
)

// enforceSignaturesEnv returns true when COSMOS_EXEC_ENFORCE_SIGNATURES is set
// to a truthy value. Production / public-submission setups should set this.
func enforceSignaturesEnv() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("COSMOS_EXEC_ENFORCE_SIGNATURES")))
	return v == "1" || v == "true" || v == "yes" || v == "on"
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
// It does NOT charge fees — TxFeeChecker accepts 0-fee txs. Add economic fees
// later by wiring a real TxFeeChecker + MinGasPrices.
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
		// Accept 0-fee txs. Replace with a real checker once a fee token exists.
		authante.NewDeductFeeDecorator(ak, bk, nil, func(_ sdk.Context, tx sdk.Tx) (sdk.Coins, int64, error) {
			feeTx, ok := tx.(sdk.FeeTx)
			if !ok {
				return nil, 0, errorsmod.Wrap(sdkerrors.ErrTxDecode, "tx must be a FeeTx")
			}
			return feeTx.GetFee(), int64(feeTx.GetGas()), nil
		}),
		NewAutoCreateAccountDecorator(ak),
		authante.NewSetPubKeyDecorator(ak),
		authante.NewValidateSigCountDecorator(ak),
		authante.NewSigGasConsumeDecorator(ak, authante.DefaultSigVerificationGasConsumer),
		authante.NewSigVerificationDecorator(ak, signModeHandler),
		authante.NewIncrementSequenceDecorator(ak),
	)
}

// _ ensures authtypes import is retained even if unused above.
var _ = authtypes.ProtoBaseAccount
