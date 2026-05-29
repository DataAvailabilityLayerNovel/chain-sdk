// package app: khai báo file này thuộc package "app" — gom toàn bộ logic
// khởi tạo và cấu hình ứng dụng Cosmos SDK (ante handler, keeper, genesis...).
package app

// import (...) : khối khai báo các thư viện được dùng trong file.
import (
	"os"      // đọc biến môi trường (os.Getenv) — dùng để bật/tắt rule qua ENV.
	"strings" // xử lý chuỗi: cắt khoảng trắng, đổi chữ thường, so sánh.

	// errorsmod: gói lỗi của Cosmos, cho phép "bọc" (wrap) lỗi kèm thông điệp.
	errorsmod "cosmossdk.io/errors"
	// math: kiểu số học của Cosmos (LegacyDec = số thập phân, Int = số nguyên lớn).
	"cosmossdk.io/math"
	// txsigning: chứa HandlerMap — bản đồ các kiểu ký (sign mode) được hỗ trợ.
	txsigning "cosmossdk.io/x/tx/signing"
	// sdk: alias cho types gốc của Cosmos SDK (Context, Tx, Coins, AccAddress...).
	sdk "github.com/cosmos/cosmos-sdk/types"
	// sdkerrors: tập các lỗi chuẩn (ErrInsufficientFee, ErrTxDecode...).
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	// authante: các "decorator" ante handler dựng sẵn của module auth.
	authante "github.com/cosmos/cosmos-sdk/x/auth/ante"
	// authkeeper: keeper quản lý tài khoản (account) — đọc/ghi account vào state.
	authkeeper "github.com/cosmos/cosmos-sdk/x/auth/keeper"
	// authsigning: interface mô tả tx có chữ ký (SigVerifiableTx).
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"
	// bankkeeper: keeper quản lý số dư token — dùng khi trừ phí (DeductFee).
	bankkeeper "github.com/cosmos/cosmos-sdk/x/bank/keeper"
)

