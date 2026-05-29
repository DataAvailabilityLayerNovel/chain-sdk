// package cosmoswasm: file định nghĩa các hàm build tx cho FAUCET — bot/script
// cấp tiền cho tài khoản mới từ ví treasury. Dùng SIGN_MODE_DIRECT (chuẩn
// Cosmos), 0 phí mặc định.
package cosmoswasm

import (
	"errors"  // errors.New cho lỗi đơn giản.
	"fmt"     // bọc lỗi với context.
	"strings" // TrimSpace input người dùng.

	// sdk: type lõi Cosmos (AccAddress, Coins).
	sdk "github.com/cosmos/cosmos-sdk/types"
	// banktypes: định nghĩa MsgSend (chuyển token giữa 2 địa chỉ).
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	// txcodec (internal): ký + serialize tx theo chuẩn Cosmos proto.
	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm/internal/txcodec"
)

// BuildSignedBankSend builds a signed bank MsgSend (SIGN_MODE_DIRECT, 0-fee)
// from a 32-byte hex secp256k1 private key. It is the primitive a faucet uses
// to fund fresh accounts from a treasury key.
//
// accNum/seq must match the treasury's on-chain auth account state at the time
// the tx is verified (only enforced when COSMOS_EXEC_ENFORCE_SIGNATURES=true).
// gas is the tx gas limit; pass 0 for the SDK default. The fee amount is left
// at zero — the chain accepts 0-fee txs; raise it here only after wiring a real
// TxFeeChecker (see docs/fee-economics.md).
//
// Returns the treasury's bech32 address (derived from the key) and the raw
// TxRaw bytes ready for /tx/submit.
//
// VI: build tx CHUYỂN TIỀN có ký, dùng cho faucet. accNum/seq PHẢI khớp state
// thật của treasury (nếu chain bật ép chữ ký). Phí = 0 (chỉ chạy khi chain
// chưa bật min-gas-price). Trả về (địa chỉ treasury, tx bytes, error).
func BuildSignedBankSend(privHex, chainID, toBech32 string, amount sdk.Coins, accNum, seq, gas uint64) (fromBech32 string, txBytes []byte, err error) {
	// Uỷ thác cho hàm "...WithFee" với fee=nil → tương đương 0-fee.
	return BuildSignedBankSendWithFee(privHex, chainID, toBech32, amount, nil, accNum, seq, gas)
}

// BuildSignedBankSendWithFee is BuildSignedBankSend with an explicit tx fee.
// Pass a non-empty fee when the chain enforces a min gas price (ante feePolicy
// with COSMOS_EXEC_MIN_GAS_PRICE > 0), otherwise the treasury's own tx is
// rejected with ErrInsufficientFee. fee must be payable from the treasury
// balance and use the gas denom. A nil/empty fee reproduces the 0-fee behavior.
//
// VI: phiên bản đầy đủ — caller TỰ truyền phí. Khi chain bật min-gas-price,
// phí phải >= ceil(gas * minGasPrice) đúng denom, nếu không TX CHÍNH CỦA
// FAUCET cũng bị reject (faucet ăn dao chính mình).
func BuildSignedBankSendWithFee(privHex, chainID, toBech32 string, amount, fee sdk.Coins, accNum, seq, gas uint64) (fromBech32 string, txBytes []byte, err error) {
	if strings.TrimSpace(chainID) == "" {
		return "", nil, errors.New("chainID is required")
	}
	if strings.TrimSpace(toBech32) == "" {
		return "", nil, errors.New("recipient address is required")
	}
	// IsZero: không có coin nào / mọi coin = 0. IsValid: denom hợp lệ, số > 0
	// (Coins sorted & non-negative). Sai 1 trong 2 → lỗi.
	if amount.IsZero() || !amount.IsValid() {
		return "", nil, fmt.Errorf("invalid send amount %q", amount.String())
	}

	// Parse khoá riêng treasury từ hex 32 byte → cryptotypes.PrivKey.
	priv, err := txcodec.NewSecp256k1FromHex(privHex)
	if err != nil {
		return "", nil, fmt.Errorf("parse treasury key: %w", err)
	}
	// Suy ra địa chỉ bech32 của treasury từ pubkey.
	from := sdk.AccAddress(priv.PubKey().Address()).String()

	// Khởi tạo msg "chuyển tiền" (MsgSend là Msg chuẩn của module bank).
	msg := &banktypes.MsgSend{
		FromAddress: from,
		ToAddress:   toBech32,
		Amount:      amount,
	}

	// Ký & serialize msg thành TxRaw (bytes sẵn sàng gửi /tx/submit).
	raw, err := txcodec.BuildSignedProtoTxBytes(txcodec.SignerInput{
		PrivKey:       priv,
		ChainID:       chainID,
		AccountNumber: accNum,
		Sequence:      seq,
		GasLimit:      gas,
		FeeAmount:     fee,
	}, msg)
	if err != nil {
		return "", nil, fmt.Errorf("sign bank send: %w", err)
	}
	return from, raw, nil
}

// TreasuryAddressFromHex returns the bech32 address for a hex secp256k1 key
// without building a tx — used to compute the genesis balance recipient.
//
// VI: lấy địa chỉ treasury từ khoá hex KHÔNG cần build tx. Dùng khi cần
// biết trước "ai nhận tiền genesis" (vd: setup file genesis có balance cho
// treasury).
func TreasuryAddressFromHex(privHex string) (string, error) {
	priv, err := txcodec.NewSecp256k1FromHex(privHex)
	if err != nil {
		return "", err
	}
	return sdk.AccAddress(priv.PubKey().Address()).String(), nil
}
