// package cosmoswasm: SDK phía client để dApp ký & gửi tx tới chain cosmos-exec.
// File này định nghĩa Signer — giữ khoá ký, chain id, gas, phí — dùng để
// ký mọi tx (Store/Instantiate/Execute WASM).
package cosmoswasm

import (
	"context" // context.Context: truyền tín hiệu huỷ/timeout xuyên các hàm.
	"errors"  // tạo lỗi đơn giản (errors.New).
	"fmt"     // định dạng chuỗi & bọc lỗi.

	// cryptotypes: interface PrivKey (mọi loại khoá riêng — secp256k1, ed25519...).
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	// sdk: type lõi Cosmos (AccAddress, Coins...).
	sdk "github.com/cosmos/cosmos-sdk/types"

	// txcodec (internal): logic mã hoá tx + parse khoá riêng từ hex.
	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm/internal/txcodec"
)

// Signer holds the credentials required to produce a signed tx. Create one
// per user (or per app key) and pass it to BuildStoreTx / BuildInstantiateTx /
// BuildExecuteTx. Sequence is fetched from the chain automatically when the tx
// is built — callers don't need to track it.
//
// VI: struct giữ thông tin để KÝ tx — khoá riêng, chain id, giới hạn gas, phí.
// Tạo 1 Signer cho mỗi người dùng (hoặc mỗi key của app). Sequence (số thứ tự
// tx) được tự lấy từ chain khi build, người gọi không cần quản lý.
type Signer struct {
	privKey   cryptotypes.PrivKey // khoá riêng để ký (interface — ẩn loại thuật toán).
	chainID   string              // định danh chain — phải khớp với chain đang gọi.
	gasLimit  uint64              // gas tối đa cho mỗi tx (chống loop vô hạn).
	feeAmount sdk.Coins           // phí (slice Coin) đính kèm tx; rỗng = phí 0.
}

// NewSignerFromHex creates a Signer from a 32-byte hex-encoded secp256k1
// private key and the rollup chain ID.
//
// VI: constructor — tạo Signer từ chuỗi hex 32 byte (khoá secp256k1) + chain ID.
// Tham số: hexPrivKey, chainID. Trả về (*Signer, error).
func NewSignerFromHex(hexPrivKey, chainID string) (*Signer, error) {
	// Parse hex -> khoá secp256k1 thật. Trả về interface PrivKey.
	pk, err := txcodec.NewSecp256k1FromHex(hexPrivKey)
	if err != nil {
		return nil, err // hex sai định dạng / sai độ dài → trả ngay.
	}
	if chainID == "" {
		return nil, errors.New("chainID is required")
	}
	// Khởi tạo Signer với gas mặc định 100 triệu (dấu _ cho dễ đọc).
	return &Signer{privKey: pk, chainID: chainID, gasLimit: 100_000_000}, nil
}

// WithGasLimit overrides the default per-tx gas limit (100M). Lower it to
// constrain compute; raise it only for unusually heavy WASM uploads.
//
// VI: method ghi đè gas mặc định. Trả về CHÍNH s (con trỏ Signer) để có thể
// xâu chuỗi cú pháp builder (vd: signer.WithGasLimit(...).WithFee(...)).
func (s *Signer) WithGasLimit(limit uint64) *Signer {
	s.gasLimit = limit
	return s
}

// WithFee sets the tx fee attached to every signed tx this Signer builds.
// Leave it unset for a 0-fee tx (works only when the chain runs with
// COSMOS_EXEC_MIN_GAS_PRICE unset/0). When the chain enforces a min gas
// price, pass ceil(gasLimit * minGasPrice) in the gas denom — otherwise the
// ante feePolicy rejects the tx with ErrInsufficientFee.
//
// VI: gắn phí cho mọi tx Signer này ký. Để trống = phí 0 (chỉ chạy khi chain
// KHÔNG ép phí tối thiểu). Khi chain bật ép phí, phải truyền >= ceil(gasLimit
// * minGasPrice) đúng denom, nếu không tx sẽ bị từ chối (ErrInsufficientFee).
func (s *Signer) WithFee(fee sdk.Coins) *Signer {
	s.feeAmount = fee
	return s
}

// Address returns the signer's bech32 account address.
//
// VI: trả địa chỉ bech32 (chuỗi "cosmos1...") suy ra từ pubkey của khoá riêng.
// Pipeline: privKey -> PubKey() -> Address() ([]byte) -> AccAddress -> String().
func (s *Signer) Address() string {
	return sdk.AccAddress(s.privKey.PubKey().Address()).String()
}

// resolveSignerInput queries the chain for account number + sequence and
// packages everything txcodec needs to sign a tx.
//
// VI: hàm phụ (chữ thường = không export) — hỏi chain xin số account &
// sequence hiện tại, rồi gói tất cả thông tin cần để ký vào struct SignerInput
// (chuyển cho lớp txcodec ký).
func (s *Signer) resolveSignerInput(ctx context.Context, client *Client) (txcodec.SignerInput, error) {
	addr := s.Address() // địa chỉ bech32 dùng để tra account.
	// FetchAccount: gọi RPC /auth/account/{addr} để lấy account_number + sequence.
	info, err := client.FetchAccount(ctx, addr)
	if err != nil {
		// fmt.Errorf với %w: BỌC lỗi gốc (giữ nguyên để errors.Is/As dùng được).
		return txcodec.SignerInput{}, fmt.Errorf("fetch account %s: %w", addr, err)
	}
	// Đóng gói mọi thứ txcodec cần để build + ký SignDoc.
	return txcodec.SignerInput{
		PrivKey:       s.privKey,
		ChainID:       s.chainID,
		AccountNumber: info.AccountNumber, // chain cấp khi tài khoản được tạo lần đầu.
		Sequence:      info.Sequence,      // tăng dần mỗi tx (chống replay).
		GasLimit:      s.gasLimit,
		FeeAmount:     s.feeAmount,
	}, nil
}
