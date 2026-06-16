// package cosmoswasm: SDK client — file này gom các hàm BUILD tx WASM
// (StoreCode, Instantiate, Execute) cho cả 2 chế độ: chưa ký (dev) và đã ký
// (production). Logic ký thật nằm trong internal/txcodec.
package cosmoswasm

import (
	"context"        // truyền tín hiệu huỷ/timeout (cho hàm signed có gọi network).
	"encoding/base64"// mã hoá tx -> base64 để gửi qua JSON.
	"encoding/hex"   // mã hoá tx -> hex (cách khác để gửi).
	"errors"         // tạo lỗi đơn giản.
	"fmt"            // bọc lỗi với ngữ cảnh.
	"strings"        // TrimSpace để xử lý input người dùng.

	// wasmtypes: định nghĩa các Msg* (MsgStoreCode, MsgInstantiateContract,
	// MsgExecuteContract) — type proto chuẩn của module wasm.
	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"

	// txcodec (internal): đóng gói/ký tx, xử lý JSON msg.
	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm/internal/txcodec"
)

// DefaultSender returns a deterministic placeholder sender address.
// Use this for development/testing; production code should use real addresses.
//
// VI: trả 1 địa chỉ "placeholder" cố định để test nhanh. PRODUCTION KHÔNG nên
// dùng (mọi tx đều xuất phát từ cùng 1 address giả → không kiểm soát quyền).
func DefaultSender() string {
	return txcodec.DefaultSender()
}

// BuildStoreTx builds a MsgStoreCode transaction for uploading WASM bytecode.
//
// VI: build tx UPLOAD bytecode WASM lên chain (chưa ký). Sender rỗng → dùng
// DefaultSender. Trả []byte = tx đã proto-encode, sẵn sàng gửi /tx/submit.
func BuildStoreTx(wasmByteCode []byte, sender string) ([]byte, error) {
	if len(wasmByteCode) == 0 {
		return nil, errors.New("wasm bytecode cannot be empty")
	}

	// WithDefaultSender: nếu sender rỗng → thay bằng DefaultSender().
	sender = txcodec.WithDefaultSender(sender)
	// BuildProtoTxBytes: gói Msg vào TxBody → Tx → marshal proto. CHƯA ký.
	return txcodec.BuildProtoTxBytes(&wasmtypes.MsgStoreCode{
		Sender:       sender,
		WASMByteCode: wasmByteCode,
	})
}

// BuildInstantiateTx builds a MsgInstantiateContract transaction.
//
// VI: build tx KHỞI TẠO contract từ codeID đã upload. Msg `any` được chuẩn
// hoá thành json.RawMessage trước khi nhét vào Msg field của wasm.
func BuildInstantiateTx(req InstantiateTxRequest) ([]byte, error) {
	if req.CodeID == 0 {
		return nil, errors.New("code id is required")
	}

	// Chuẩn hoá msg về JSON hợp lệ (map/struct/[]byte đều OK).
	msgJSON, err := txcodec.NormalizeJSONMsg(req.Msg)
	if err != nil {
		return nil, fmt.Errorf("invalid instantiate msg: %w", err)
	}

	sender := txcodec.WithDefaultSender(req.Sender)
	label := strings.TrimSpace(req.Label)
	if label == "" {
		label = "wasm-via-sdk" // mặc định khi caller không truyền.
	}

	// Khởi tạo Msg và gán *theo cú pháp struct literal*.
	instantiate := &wasmtypes.MsgInstantiateContract{
		Sender: sender,
		CodeID: req.CodeID,
		Label:  label,
		Msg:    msgJSON,
	}

	// Admin TUỲ CHỌN — chỉ gán nếu caller có truyền (giữ field rỗng nếu không).
	if admin := strings.TrimSpace(req.Admin); admin != "" {
		instantiate.Admin = admin
	}

	return txcodec.BuildProtoTxBytes(instantiate)
}

// BuildExecuteTx builds a MsgExecuteContract transaction.
//
// VI: build tx GỌI contract đã instantiate. Đây là kiểu tx phổ biến nhất.
func BuildExecuteTx(req ExecuteTxRequest) ([]byte, error) {
	contract := strings.TrimSpace(req.Contract)
	if contract == "" {
		return nil, errors.New("contract is required")
	}

	msgJSON, err := txcodec.NormalizeJSONMsg(req.Msg)
	if err != nil {
		return nil, fmt.Errorf("invalid execute msg: %w", err)
	}

	return txcodec.BuildProtoTxBytes(&wasmtypes.MsgExecuteContract{
		Sender:   txcodec.WithDefaultSender(req.Sender),
		Contract: contract,
		Msg:      msgJSON,
	})
}