// enforceSignaturesEnv returns true when COSMOS_EXEC_ENFORCE_SIGNATURES is set
// to a truthy value. Production / public-submission setups should set this.
//
// VI: hàm trả về bool — true nếu ENV COSMOS_EXEC_ENFORCE_SIGNATURES bật.
// Dùng để quyết định có bắt buộc kiểm tra chữ ký hay không (môi trường
// production nên bật).
func enforceSignaturesEnv() bool {
	// os.Getenv(...): đọc giá trị ENV (chuỗi, "" nếu chưa set).
	// strings.TrimSpace: bỏ khoảng trắng đầu/cuối.
	// strings.ToLower: đổi về chữ thường để so sánh không phân biệt hoa/thường.
	v := strings.ToLower(strings.TrimSpace(os.Getenv("COSMOS_EXEC_ENFORCE_SIGNATURES")))
	// trả về true nếu giá trị thuộc tập "bật": 1 / true / yes / on.
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

// treasuryAddrFromEnv reads COSMOS_EXEC_TREASURY_ADDR and returns the parsed
// bech32 address. Returns nil (empty) when unset or malformed — the EndBlocker
// sweep treats that as "feature disabled" and leaves fees in the collector.
//
// IMPORTANT: this value must be identical on every node (sequencer + full
// nodes). Config drift would let one node sweep while another doesn't, which
// produces divergent app state and a fork. Same constraint as
// COSMOS_EXEC_MIN_GAS_PRICE / COSMOS_EXEC_GAS_DENOM.
//
// VI: đọc địa chỉ ví "treasury" từ ENV. Rỗng/sai định dạng → trả nil,
// EndBlocker sẽ KHÔNG quét phí (giữ nguyên hành vi mặc định). Mọi node
// phải set giá trị giống nhau, nếu lệch sẽ fork.
func treasuryAddrFromEnv() sdk.AccAddress {
	v := strings.TrimSpace(os.Getenv("COSMOS_EXEC_TREASURY_ADDR"))
	if v == "" {
		return nil
	}
	addr, err := sdk.AccAddressFromBech32(v)
	if err != nil {
		return nil
	}
	return addr
}

// feePolicy is the minimum-gas-price rule applied by the TxFeeChecker. It is
// loaded from the same env vars cost.go uses so /tx/estimate and on-chain
// enforcement agree. When minGasPrice is zero (env unset) enforcement is OFF
// and 0-fee txs are accepted — preserving the permissionless dev default.
//
// VI: struct mô tả "chính sách phí". minGasPrice = giá gas tối thiểu;
// denom = đơn vị token tính phí. Nếu minGasPrice = 0 thì KHÔNG ép phí
// (mặc định dev, chấp nhận tx phí 0).
type feePolicy struct {
	minGasPrice math.LegacyDec // giá gas tối thiểu (số thập phân lớn).
	denom       string         // đơn vị token, ví dụ "ustake".
}

// feePolicyFromEnv: hàm dựng feePolicy bằng cách đọc cấu hình từ ENV.
// Trả về: một feePolicy đã điền sẵn giá trị.
func feePolicyFromEnv() feePolicy {
	// Đọc đơn vị token từ ENV COSMOS_EXEC_GAS_DENOM.
	denom := strings.TrimSpace(os.Getenv("COSMOS_EXEC_GAS_DENOM"))
	if denom == "" { // nếu ENV trống → dùng giá trị mặc định "ustake".
		denom = "ustake"
	}
	// Khởi tạo policy với minGasPrice = 0 (tức tắt ép phí mặc định).
	p := feePolicy{minGasPrice: math.LegacyZeroDec(), denom: denom}
	// Nếu ENV COSMOS_EXEC_MIN_GAS_PRICE có giá trị...
	if v := strings.TrimSpace(os.Getenv("COSMOS_EXEC_MIN_GAS_PRICE")); v != "" {
		// ...parse chuỗi thành số thập phân. Cú pháp `if a, err := f(); cond`
		// là khai báo biến cục bộ ngay trong if.
		if d, err := math.LegacyNewDecFromStr(v); err == nil && d.IsPositive() {
			// chỉ nhận khi parse thành công (err == nil) và > 0.
			p.minGasPrice = d
		}
	}
	return p // trả về policy hoàn chỉnh.
}

// enforced reports whether a positive min gas price is configured.
//
// VI: method trên feePolicy (receiver `p feePolicy`). Trả về true nếu
// minGasPrice > 0 → tức chính sách phí đang được áp dụng.
func (p feePolicy) enforced() bool { return p.minGasPrice.IsPositive() }

// requiredFee is ceil(gas * minGasPrice) in the policy denom.
//
// VI: tính phí tối thiểu cần trả = làm tròn lên (ceil) của (gas * minGasPrice).
// Tham số: gas (uint64). Trả về: math.Int (số nguyên lớn).
func (p feePolicy) requiredFee(gas uint64) math.Int {
	// MulInt: nhân Dec với Int; Ceil: làm tròn lên; RoundInt: ép về Int.
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
//
// VI: hàm trả về một CLOSURE (hàm con) đúng kiểu authante.TxFeeChecker.
// Closure này được module DeductFee gọi cho mỗi tx để: kiểm tra phí đủ chưa
// và tính "độ ưu tiên" (priority). Tham số p: chính sách phí đã nạp từ ENV.
func txFeeChecker(p feePolicy) authante.TxFeeChecker {
	// return func(...): trả về hàm. Đầu vào ctx (ngữ cảnh giao dịch) + tx.
	// Đầu ra: (phí sdk.Coins, priority int64, error).
	return func(ctx sdk.Context, tx sdk.Tx) (sdk.Coins, int64, error) {
		// Ép kiểu (type assertion) tx về sdk.FeeTx để lấy được phí/gas.
		// Cú pháp `v, ok := x.(T)`: ok=false nếu x không phải kiểu T.
		feeTx, ok := tx.(sdk.FeeTx)
		if !ok {
			// Không phải FeeTx → trả lỗi giải mã, bọc kèm thông điệp.
			return nil, 0, errorsmod.Wrap(sdkerrors.ErrTxDecode, "tx must be a FeeTx")
		}
		fee := feeTx.GetFee() // sdk.Coins: phí người dùng đính kèm.
		gas := feeTx.GetGas() // uint64: lượng gas tx khai báo.

		// Chỉ ép phí khi policy bật VÀ không phải chế độ simulate.
		if p.enforced() && ctx.ExecMode() != sdk.ExecModeSimulate {
			required := p.requiredFee(gas)    // phí tối thiểu phải có.
			paid := fee.AmountOf(p.denom)     // số đã trả theo đúng denom.
			if paid.LT(required) {            // LT = less-than: trả thiếu.
				// Wrapf: bọc lỗi kèm thông điệp có định dạng (%s, %d...).
				return nil, 0, errorsmod.Wrapf(
					sdkerrors.ErrInsufficientFee,
					"insufficient fee: got %s, need at least %s%s (gas %d * min price %s)",
					fee, required, p.denom, gas, p.minGasPrice,
				)
			}
		}

		// Priority = fee paid in the gas denom (clamped to int64).
		// VI: priority = số phí trả (theo denom) — tx phí cao được ưu tiên.
		priority := int64(0)
		if paid := fee.AmountOf(p.denom); paid.IsInt64() {
			// Nếu vừa kiểu int64 → dùng trực tiếp.
			priority = paid.Int64()
		} else if paid.IsPositive() {
			// Quá lớn không vừa int64 → kẹp về giá trị int64 lớn nhất.
			// `^uint64(0) >> 1` = 0x7FFF... = max int64.
			priority = int64(^uint64(0) >> 1) // clamp to max int64
		}
		// Trả về: phí hợp lệ, priority, không lỗi (nil).
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
//
// VI: struct decorator tự tạo tài khoản nếu địa chỉ người ký chưa tồn tại
// trong state. Nhờ vậy tx ký lần đầu không cần xin faucet trước.
// Phải chạy TRƯỚC các decorator kiểm tra pubkey/chữ ký.
type AutoCreateAccountDecorator struct {
	ak authkeeper.AccountKeeper // keeper để đọc/tạo/ghi account.
}

// NewAutoCreateAccountDecorator: hàm khởi tạo (constructor) decorator.
// Tham số: account keeper. Trả về: bản sao struct đã gắn keeper.
func NewAutoCreateAccountDecorator(ak authkeeper.AccountKeeper) AutoCreateAccountDecorator {
	return AutoCreateAccountDecorator{ak: ak}
}

// AnteHandle: method bắt buộc để struct này là một ante decorator.
// Tham số:
//   - ctx: ngữ cảnh giao dịch (chứa state, block height...).
//   - tx: giao dịch đang xử lý.
//   - simulate: true nếu chỉ chạy thử để ước lượng gas.
//   - next: decorator kế tiếp trong chuỗi (gọi để đi tiếp).
//
// Trả về: ctx (có thể đã sửa) và error.
func (d AutoCreateAccountDecorator) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (sdk.Context, error) {
	// Ép tx về SigVerifiableTx để lấy danh sách chữ ký.
	sigTx, ok := tx.(authsigning.SigVerifiableTx)
	if !ok {
		return ctx, errorsmod.Wrap(sdkerrors.ErrTxDecode, "tx must be a SigVerifiableTx")
	}

	// GetSignaturesV2: lấy danh sách chữ ký (mỗi cái có PubKey).
	sigs, err := sigTx.GetSignaturesV2()
	if err != nil {
		return ctx, err // có lỗi → trả ngay, dừng chuỗi.
	}

	// Duyệt từng chữ ký. `for _, sig := range sigs` bỏ qua index, lấy phần tử.
	for _, sig := range sigs {
		if sig.PubKey == nil { // không có pubkey → bỏ qua.
			continue
		}
		// Suy ra địa chỉ tài khoản từ pubkey.
		addr := sdk.AccAddress(sig.PubKey.Address())
		// Nếu account chưa tồn tại trong state...
		if !d.ak.HasAccount(ctx, addr) {
			// ...tạo account mới gắn địa chỉ này...
			acc := d.ak.NewAccountWithAddress(ctx, addr)
			// ...rồi lưu vào state.
			d.ak.SetAccount(ctx, acc)
		}
	}

	// Gọi decorator kế tiếp để tiếp tục chuỗi xử lý ante.
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
//
// VI: hàm lắp ráp toàn bộ "chuỗi ante" — danh sách bước kiểm tra chạy
// tuần tự cho MỖI tx trước khi thực thi. Tham số: account keeper, bank
// keeper, bản đồ sign mode. Trả về: 1 sdk.AnteHandler đã ghép sẵn.
func NewPermissionlessAnteHandler(
	ak authkeeper.AccountKeeper,
	bk bankkeeper.Keeper,
	signModeHandler *txsigning.HandlerMap,
) sdk.AnteHandler {
	// ChainAnteDecorators: nối nhiều decorator thành 1 handler chạy theo thứ tự.
	return sdk.ChainAnteDecorators(
		authante.NewSetUpContextDecorator(),         // dựng ctx + đặt giới hạn gas.
		authante.NewExtensionOptionsDecorator(nil),  // chặn extension option lạ.
		authante.NewValidateBasicDecorator(),        // kiểm tra cơ bản (ValidateBasic).
		authante.NewTxTimeoutHeightDecorator(),      // tx hết hạn theo block height.
		authante.NewValidateMemoDecorator(ak),       // kiểm tra độ dài memo.
		authante.NewConsumeGasForTxSizeDecorator(ak),// tính gas theo kích thước tx (chống DOS).
		// Auto-create accounts BEFORE DeductFee so the fee payer (= first signer
		// by default) exists in state when DeductFee looks it up. Otherwise the
		// first tx from a fresh Keplr account fails with "fee payer address
		// <bytes> does not exist: unknown address".
		// VI: tạo account TRƯỚC bước trừ phí, để người trả phí đã tồn tại.
		NewAutoCreateAccountDecorator(ak),
		// Fee rule from env: 0-fee when unset, gas*minGasPrice when configured.
		// VI: trừ phí; quy tắc phí lấy từ ENV qua txFeeChecker(feePolicyFromEnv()).
		authante.NewDeductFeeDecorator(ak, bk, nil, txFeeChecker(feePolicyFromEnv())),
		authante.NewSetPubKeyDecorator(ak),          // gắn pubkey vào account.
		authante.NewValidateSigCountDecorator(ak),   // giới hạn số lượng chữ ký.
		authante.NewSigGasConsumeDecorator(ak, authante.DefaultSigVerificationGasConsumer), // tính gas cho việc verify chữ ký.
		authante.NewSigVerificationDecorator(ak, signModeHandler), // xác minh chữ ký thật.
		authante.NewIncrementSequenceDecorator(ak), // tăng sequence (chống replay).
	)
}
