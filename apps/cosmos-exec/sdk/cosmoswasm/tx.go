package cosmoswasm

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"

	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm/internal/txcodec"
)

// DefaultSender returns a deterministic placeholder sender address.
// Use this for development/testing; production code should use real addresses.
func DefaultSender() string {
	return txcodec.DefaultSender()
}

// BuildStoreTx builds a MsgStoreCode transaction for uploading WASM bytecode.
func BuildStoreTx(wasmByteCode []byte, sender string) ([]byte, error) {
	if len(wasmByteCode) == 0 {
		return nil, errors.New("wasm bytecode cannot be empty")
	}

	sender = txcodec.WithDefaultSender(sender)
	return txcodec.BuildProtoTxBytes(&wasmtypes.MsgStoreCode{
		Sender:       sender,
		WASMByteCode: wasmByteCode,
	})
}

// BuildInstantiateTx builds a MsgInstantiateContract transaction.
func BuildInstantiateTx(req InstantiateTxRequest) ([]byte, error) {
	if req.CodeID == 0 {
		return nil, errors.New("code id is required")
	}

	msgJSON, err := txcodec.NormalizeJSONMsg(req.Msg)
	if err != nil {
		return nil, fmt.Errorf("invalid instantiate msg: %w", err)
	}

	sender := txcodec.WithDefaultSender(req.Sender)
	label := strings.TrimSpace(req.Label)
	if label == "" {
		label = "wasm-via-sdk"
	}

	instantiate := &wasmtypes.MsgInstantiateContract{
		Sender: sender,
		CodeID: req.CodeID,
		Label:  label,
		Msg:    msgJSON,
	}

	if admin := strings.TrimSpace(req.Admin); admin != "" {
		instantiate.Admin = admin
	}

	return txcodec.BuildProtoTxBytes(instantiate)
}

// BuildExecuteTx builds a MsgExecuteContract transaction.
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
//	{"record_blob": {"commitment": "<hex>", "tag": "<tag>", ...extra}}
//
// Your WASM contract must handle a "record_blob" execute message.
func BuildBlobCommitTx(req BlobCommitTxRequest) ([]byte, error) {
	commitment := strings.TrimSpace(req.Commitment)
	if commitment == "" {
		return nil, errors.New("commitment is required")
	}
	contract := strings.TrimSpace(req.Contract)
	if contract == "" {
		return nil, errors.New("contract is required")
	}

	inner := map[string]any{
		"commitment": commitment,
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
		Msg:      map[string]any{"record_blob": inner},
	})
}

// BuildBatchRootTx builds a WASM execute transaction that records a Merkle
// batch root in a CosmWasm contract.  This is the on-chain half of CommitRoot:
// N blobs stored off-chain, one 32-byte root written to the contract.
//
// The contract message sent is:
//
//	{"record_batch": {"root": "<hex>", "count": N, "tag": "<tag>", ...extra}}
//
// Your WASM contract must handle a "record_batch" execute message.
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
		"count": req.Count,
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

// EncodeTxBase64 encodes transaction bytes as standard base64.
func EncodeTxBase64(tx []byte) string {
	return base64.StdEncoding.EncodeToString(tx)
}

// EncodeTxHex encodes transaction bytes as hex.
func EncodeTxHex(tx []byte) string {
	return hex.EncodeToString(tx)
}