// BuildBlobCommitTx builds a WASM execute transaction that records a blob
// commitment in a CosmWasm contract.  This is the on-chain half of the
// blob-first pattern: large data lives in the blob store (off WASM state),
// and only a 32-byte commitment is written to the contract.
//
// The contract message sent is:
//
//	{"record_blob": {"commitment": "<hex>", "height": <da_height>,
//	                 "namespace": "<hex>", "tag": "<tag>", ...extra}}
//
// Your WASM contract must handle a "record_blob" execute message. Recording
// `height` (and `namespace`) on-chain is what lets anyone later retrieve the
// blob from Celestia via Blob.Get(height, namespace, commitment) — commitment
// alone is not enough.
//
// VI: build tx ghi 1 "blob commitment" (cam kết Celestia) vào contract WASM.
// Mô hình "blob-first": dữ liệu LỚN nằm trên Celestia (ngoài state WASM), CHỈ
// commitment + height được ghi on-chain → tiết kiệm gas + giảm phình state.
// Ghi cả `height` (và `namespace`) vì retrieve cần đủ (height, namespace,
// commitment). Contract WASM PHẢI có handler "record_blob".
func BuildBlobCommitTx(req BlobCommitTxRequest) ([]byte, error) {
	commitment := strings.TrimSpace(req.Commitment)
	if commitment == "" {
		return nil, errors.New("commitment is required")
	}
	contract := strings.TrimSpace(req.Contract)
	if contract == "" {
		return nil, errors.New("contract is required")
	}

	// inner: payload bên trong khoá "record_blob".
	inner := map[string]any{
		"commitment": commitment,
	}
	// height/namespace là khoá để retrieve blob từ Celestia sau này.
	if req.Height > 0 {
		inner["height"] = req.Height
	}
	if ns := strings.TrimSpace(req.Namespace); ns != "" {
		inner["namespace"] = ns
	}
	if tag := strings.TrimSpace(req.Tag); tag != "" {
		inner["tag"] = tag
	}
	// Cho phép caller thêm field tuỳ ý qua Extra (vd: timestamp, version).
	for k, v := range req.Extra {
		inner[k] = v
	}

	// Tận dụng BuildExecuteTx — chỉ khác cấu trúc msg.
	return BuildExecuteTx(ExecuteTxRequest{
		Sender:   req.Sender,
		Contract: contract,
		Msg:      map[string]any{"record_blob": inner},
	})
}

// BuildBatchRootTx builds a WASM execute transaction that records a Merkle
// batch root in a CosmWasm contract.  This is the on-chain half of SubmitBatch:
// N blobs stored off-chain, one 32-byte root written to the contract.
//
// The contract message sent is:
//
//	{"record_batch": {"root": "<hex>", "count": N, "tag": "<tag>", ...extra}}
//
// Your WASM contract must handle a "record_batch" execute message.
//
// VI: tương tự BuildBlobCommitTx nhưng cho 1 ROOT cây Merkle gom N blob.
// 1 root đại diện cho N blob → tiết kiệm hơn nữa khi gom batch lớn.
func BuildBatchRootTx(req BatchRootTxRequest) ([]byte, error) {
	root := strings.TrimSpace(req.Root)
	if root == "" {
		return nil, errors.New("root is required")
	}
	contract := strings.TrimSpace(req.Contract)
	if contract == "" {
		return nil, errors.New("contract is required")
	}

	inner := map[string]any{
		"root":  root,
		"count": req.Count, // số blob trong batch (để contract verify cây).
	}
	// height là khoá để retrieve các blob của batch từ Celestia sau này.
	if req.Height > 0 {
		inner["height"] = req.Height
	}
	if ns := strings.TrimSpace(req.Namespace); ns != "" {
		inner["namespace"] = ns
	}
	if tag := strings.TrimSpace(req.Tag); tag != "" {
		inner["tag"] = tag
	}
	for k, v := range req.Extra {
		inner[k] = v
	}

	return BuildExecuteTx(ExecuteTxRequest{
		Sender:   req.Sender,
		Contract: contract,
		Msg:      map[string]any{"record_batch": inner},
	})
}

// BuildSignedStoreTx is the signed counterpart of BuildStoreTx. It queries the
// signer's on-chain account for account_number + sequence, then produces a
// fully signed TxRaw. Use this when the cosmos-exec AnteHandler enforces
// signatures (Level 2 hardening).
//
// VI: phiên bản CÓ KÝ. Gọi network để lấy account_number + sequence từ chain
// (qua signer.resolveSignerInput → client.FetchAccount) rồi ký bằng privKey.
// Dùng khi chain bật COSMOS_EXEC_ENFORCE_SIGNATURES.
func BuildSignedStoreTx(ctx context.Context, client *Client, signer *Signer, wasmByteCode []byte) ([]byte, error) {
	if signer == nil {
		return nil, errors.New("signer is required")
	}
	if len(wasmByteCode) == 0 {
		return nil, errors.New("wasm bytecode cannot be empty")
	}
	in, err := signer.resolveSignerInput(ctx, client) // hỏi chain xin số.
	if err != nil {
		return nil, err
	}
	return txcodec.BuildSignedProtoTxBytes(in, &wasmtypes.MsgStoreCode{
		Sender:       signer.Address(), // sender = địa chỉ DERIVE từ pubkey.
		WASMByteCode: wasmByteCode,
	})
}

// BuildSignedInstantiateTx is the signed counterpart of BuildInstantiateTx.
// req.Sender is ignored — the signer's derived address is used.
//
// VI: như BuildInstantiateTx nhưng có ký. req.Sender bị BỎ QUA — bắt buộc
// dùng signer.Address() để khớp chữ ký (an toàn, tránh sender giả).
func BuildSignedInstantiateTx(ctx context.Context, client *Client, signer *Signer, req InstantiateTxRequest) ([]byte, error) {
	if signer == nil {
		return nil, errors.New("signer is required")
	}
	if req.CodeID == 0 {
		return nil, errors.New("code id is required")
	}
	msgJSON, err := txcodec.NormalizeJSONMsg(req.Msg)
	if err != nil {
		return nil, fmt.Errorf("invalid instantiate msg: %w", err)
	}
	label := strings.TrimSpace(req.Label)
	if label == "" {
		label = "wasm-via-sdk"
	}
	in, err := signer.resolveSignerInput(ctx, client)
	if err != nil {
		return nil, err
	}
	instantiate := &wasmtypes.MsgInstantiateContract{
		Sender: signer.Address(),
		CodeID: req.CodeID,
		Label:  label,
		Msg:    msgJSON,
	}
	if admin := strings.TrimSpace(req.Admin); admin != "" {
		instantiate.Admin = admin
	}
	return txcodec.BuildSignedProtoTxBytes(in, instantiate)
}

// BuildSignedExecuteTx is the signed counterpart of BuildExecuteTx.
// req.Sender is ignored — the signer's derived address is used.
//
// VI: như BuildExecuteTx nhưng có ký. req.Sender bị bỏ qua tương tự.
func BuildSignedExecuteTx(ctx context.Context, client *Client, signer *Signer, req ExecuteTxRequest) ([]byte, error) {
	if signer == nil {
		return nil, errors.New("signer is required")
	}
	contract := strings.TrimSpace(req.Contract)
	if contract == "" {
		return nil, errors.New("contract is required")
	}
	msgJSON, err := txcodec.NormalizeJSONMsg(req.Msg)
	if err != nil {
		return nil, fmt.Errorf("invalid execute msg: %w", err)
	}
	in, err := signer.resolveSignerInput(ctx, client)
	if err != nil {
		return nil, err
	}
	return txcodec.BuildSignedProtoTxBytes(in, &wasmtypes.MsgExecuteContract{
		Sender:   signer.Address(),
		Contract: contract,
		Msg:      msgJSON,
	})
}

// EncodeTxBase64 encodes transaction bytes as standard base64.
// VI: []byte → chuỗi base64 (chuẩn). Dùng cho JSON body /tx/submit.
func EncodeTxBase64(tx []byte) string {
	return base64.StdEncoding.EncodeToString(tx)
}

// EncodeTxHex encodes transaction bytes as hex.
// VI: []byte → chuỗi hex thường (vd "abcd..."). Dùng khi caller thích hex.
func EncodeTxHex(tx []byte) string {
	return hex.EncodeToString(tx)
}
